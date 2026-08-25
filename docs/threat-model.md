# Threat model

agentfs reads a directory the operator may not control, written by autonomous agents, and renders it
into a terminal. This page names each boundary, what crosses it, the control, and the test that checks
the control.

A row without a test is an assertion. Every row here names one, and each name resolves under
`go test -list '.*' ./...`.

## What agentfs is

A read-only observer. It opens one directory, reads files below it, and writes to standard output.
It opens no socket, runs no subprocess, and creates no file inside a workspace.

## Trust boundaries

| # | Boundary | What crosses it |
| --- | --- | --- |
| 1 | Workspace → agentfs | File names, directory structure, file bytes, state documents |
| 2 | agentfs → terminal | Rendered cells |
| 3 | agentfs → standard output | JSON and NDJSON read by other programs |
| 4 | Operator → agentfs | The path, flags, and environment |

---

## 1. Terminal escape injection (STRIDE: Tampering, Elevation)

**Threat.** A log line or filename carries an ANSI sequence. `OSC 52` writes the system clipboard.
`CSI 2J` clears the screen. `DCS` and `APC` payloads can drive a terminal's own extensions. The content
is written by an autonomous agent, so it is attacker-influenced whenever the agent's inputs are.

**Control.** `textx.Sanitize` removes CSI, OSC, DCS, APC, PM and SOS sequences and two-byte escapes,
and replaces every remaining C0 and C1 control with a visible stand-in. Every path from a workspace
byte to the screen passes through it: `fileview` sanitizes each line as it loads, and the panes
sanitize names and declared values before styling them.

Sanitizing cannot happen at the render boundary, because by then a line also carries the palette's own
escapes. The panes are the single point where workspace bytes become styled text.

**Tests.** `TestSanitizeMarksTerminalSequences`, `FuzzSanitize` (asserts no escape, C0, C1 or bidi
control survives any input, and that sanitizing is idempotent), `TestFrameSurvivesHostileContent`
(drives a hostile workspace through the whole model and asserts no escape reaches the frame),
`TestPlainPaletteEmitsNoEscapes`.

---

## 2. Display spoofing through Unicode (STRIDE: Spoofing)

**Threat.** A right-to-left override in a filename makes `gpj.exe` render as `exe.jpg`. Zero-width
characters hide content between rendered runes.

**Control.** `textx.Sanitize` replaces the bidirectional formatting controls (U+202A–U+202E,
U+2066–U+2069, U+200E, U+200F) with a visible marker, and the zero-width format characters
(U+200B–U+200D, U+2060, U+FEFF, U+00AD) with another. Both are one cell wide, so a substitution cannot
shift the columns to its right.

Text is rendered in logical order and never reordered. Width is measured in terminal cells, so a
double-width rune is never split.

**Machine-readable output.** A JSON encoder escapes the C0 controls, so an ESC in an envelope or a
record is already inert, but it leaves the bidirectional and zero-width format characters raw — and a
consumer that prints a member into a terminal is handed the override. `report.escapeInvisible`
rewrites them into their `\uXXXX` escapes on the way out of every envelope and every record.
Escaping rather than replacing keeps the value recoverable, which a path member has to be: a consumer
that decodes the JSON gets the rune the workspace wrote, and one that cats the stream sees six ASCII
characters.

**Tests.** `TestSanitizeReplacesBidiOverrides`, `TestSanitizeReplacesZeroWidth`, `FuzzSanitize`,
`FuzzFit` (asserts a sanitized line always fits exactly the requested cell count),
`TestEnvelopeEscapesTheRunesATerminalActsOn`, `TestStreamEscapesTheRunesATerminalReordersAround`,
`TestTheStreamWritesNoEscapeFromWorkspaceContent` (drives a hostile workspace through the record
stream and asserts no byte a terminal acts on reaches it).

---

## 3. Path escape (STRIDE: Information disclosure)

**Threat.** A symlink inside the workspace points outside it. Reading it discloses a file the operator
did not point agentfs at, which matters because the documented use is a shared mount.

