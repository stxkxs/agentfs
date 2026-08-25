# CLAUDE.md

Instructions for an agent changing agentfs. `CONTRIBUTING.md` has the same conventions at length.

## What this is

A Go terminal UI that watches AI-agent workspaces on disk, and the contract those agents write
against. The contract is the product: `internal/agentstate` declares every member, the vocabulary,
the decoder and the published JSON Schema, and every reader in the tree goes through it rather than
matching filenames of its own.

## Before you change anything

- **Read the code you are about to describe.** Every path, flag, command, function and field you name
  in prose is a claim. Run the command or grep the code first. A name that does not resolve is the
  defect the docs tests exist to catch.
- **Add no dependencies.** No `go get`, no `go mod tidy` to pull something in. Everything outside
  `go.mod` is out.
- **Do not edit generated files.** `docs/reference/` and `schema/` are written by
  `internal/tools/docsgen` and `internal/tools/schemagen`. Edit the Go table that feeds them, then run
  `task gen`.

## The gate

```sh
task ci        # verify, lint, race tests, coverage floors, vulnerabilities
```

Or one phase at a time: `task build`, `task lint`, `task test:race`, `task cover`,
`task gen`, `task verify`. Each exits 0 from a clean tree. `task verify` regenerates and then fails on
any difference under `schema` or `docs/reference`, so commit what it regenerates.

`go test .` checks the repository's claims about itself: named packages exist, linked documents exist,
markdown carries no session narration, code fences are closed.

## Package map

| Package | Holds |
| --- | --- |
| `internal/agentstate` | The contract: `Rules`, `Decode`, `SchemaJSON`, the status vocabulary |
| `internal/diag` | Codes, severities, `Sink`, RFC 6901 pointer resolution |
| `internal/fsx` | The read-only filesystem seam, `os.Root` confinement, `Classify` |
| `internal/config` | `limitSpecs`: every ceiling with its flag, environment variable and default |
| `internal/workspace` | Agent and run discovery, the settle rule, staleness |
| `internal/watch` | Notify, sweep and hybrid observation; coalescing |
| `internal/index` | The lazy incremental tree |
| `internal/fileview` | Bounded file windows, tailing, lexers |
| `internal/report` | Exit codes, the JSON envelope, the NDJSON stream |
| `internal/textx` | `Sanitize`, `Fit`, `FindAll` — rune-safe text handling |
| `internal/ui/*` | Theme, layout, keys, render, panes, the model |
| `internal/cli` | Command table, flag registry, exit codes |

## Tests

- **Never sleep against the wall clock.** A test that involves time runs inside a `testing/synctest`
  bubble, where `synctest.Wait` reports quiescence and `time.Sleep` costs nothing.
  `internal/watch/engine_sync_test.go` is the pattern.
- **Never build a temporary directory for a unit test.** Construct a `testing/fstest.MapFS` and pass
  it to `fsx.New`. `t.TempDir` belongs only in a test whose subject is the real filesystem, such as
  `internal/fsx/confinement_test.go`.
- **Assert on codes and pointers, not on message text.** Messages and hints are prose and carry no
  contract.
- Coverage floors live in `coverage.yaml`. Raise one by review; `task cover -- -ratchet` records a
  high-water mark and fails a regression.

## Adding a flag

A field on `config.Config`, a row in `limitSpecs` at the same position, then `task gen`. The flag and
the environment variable follow from the field name. Nothing else registers it — `bind` in
`internal/cli/flags.go` walks `config.Limits()`.

## Adding a diagnostic code

A `diag.Code` constant in its numeric band, a `registry` row, and a case under
`internal/conformance/testdata/cases/`. When any diagnostic the case provokes carries a hint, add
`fixed/state.json` showing the document those hints produce — the suite decodes it and fails when a
hint left its own finding standing. A code no document can provoke goes in `exemptCodes` with the
reason.

Codes are permanent. Retire one rather than reusing it.

## The network-filesystem claim

agentfs claims NFS, EFS and S3-backed FUSE mounts work. Kernel notification reports only this
kernel's VFS activity, so another client's write to a network export raises no event here.
`Config.FilesystemMode` resolves `auto` to notification alone only where `fsx.Classify` reports a
local filesystem, and to notification plus a bounded stat sweep everywhere else, an unrecognized
filesystem included.

State the mechanism, its cost and its limit together. The sweep covers the tracked set — the
directories on screen — so a change inside a collapsed directory surfaces when it is opened rather
than immediately. Never name a network filesystem without naming how change on it is observed.

## Prose

Applies to comments, package docs, flag summaries, error and log messages, task descriptions, test
names, markdown.

- **Timeless.** No *currently, now, new, soon, eventually, latest, older, previously*. Do not describe
  a rewrite, the old code, a migration, or what something replaced. Write as though the code was
  always this way. A stated requirement or version is the exception.
- **No session narration.** The reader was not in the room: no recalled conversation, no reported
  discovery, no decision attributed to a discussion. `TestProseIsTimeless` in `docs_test.go` carries
  the refused phrasings and fails any markdown file holding one.
- **No self-defence.** Do not argue with an imagined reviewer. State the constraint; the constraint is
  the argument.
- **Assert or say nothing.** No *I think, probably, hopefully, should work*. Attributed uncertainty
  about an external system is fine.
- **Rationale stays, narration goes.** A sentence earns its place by telling the reader something they
  need in order to use or change the thing correctly.
- **No measurement as documentation.** "Reports four findings" goes stale at the first edit. State the
  requirement, or give the command that answers the question.
- **No internal-only provenance.** No ticket ids, review threads or personal names in shipped prose.

## Trying it

```sh
go build -o /tmp/agentfs .
./scripts/demo-agents.sh /tmp/agentfs-workspace &
/tmp/agentfs /tmp/agentfs-workspace
/tmp/agentfs validate /tmp/agentfs-workspace
```

The demo writes conforming documents atomically — a temporary file in the same directory, then a
rename. Keep it that way: it is what the contract asks of an integrator, and a demo that tore its own
writes would contradict the guidance it exists to illustrate.
