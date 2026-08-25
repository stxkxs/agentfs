# Watch a network mount

Watch a workspace on NFS, EFS, SMB, or an S3-backed FUSE mount, and know what you are and are not
seeing.

## Why this page exists

Kernel change notification reports activity passing through **this** kernel's VFS. A write made by
another client of a network export passes through a different kernel and raises no event here. A
notify-only watcher on such a mount shows a workspace that has not changed since it was opened, and
looks healthy doing it.

agentfs therefore re-reads the directories it is displaying on an interval, in addition to
subscribing to kernel events.

## Check what you have

```sh
agentfs doctor /mnt/efs/workspace
```

```
workspace     /mnt/efs/workspace
filesystem    nfs (network)
detection     hybrid
confinement   os.Root — a path that resolves outside the workspace is refused
agents        12
tracked dirs  4 (sweep budget 512 per cycle)
watch budget  8192
contract      agentfs/v1
sweep cost    up to 921600 filesystem operations per hour from this process;
              N instances against one export cost N times that
```

`filesystem` is what the probe found; `detection` is what that resolved to.

## How the mode is chosen

| Probe result | Mode | Why |
| --- | --- | --- |
| local | `notify` | Every write passes through this kernel, so every write raises an event. |
| network | `hybrid` | Another client's write does not. |
| fuse | `hybrid` | A change made behind the mount does not. |
| unrecognized | `hybrid` | Assuming events arrive is the failure that shows an empty feed while looking healthy. |

Override with `--watch`:

```sh
agentfs --watch=sweep  /mnt/efs/workspace   # ignore kernel events entirely
agentfs --watch=notify /mnt/efs/workspace   # accept that remote writes go unreported
```

## What the sweep costs

The sweep covers the **tracked set**: the directories whose contents are on screen. Its cost follows
the viewport rather than the size of the workspace, so a 400,000-file export costs the same as a small
one.

Per cycle it performs at most `--sweep-budget` directory reads, every `--sweep-interval`. A tracked
set larger than the budget is covered across successive cycles, so a large set slows detection instead
of stalling the process. The per-hour ceiling `doctor` prints is that arithmetic.

Several operators watching one export multiply it. `doctor` reports this process's figure; multiply by
the number of instances to get what the export sees.

## What it does not cover

A change inside a **collapsed** directory is reported when the directory is opened, not when it
happens. This is the deliberate consequence of binding cost to the viewport. Opening a directory
re-reads it, so nothing is missed — only delayed.

## Tuning

```sh
agentfs --sweep-interval=10s --sweep-budget=128 /mnt/efs/workspace
```

A longer interval and a smaller budget lower the load on the export and raise the delay before a
remote write appears. The export's own attribute cache sets a floor agentfs cannot beat: an NFS mount
with `acdirmax=60` will not report a remote change sooner than its cache allows, however often
agentfs asks.

Every setting is in [flags.md](../reference/flags.md); each has an `AGENTFS_` environment variable.

## When the mount goes away

An unmount, a stale handle, or a deleted root is reported distinctly from a per-file failure. The
status line reads `root unreadable — retrying`, the last good frame is held rather than blanked, and
agentfs reopens the root with exponential backoff between `--root-retry-min` and `--root-retry-max`.
On recovery it resynchronizes, because changes during the outage were not observed.

A one-shot command exits `3` instead. See [the runbook](../runbook.md).

## Reading it further

[Change detection](../explanation/change-detection.md) explains what each mechanism observes, what
coalescing does, and what a resynchronization means.
