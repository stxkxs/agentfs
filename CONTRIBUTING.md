# Contributing to agentfs

agentfs watches directories the operator does not control the contents of, and publishes a contract
other people's agents write against. Both make the same demand of a change: what the code does has to
be what the documentation says, and a test has to hold the two together. The conventions below are
how that is arranged.

---

## What you need

| Tool | Used by | Requirement |
| --- | --- | --- |
| Go | everything | 1.26 or later, matching the `go` directive in `go.mod` |
| [Task](https://taskfile.dev) | every phase below | 3.x |
| [golangci-lint](https://golangci-lint.run) | `task lint`, `task fmt` | v2.13.1, the version the gate runs |
| [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) | `task vuln` | `go install golang.org/x/vuln/cmd/govulncheck@latest` |

`task --list` prints every task with what it does. Nothing in the repository needs a dependency
outside `go.mod`; do not add one.

---

## The gate

Four phases. Each exits 0 from a clean tree, and `task ci` runs the whole set in order.

```sh
task ci
```

### build

```sh
task build      # dist/agentfs, with version, commit and build date stamped in
task run -- ./workspace
```

`task build` compiles with `-trimpath` and link-time stamps that `agentfs version` prints. Windows is
a compile target: `GOOS=windows go build ./...` has to succeed, though the confinement tests that rest
on `os.Root` run on Linux and macOS.

### lint

```sh
task fmt        # apply the formatters
task vet
task lint
```

`.golangci.yml` is the configuration. `gofmt` and `goimports` are the formatters, with
`github.com/stxkxs/agentfs` grouped last. A `//nolint` needs a specific linter and an explanation —
`nolintlint` refuses a bare one, and an unused one.

### test

```sh
task test
task test:race  # -race -shuffle=on, which is what the gate runs
task cover      # statement coverage against the floors in coverage.yaml
task fuzz       # explore beyond the seed corpora, for a bounded time
```

Every fuzz target's seed corpus runs as an ordinary test under `task test`, so an input that once
found a defect gates every commit. `task fuzz` is the search beyond those seeds: it has a time budget,
its result depends on what else the machine is doing, and it is therefore not part of `task ci`. CI
runs it as its own job, where a found input is uploaded and becomes a seed.

`task fuzz` fails when the tree declares no fuzz target, so the search cannot pass by having nothing
to run. An input it finds is written under the target's `testdata/fuzz/` and belongs in the commit:
the crash is only reproducible with the bytes that caused it.

### docs

```sh
task gen        # regenerate schema/ and docs/reference/
task verify     # formatting, module hygiene, and generated files matching their generators
```

`task verify` runs `task gen` and then fails when `git diff` reports a change under
`schema` or `docs/reference`. Commit the regenerated files alongside the change that moved them.

`go test .` — the root package — checks the claims the repository makes about itself: every internal
package the README names exists, every document it links to is there, no markdown file carries session
narration or a time-relative phrase, and every code fence is closed.

---

## Generated reference

Everything under `docs/reference/` and `schema/` is written by a generator. Editing a generated page
is lost at the next `task gen`, and `task verify` fails on the difference.

Edit the Go table instead. Each is the single source for the decoder, the flag registry or the
renderer as well as for the page, so the code and the reference cannot disagree:

| What the page describes | The table to edit |
| --- | --- |
| The state contract's members | `agentstate.Rules` in `internal/agentstate/state.go` |
| The status vocabulary and its compatibility spellings | `internal/agentstate/status.go` |
| Flags, environment variables, defaults | `limitSpecs` in `internal/config/limit.go` |
| Diagnostic codes | `registry` in `internal/diag/diag.go` |
| Exit codes | `registry` in `internal/report/code.go` |
| Key bindings | `defaultBindings` in `internal/ui/keys/table.go` |

Then run `task gen`.

---

## Test conventions

**No wall-clock waiting.** A test that involves time runs inside a [`testing/synctest`][synctest]
bubble. Inside one, `synctest.Wait` reports that everything runnable has run, and `time.Sleep`
advances a fake clock at no real cost — so "the coalescing window closed" is a condition rather than a
duration to guess at. `internal/watch/engine_sync_test.go` and `internal/ui/app/harness_test.go` are
the worked examples.

**No temporary directories in a unit test.** Tests build a workspace as a
[`testing/fstest.MapFS`][fstest] and hand it to `fsx.New`. The filesystem seam is what makes that
possible: `fsx.FS` is the read-only capability set agentfs needs, and both `os.Root.FS` and
`fstest.MapFS` satisfy it, so every layer above `internal/fsx` sees the same type in a test as in
production. A test that reaches for `t.TempDir` is asserting something about the real filesystem —
`internal/fsx/confinement_test.go` holds `os.Root` to refusing a symlink that leaves the workspace,
and `internal/buildinfo/stamp_test.go` builds a binary to hold the linker to its stamps. Those are the
shape of the exception.

**Assert on the contract, not on the prose.** The conformance corpus compares diagnostic codes and
JSON Pointers, because those are what a consumer branches on. A test that matched on message text
would fail on a reworded sentence and pass on a changed meaning.

[synctest]: https://pkg.go.dev/testing/synctest
[fstest]: https://pkg.go.dev/testing/fstest

---

## Adding a flag

A setting that is not in the limits table cannot be set from the command line, cannot be read from the
environment, and cannot appear in the reference. Three edits and a generator run:

1. **A field on `config.Config`** in `internal/config/config.go`, with a value in `Defaults`.
2. **A row in `limitSpecs`** in `internal/config/limit.go`, at the same position the field sits at in
   the struct. The row carries the flag name, the environment variable, the unit, the floor a value
   must clear, and a summary. The summary opens with the field name, ends in a full stop, and — where
   the value is a number — says why the default is the number it is.
3. **`task gen`**, which rewrites the reference page from the table.

The flag and the environment variable follow from the field name: `MaxEntriesPerDir` gives
`-max-entries-per-dir` and `AGENTFS_MAX_ENTRIES_PER_DIR`. `TestLimitsNamesEveryConfigField` holds the
table to one row per field in field order, and the tests beside it hold the unit to the field's Go
type, the names to the field name, an enum row to its whole vocabulary, and the summary to its shape.
A relation between two settings — a preview that cannot outsize a read, a backoff that cannot start
above its ceiling — belongs in `relations` in `internal/config/validate.go`.

Nothing else registers the flag: `bind` in `internal/cli/flags.go` walks `config.Limits()`.

---

## Adding a diagnostic code

A code is permanent. It is retired rather than reused, so a consumer that suppresses one never has it
come to mean something else.

1. **A `diag.Code` constant** in `internal/diag/diag.go`, in the band its layer owns — 1xxx document
   well-formedness, 2xxx member typing, 3xxx member semantics, 4xxx observation, 5xxx resource
   ceilings.
2. **A row in `registry`** in the same file: the severity the code is raised at, one sentence naming
   the condition, and whether the condition is semantic — one no JSON Schema validator can raise, which
   is how the schema-agreement test knows to exclude it rather than fail on it.
3. **A conformance case** under `internal/conformance/testdata/cases/<name>/`: a `state.json` that provokes the
   code, and an `expect.json` recording the codes and JSON Pointers the decoder reports, the status the
   document decodes to, and one sentence saying what the case asserts.
4. **A `fixed/state.json`** in the case directory when any diagnostic the document provokes carries a
   hint. `TestHintsAreActionable` decodes it and fails when it still raises an error, or still raises a
   code a hint claimed to resolve. A hint that leaves its own finding standing is decoration.

`TestEveryCodeHasACase` fails on a registered code no case covers. A code describing a condition
outside the document — the root vanishing, a ceiling being crossed, a read failing — has no document
that can provoke it, and belongs in `exemptCodes` with the reason it cannot be reached from a case.
The exemption list is checked in both directions: an exemption a case covers fails as well.

The corpus is an evaluation suite, not a fixture set. `minimumCases` fails a change that deletes cases
to make a decoder edit pass.

---

## The coverage gate

```sh
task cover               # measure, print the table, enforce the floors
task cover -- -ratchet   # also fail on a drop, and record an improvement
```

`coverage.yaml` holds two numbers per package, and they are moved by different hands. The **floor** is
the contract a reviewer agreed to and is edited by hand; nothing lowers one automatically. The
**recorded** value is the high-water mark `-ratchet` writes, and a package that falls below its own
recorded value fails — including one that leaves the profile altogether, which is how deleting a
package's tests is caught rather than rewarded.

A floor of 0 gates a package without setting a bar, which is how the gate goes in before the tests it
gates. Raising one as tests land is a review decision on a single line.

---

## Prose

Every prose surface is held to the same rules: comments, package docs, flag summaries, error and log
messages, Taskfile descriptions, test names, CI step names, markdown.

- **Timeless.** No *currently, now, new, soon, eventually, latest, older, previously*. Do not describe
  what something replaced, or how it came to be this way. Write as though the code was always like
  this. A stated requirement or version — "Go 1.26 or later" — is the exception.
- **No session narration.** The reader was not in the room: no recalled conversation, no reported
  discovery, no decision attributed to a discussion. `TestProseIsTimeless` in `docs_test.go` carries
  the refused phrasings and fails any markdown file holding one — read the list there before writing
  a document.
- **No self-defence.** Do not argue with an imagined reviewer. State the constraint; the constraint is
  the argument.
- **Assert or say nothing.** No *I think, probably, hopefully, should work*. Attributed uncertainty
  about an external system is fine: "NFS attribute caching may delay this by `acdirmax` seconds".
- **Rationale stays, narration goes.** A sentence earns its place by telling the reader something they
  need in order to use or change the thing correctly.
- **No measurement presented as documentation.** "Reports four findings" is true when written and
  nothing keeps it true. State the requirement, or give the command that answers the question.
- **Named things resolve.** Every path, flag, command, function and field named in prose is a claim
  about the world. Run the command or grep the code before you write the sentence.

---

## The claim about network filesystems

agentfs claims to work on NFS, EFS and S3-backed FUSE mounts. Kernel change notification reports
activity passing through *this* kernel's VFS, so a write by another client of a network export raises
no event here. What makes the claim true is that `Config.FilesystemMode` resolves `auto` to
notification alone only on a filesystem `fsx.Classify` recognizes as local, and to notification plus a
bounded stat sweep on every other kind, including one it does not recognize.

The sweep covers the tracked set — the directories being displayed — so its cost is a function of what
is on screen rather than of workspace size, and its limit is that a change inside a collapsed directory
is reported when the directory is opened rather than immediately. A change that touches detection has
to keep the mechanism, the cost and that limit stated together. `TestFilesystemClaimsNameTheirDetectionMode`
fails a document that names a network filesystem without naming how change on it is observed.

---

## Trying it

```sh
go build -o /tmp/agentfs .
./scripts/demo-agents.sh /tmp/agentfs-workspace &
/tmp/agentfs /tmp/agentfs-workspace
```

`scripts/demo-agents.sh` — also `task demo -- <directory>` — fills a workspace with five agents
covering the status vocabulary, and writes every document atomically, which is what the contract asks
of an integrator. `/tmp/agentfs validate /tmp/agentfs-workspace` reports no findings against it.

---

## Commits

Explain what changed and why, with file-level detail where it matters. A bug fix names the bug, the
root cause and the fix. Scale the length to the scope of the change. Keep internal-only provenance —
ticket ids, review threads, personal names — out of the message body and out of shipped prose.