**Control.** The root is an `os.Root`. Its resolution refuses any path that leaves the root, including
one reached through a symlink — a property `os.DirFS` does not have. Separately, the tree records
symlinks and never follows them: a link is a leaf whatever it points at, which also makes a link cycle
unrepresentable rather than merely bounded.

Change events are normalized through `fsx.Clean`, which drops any path that is absolute or climbs out,
so an event naming a path outside the workspace is discarded rather than resolved.

**Tests.** `TestSymlinkEscapeIsRefused` (against a real filesystem, asserting the escape `os.DirFS`
resolves is refused), `TestSymlinkIsNeverFollowed`, `TestCleanRejectsEscapingPaths`, `FuzzClean`.

**Platform.** Asserted on Linux and macOS. Windows is compiled and not exercised; `agentfs doctor`
says so on the running binary, and [platforms.md](reference/platforms.md) states it.

**Toolchain.** The confinement property is the standard library's, so it is only as good as the
toolchain that provides it. `go.mod` requires a Go release in which the known root-escape defects are
fixed, and `task vuln` fails the build on a toolchain carrying one. A stale toolchain is a hole in
this control, not merely an out-of-date build.

---

## 4. Write-back to an observed workspace (STRIDE: Tampering)

**Threat.** An observer that can write can corrupt what it is watching.

**Control.** Structural rather than conventional: the filesystem seam `fsx.FS` declares no method that
writes. No code above it can create, modify or remove a file in a workspace, because no such method
exists to call.

`os` reaches the filesystem without passing through the seam, so the lint configuration closes that
route too. The `depguard` rule `no-write-outside-the-seam` refuses `os/exec`, and four `forbidigo`
patterns refuse every mutating call `os` offers: the ones that create or overwrite a file
(`os.Create`, `os.CreateTemp`, `os.OpenFile`, `os.Truncate`, `os.WriteFile`), the ones that create a
directory or a link (`os.Link`, `os.Mkdir`, `os.MkdirAll`, `os.MkdirTemp`, `os.Symlink`), the ones
that remove or move an entry (`os.Remove`, `os.RemoveAll`, `os.Rename`), and the ones that change an
entry's metadata (`os.Chmod`, `os.Chown`, `os.Chtimes`). Both denials are lifted in exactly two
places: the generators under `internal/tools`, which are not linked into the binary and write into
the repository rather than into a workspace, and test files, which are not linked into it either.
Every file the binary is compiled from, `main.go` included, is covered.

**Tests.** `TestFSSeamHasNoWriteMethod` (reflects over the interface and fails on any write method),
and `golangci-lint run ./...`, which fails the build on a write or a subprocess anywhere the binary
is compiled from.

---

## 5. Network exfiltration (STRIDE: Information disclosure)

**Threat.** A workspace is read into a process that can reach the network.

**Control.** The `depguard` rule `no-network` denies `net`, `net/http` and `net/url` in every
non-test file. The absence of a socket is enforced by the build rather than left as a property that
happens to hold.

**Test.** `golangci-lint run ./...` — a network import fails the build.

---

## 6. Resource exhaustion (STRIDE: Denial of service)

**Threat.** A workspace with four hundred thousand files, a directory with a million entries, a
four-gigabyte log, a symlink cycle, a state document that is one enormous string, or an agent writing
fifty times a second.

**Control.** Every dimension has a ceiling in the `config` table, and every ceiling reached is rendered
on the status line rather than silently applied:

| Dimension | Ceiling | Reported as |
| --- | --- | --- |
| Tree depth | `--max-depth` | `AFS5014` |
| Directory entries | `--max-entries-per-dir` | `AFS5013` |
| Tree nodes | `--max-nodes` | `AFS5012` |
| Kernel watches | `--max-watches` | `AFS5015` |
| Batch size | `--max-batch` | `AFS5016` |
| Changes held between discovery and delivery | `--max-queue` | `AFS5016` |
| File bytes read | `--max-preview-bytes` | the preview badge |
| State document bytes | `--max-document-bytes` | `AFS1008` |
| Preserved unknown members | `--max-extra-bytes` | `AFS1006` |
| Feed entries | `--max-feed-entries` | the feed badge |
| Sweep operations per cycle | `--sweep-budget` | the status line |

