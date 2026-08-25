# Limits

Every quantity agentfs works with is chosen by somebody else. How many entries a directory holds, how
large a file is, how fast an agent writes, how deep a tree goes — the workspace decides all of it, and
the workspace is written by autonomous processes with no obligation to be reasonable. A reader with
no ceilings has its memory and its frame rate set by whatever the agents happen to do.

So agentfs states a number for each of those quantities. That is what a ceiling is: the point at
which agentfs stops doing work chosen by the workspace and reports that it stopped. The reference
lists the flags and defaults; this document is why each one exists and what reaching it looks like.

## One table, not a scattering of constants

`config.Limits()` is the registry. Each row carries the Go field name, the flag, the environment
variable, the unit, the default rendered in that unit, and one sentence of prose. Flag registration
(`cli.bind`), the help text (`cli.writeFlags`) and the generated reference all read that one table,
and a test asserts by reflection that the table names every field of `config.Config`
(`TestLimitsNamesEveryConfigField`).

Two properties follow. A ceiling that is not in the table cannot be set from the command line and
cannot appear in the reference. And a field added to `config.Config` without a row fails the suite
rather than shipping as an undocumented knob.

`TestEverySettingHasAConsumer` closes the other direction. It parses every package outside
`internal/config` that imports it and requires each row's field to be read from a `config.Config` in
one of them, so a setting the reference publishes and nothing applies fails the suite too. A flag an
operator can set, a summary the reference prints and a behaviour the binary does not have is the
failure a table of ceilings is most exposed to, because a ceiling is easier to describe than to
apply.

The scan counts a read and not an appearance. A package qualifier (`fsx.Root`), a field of another
struct (`app.Options.Root`) and the left of an assignment are all excluded, because each of them
would let a row pass on a name that is not a setting's. What stays outside its reach is reflection:
`cli.bind` and `cli.applyEnv` reach every field through `reflect.Value.FieldByName`, and counting
that would satisfy every row at once.

Every flag has an environment variable: the flag name uppercased with dashes as underscores, prefixed
with `AGENTFS_`. A flag beats an environment variable, because the more specific statement of intent
is the later one. `agentfs --help` prints the whole table with defaults.

## The floors

`config.Validate` reports **every** setting the program cannot run under in one call, joined with
`errors.Join`, so an operator correcting a configuration fixes the whole thing rather than
discovering the next mistake on the next start. Each finding is a `config.FieldError` naming both the
Go field and the flag.

A count, byte or duration ceiling must be at least the floor its row carries. Beyond the per-field
floors, two relations between fields have to hold:

- preserved undefined members cannot outsize the document holding them
  (`--max-extra-bytes` ≤ `--max-document-bytes`),
- a backoff cannot start above its own ceiling (`--root-retry-min` ≤ `--root-retry-max`).

`--sweep-interval` has a floor of its own, `config.MinSweepInterval`. Below it a sweep pass is still
running when the next one is due, and agentfs spends more of the machine re-reading directories than
the agents it watches spend writing them.

An invalid setting exits `2` with the field, the flag and the constraint on stderr:

```
agentfs: SweepInterval (--sweep-interval): must be at least 100ms, got 10ms
```

An environment variable that does not parse is ignored rather than fatal. An exported value is
ambient — refusing to start because of an unrelated shell setting would make the tool break for
reasons the operator did not choose.

## What each ceiling protects, and what reaching it looks like

### The walk

| Flag | Protects against | Applied by | What the operator sees |
| --- | --- | --- | --- |
| `--max-depth` | A pathological or generated tree that descends without end. | `index.Index.request` | The subtree is marked truncated and counted in `index.Stats.DepthTruncated`. The status line reports `N subtrees below the depth ceiling`. |
| `--max-entries-per-dir` | A run directory an agent fills without bound. | `index.Index.absorb` | The directory holds a prefix, is marked truncated, and is counted in `index.Stats.TruncatedDirs`. The status line reports `N directories capped`. |
| `--max-nodes` | A workspace larger than the memory the operator has. | `index.Index.absorb` | `index.Stats.NodeCeilingHit` is set and the status line reports `tree at its node ceiling`, ranked severe. |

