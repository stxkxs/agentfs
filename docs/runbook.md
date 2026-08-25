# Runbook

Symptoms, what they mean, and what to do. The index is the diagnostic code: a reader who sees
`AFS5010` on screen or in JSON looks it up here.

Collect evidence first:

```sh
agentfs doctor --format json /path/to/workspace
```

That reports what the filesystem was probed as, which detection mode resolved, the ceilings in force,
and the sweep cost.

---

## The status line

The bottom line of the terminal interface answers *am I seeing everything*. When healthy it reads:

```
apfs · notify ✓ complete view
```

When not, it names every condition limiting the view, most serious first, with a count of any it could
not fit. A condition is never dropped silently; if the terminal is too narrow the line ends `+2 more`.

---

## The workspace root became unreadable

**Status line** `workspace root unreadable — retrying` · **Code** `AFS5010` · **Exit** `3` from a one-shot command

An unmount, a stale NFS handle, or a deleted root. Every subsequent read fails until the root is
reopened, which is why it is reported distinctly from a per-file failure.

agentfs holds the last good frame rather than blanking, and reopens with exponential backoff between
`--root-retry-min` and `--root-retry-max`. On success it reports `AFS5011` and resynchronizes, because
changes during the outage were not observed.

Check the mount:

```sh
mount | grep /mnt/efs
ls -d /mnt/efs/workspace
```

Nothing to do inside agentfs. It recovers when the path does.

---

## Changes on a network mount are not appearing

**Symptom** The feed is empty while another host is writing.

Confirm the detection mode:

```sh
agentfs doctor /mnt/efs/workspace
```

If `detection` reads `notify` on a network filesystem, kernel events are the only mechanism running
and a remote write raises none. Force the sweep:

```sh
agentfs --watch=hybrid /mnt/efs/workspace
```

If it already reads `hybrid`, the delay is the sweep interval plus the export's own attribute cache.
An NFS mount at `acdirmax=60` will not report a remote change sooner than its cache allows, however
often agentfs asks. Lower `--sweep-interval` only after checking the mount options.

A change inside a **collapsed** directory is reported when the directory is opened. That is deliberate:
the sweep covers what is on screen, which is what keeps its cost independent of workspace size.

---

## Changes are being dropped

**Status line** `N changes dropped, resynchronized` · **Code** `AFS5016`

A single batch exceeded `--max-batch`, so it is not a complete account of what happened. agentfs
requests a resynchronization rather than applying a partial batch, and the tree is rebuilt from the
filesystem — the view stays correct, and the feed is missing the individual entries.

A workspace producing this steadily is writing faster than the batch ceiling. Raise it:

```sh
agentfs --max-batch=16384 /path/to/workspace
```

---

## Part of the tree is swept rather than watched

**Status line** `N directories swept, not watched` · **Code** `AFS5015`

The kernel refused a watch. On Linux that is usually the per-user inotify limit:

```sh
cat /proc/sys/fs/inotify/max_user_watches
```

Those directories are swept instead, so changes still surface — within one sweep interval rather than
immediately. Either raise the kernel limit, or accept the slower path.

`--max-watches` bounds what agentfs asks for, so a workspace cannot exhaust the limit for everything
else on the host.

---

## A directory is capped

**Status line** `N directories capped` · **Code** `AFS5013`

A directory holds more entries than `--max-entries-per-dir`. The pane marks the directory as capped;
what is shown is a prefix, not the whole directory.

Raise the ceiling, or narrow the workspace:

```sh
agentfs --max-entries-per-dir=20000 /path/to/workspace
```

An agent filling a run directory without bound is worth fixing at the source: a state document is a
status declaration, and bulk output belongs in `artifacts/`.

---

## A subtree is below the depth ceiling

**Status line** `N subtrees below the depth ceiling` · **Code** `AFS5014`

Descent stopped at `--max-depth`. Raise it, or point agentfs deeper into the tree.

---

## The tree is at its node ceiling

**Status line** `tree at its node ceiling` · **Code** `AFS5012`

`--max-nodes` was reached. What is shown is a prefix of the workspace. Raise the ceiling if the host
has the memory; the node table is proportional to it.

---

## An agent shows as stale

**Code** `AFS4002`

The document has not been rewritten within the heartbeat it declared, so what it says may not still be
true. The status it declared is still shown, marked stale.

Either the agent stopped without declaring a terminal status, or it declares a `heartbeat_seconds`
shorter than its real cadence. Check the agent, then check the document:

```sh
agentfs scan /path/to/workspace
```

An agent that declares no heartbeat is judged against `--stale-after`, which cannot know its cadence.
Declaring one is the fix.

---

## An agent shows as settling

**Code** `AFS4010`

The document changed between two readings and did not decode. agentfs withholds the error and holds
the last complete reading, because a document being rewritten in place is observed torn and reporting
that as invalid would make the bar flicker on every cycle of a healthy agent.

If it settles, the reading returns. If it persists, the document is genuinely malformed and is
reported as invalid on the next stable reading.

The writer's fix is atomic writes — see
[emit state from an agent](how-to/emit-state-from-an-agent.md).

---

## An agent shows as invalid

**Codes** `AFS1001`, `AFS1002`, `AFS2xxx`, `AFS3xxx`

The document was read and does not carry a usable state. Every diagnostic names a JSON Pointer, a line
and column, and a hint:

```sh
agentfs validate /path/to/workspace
```

Applying the hint produces a document that validates clean. Every code is in
[diagnostics.md](reference/diagnostics.md).

---

## An agent shows as unreadable

**Code** `AFS4001`

The document exists and could not be read: permissions, an I/O error, or removal between being listed
and being read. Check permissions on the workspace directory.

A document larger than `--max-document-bytes` is reported rather than read (`AFS1008`). A state
document is a status declaration; bulk output belongs elsewhere.

---

## Timestamps are reported as being from the future

**Code** `AFS3003`

A timestamp lies beyond this host's clock by more than `--skew-tolerance`. On a shared mount the
writer is a different host, and its clock is not this one.

Check clock synchronization on both hosts. If the fleet's skew is genuinely wider than the tolerance,
raise it:

```sh
agentfs --skew-tolerance=30s /path/to/workspace
```

---

## The terminal says it is too small

The interface needs 60 columns by 16 rows. Below that agentfs renders one line naming the size it
needs, rather than clamping panes into fragments.

Use a one-shot command instead:

```sh
agentfs scan /path/to/workspace
```

---

## Glyphs render as boxes

The default marks are Unicode. For a terminal or font that cannot render them:

```sh
agentfs --ascii /path/to/workspace
```

Every distinction agentfs draws in colour is also drawn with a glyph, so `--color=never` loses no
meaning either.

---

## Raising a limit

Every ceiling has a flag and an `AGENTFS_` environment variable, listed in
[flags.md](reference/flags.md):

```sh
agentfs --max-nodes=500000 /path/to/workspace
AGENTFS_MAX_NODES=500000 agentfs /path/to/workspace
```

An invalid value is refused at startup, naming every field that failed rather than the first.

---

## Collecting evidence

```sh
agentfs doctor --format json /path/to/workspace > doctor.json
agentfs validate --format json /path/to/workspace > validate.json
agentfs version --format json > version.json
```

Those three describe how the workspace is observed, what it declares, and what binary read it.