Startup reads exactly one directory whatever the workspace holds, and a change batch reloads only the
directories it names, so cost follows the viewport rather than the workspace.

**Tests.** `TestDepthCeilingStopsDescent`, `TestEntryCeilingMarksTheDirectoryTruncated`,
`TestLoadBoundsAHugeFile`, `TestBuilderOverflowSetsTruncatedAndResync`,
`TestAFullQueueIsReportedRatherThanAbsorbed`,
`TestNewReadCountIsIndependentOfWorkspaceSize`, `TestApplyIsBoundedByTheBatch`,
`TestSweepCostIsIndependentOfWorkspaceSize`, `TestFeedReportsWhatItDiscarded`,
`TestEveryCeilingRejectsNonPositive`.

---

## 7. Malformed and hostile documents (STRIDE: Tampering, Denial of service)

**Threat.** A state document that is not JSON, is JSON but not an object, declares a status outside the
vocabulary, or is being rewritten as it is read.

**Control.** Decoding is member-by-member: a document with several bad members yields a diagnostic for
each rather than stopping at the first. Nothing panics on any input. A status is matched exactly
against a closed vocabulary, never by substring. A well-formedness error is withheld until two
readings agree, so a torn write is reported as settling rather than as invalid.

**Tests.** `FuzzDecode` (asserts no panic and no unregistered code on arbitrary input),
`TestThreeBadMembersYieldThreeDiagnostics`, `TestStatusMatchIsExact`,
`TestTornWriteDoesNotFlapTheStatus`, `TestASettledSyntaxErrorIsReported`, and the conformance corpus.

---

## 8. Secrets on a shared terminal (STRIDE: Information disclosure)

**Threat.** A workspace contains a token in a log line or a state document. agentfs renders it, and a
shared terminal, a screen share or a recording captures it.

**Position, not control.** agentfs renders what the workspace contains. `--redact-keys` names the
document members whose quoted values are masked, and it carries a default list — the spellings a
credential is written under by the SDKs agents are built on — so a token an agent writes into a
member called `api_key` or `authorization` is masked without being asked for.

That masking reaches one surface: the file preview, where `fileview.Redact` rewrites each line before
it is lexed. It is a convenience on that one pane, not a boundary around the workspace. A credential
reaches the screen unmasked when it is written under a member name the list does not carry, when it
is a number or an object rather than a quoted string, when it is in a filename or a directory name,
and when it is in a member the agent bar renders. The one-shot JSON output redacts nothing at all.

So the statement that holds is: **treat a workspace as sensitive as the agents that write it.**

---

## 9. Operator input (STRIDE: Denial of service)

**Threat.** A flag or environment variable that puts agentfs into a state that never terminates.

**Control.** Configuration is validated at startup, reporting every invalid field rather than the
first. A sweep interval below its floor is refused, so a polling loop cannot be configured into a
storm. An environment variable that does not parse is ignored rather than fatal: an exported value is
ambient, and refusing to start because of an unrelated shell setting would be worse than proceeding
with the default.

**Tests.** `TestSweepIntervalFloor`, `TestEveryCeilingRejectsNonPositive`,
`TestValidateAcceptsEveryEnumSpelling`.

---

## Non-goals

Stated so nobody mistakes them for gaps:

- agentfs does **not** sandbox the agents that write a workspace.
- agentfs does **not** authenticate the writer of a state document. Anything that can write the
  directory can declare anything.
- agentfs does **not** verify that a state document is truthful. It reports what was declared.
- agentfs does **not** verify artifacts it did not produce.
- agentfs offers **no** protection against a workspace the operator should not have been able to read.
  It reads with the operator's own credentials and nothing more.

## Reporting

Security issues: open an issue at https://github.com/stxkxs/agentfs/issues.