The depth ceiling stops descent; it does not stop the walk. A directory below it is held as a leaf,
so the tree stays navigable and says so, rather than failing to open. `TestDepthCeilingStopsDescent`
and `TestEntryCeilingMarksTheDirectoryTruncated` hold both.

Anything the index does not hold is not tracked, so it is not swept either. A ceiling on the walk is
therefore a ceiling on what change detection observes — see
[change-detection.md](change-detection.md).

### Observation

| Flag | Protects against | Applied by | What the operator sees |
| --- | --- | --- | --- |
| `--max-watches` | Exhausting the kernel's per-user watch budget, which is shared with every other program on the host. | `watch.notifySource.addTree` | `watch.Stats.WatchesRefused` rises; the status line reports `N directories swept, not watched`. |
| `--max-batch` | One frame spending unbounded time applying a burst. | `watch.builder.add` | `watch.Stats.Dropped` rises, the batch carries `Truncated` and `Resync`, and the status line reports `N changes dropped, resynchronized`, ranked severe. |
| `--max-queue` | A queue between discovery and delivery that grows until the process is killed. | `watch.engine.emit` | The same account as `--max-batch`: `watch.Stats.Dropped` rises, the batch carries `Truncated` and `Resync`, and the status line reports `N changes dropped, resynchronized`, ranked severe. |
| `--sweep-budget` | A sweep pass whose cost is the size of the tracked set rather than a number agentfs chose. | `watch.engine.sweepSlice` | Nothing directly. A tracked set larger than the budget is covered across successive cycles, so detection slows rather than stalling. |
| `--sweep-interval` | A sweep that costs more of the machine than the agents it watches. | `watch.engine.run` | The interval bounds how late a change made by another client of an export can be reported. `agentfs doctor` prints the resulting operations per hour. |
| `--dedup-ttl` | One change reported twice by the two sources of a hybrid observer. | `watch.hybridSource.dedupe` | Within the window of an emitted change, a further change to that path with the same operation is suppressed. |
| `--root-retry-min`, `--root-retry-max` | Spending the machine on a mount that does not come back. | `watch.engine.run` | The backoff between reopen attempts doubles from the floor to the ceiling while the status line reports `workspace root unreadable — retrying`. |

The two change ceilings sit on either side of one channel. `--max-queue` sizes the channel between
the sources and the delivery goroutine; `--max-batch` bounds what one window drains out of it. Their
defaults are related — a batch is half a queue — so a queue that has filled drains in two frames.

Reaching either is the same event to a reader. `watch.engine.emit` never blocks a source: when the
channel is full, the change is discarded and a loss is signalled instead, and the batch that window
produces carries `Truncated` and `Resync` exactly as a batch that overflowed its own ceiling does. A
resync makes the consumer rebuild the tree from the filesystem, so the tree is correct either way and
what is lost is the individual entries.

### Reading a document

| Flag | Protects against | Applied by | What the operator sees |
| --- | --- | --- | --- |
| `--max-document-bytes` | A "state document" that is really a log, decoded as state. | `workspace.Scanner.read` | The agent is `unreadable` and carries `AFS1008`, naming the size and the ceiling. The file is not read. |
| `--max-preview-bytes` | A preview that copies a file rather than windowing it. | `fileview.Load` | The preview holds a window of the file and its badge says so. Following a log reads only the appended range. |
| `--max-extra-bytes` | A document written to an unknown schema turning into unbounded retention. | `agentstate.Decode`, given the ceiling by `workspace.Scanner.decodeOptions` | Preserved undefined members beyond the ceiling are dropped with `AFS1006`. |

A state document is a status declaration. Every ceiling in this group says the same thing in a
different unit: bulk output belongs in an artifact, and a reader that decodes a hundred-megabyte file
as state has spent its whole memory budget on one agent.

### Retention

| Flag | Protects against | Applied by | What the operator sees |
| --- | --- | --- | --- |
| `--max-feed-entries` | An activity feed whose cost grows with how long agentfs has been running. | `pane.NewFeed` | The feed is a ring. Its badge reports `N discarded` once it has evicted anything. |
| `--max-diagnostics` | A workspace that is wrong in one way being wrong in that way thousands of times. | `app.Model.Diagnostics` | The list holds the ceiling. Its last entry is `AFS5009`, counting the findings beyond it rather than listing them. |

`--max-diagnostics` bounds what the terminal command retains. `agentfs validate` and `agentfs scan`
report a document's findings as they read it and retain nothing across documents, so the ceiling has
nothing to shed there.

### Interpretation

| Flag | What it decides | Applied by |
| --- | --- | --- |
| `--stale-after` | How long a document may go unwritten before the agent is reported `stale` with `AFS4002`. A document that declares `heartbeat_seconds` is held to its own declaration instead. | `workspace.Scanner.staleAfter` |
| `--skew-tolerance` | How far ahead of this host's clock a workspace timestamp may sit before `AFS3003` is raised. A small lead is clamped to zero rather than rendered as a negative age, because a shared mount is written by hosts whose clocks are not this one. | `workspace.Scanner.age`, `agentstate.Decode` |
| `--strict` | The settle rule's reading count in the terminal command: with it set, a document that fails well-formedness is reported on the first reading rather than after a stable one. | `app.settleReads` |

`--strict` reaches nothing outside the terminal command. `agentfs validate` and `agentfs scan` read
once and settle nothing, whatever it is set to.

### Presentation

| Flag | What it decides | Applied by |
| --- | --- | --- |
| `--color` | `auto` styles only when stdout is a terminal, `always` styles regardless of where the output goes — which is what a pipe into a pager wants — and `never` emits no escape sequence at all. | `cli.palette` |
| `--ascii` | Restricts the frame to ASCII glyphs, for terminals and fonts that render box drawing and braille as replacement boxes. | `cli.palette`, `theme.ASCIIGlyphs` |
| `--redact-keys` | Names the JSON members whose quoted values the file preview masks, matched without regard to case, underscores or hyphens. The default list carries the spellings a credential is written under by the SDKs agents are built on. | `fileview.Redact`, reached from `app.Model.loadPreview` |

Every distinction agentfs draws in colour is also drawn with a glyph
(`TestPlainDistinguishesStatusAndChangeByGlyph`), so `--color=never` loses decoration and no
information.

Masking reaches the file preview and nothing else. A value the workspace writes anywhere else — into
a filename, into a run directory's name, into a member the agent bar renders — reaches the screen as
written, and so does a value that is a number or an object rather than a quoted string. What that
means for a shared screen is in
[threat-model.md § Secrets on a shared terminal](../threat-model.md#8-secrets-on-a-shared-terminal-stride-information-disclosure).

## Ceilings that are constants rather than settings

Some bounds are not settings, because a value an operator would want to change is a value the design
would rather not have. They are named here so that a reader who hits one can find it.

| Constant | Bounds |
| --- | --- |
| `fileview.MaxLines` | Display lines one file window holds. A window of empty lines is still a window. |
| `fileview.MaxSpans` | Highlight spans one line carries, so a pathological line costs a bounded lex. |
| `fileview.DefaultMaxBytes` | The window ceiling when a caller supplies none. |
| `watch.DefaultOptions().Window` | How long changes accumulate before a batch is closed. |
| `diag.Abbreviate`'s callers | The rune count a diagnostic's offending value is rendered to. |
| `config.MinSweepInterval` | The floor under `--sweep-interval`. |

## Related

- [change-detection.md](change-detection.md) — the ceilings on observation, in context.
- [architecture.md](architecture.md) — why the limits table is a package.
- [../runbook.md](../runbook.md) — how to raise a ceiling, and what to check before doing it.
