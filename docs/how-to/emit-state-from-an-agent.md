# Emit state from an agent

Make an agent visible to agentfs. Write one JSON document, atomically, into the agent's workspace
directory.

## The minimum

```json
{
  "schema": "agentfs/v1",
  "status": "running"
}
```

Write it to `<workspace>/<agent-name>/state.json`. `status` is one of `running`, `idle`, `blocked`,
`error`, `done`, matched exactly — the whole vocabulary and every member is in
[the state contract](../reference/state-schema.md), and `agentfs schema` prints it as JSON Schema.

## Worth adding

```json
{
  "schema": "agentfs/v1",
  "status": "running",
  "agent": "researcher",
  "task": "Retrieve and rank sources",
  "step": 3,
  "steps_total": 8,
  "model": "claude-opus-5",
  "run_id": "eval-2026-04-08-a",
  "heartbeat_seconds": 30,
  "started_at": "2026-04-08T12:00:00Z",
  "updated_at": "2026-04-08T13:00:00Z"
}
```

`heartbeat_seconds` is an undertaking: *rewrite me at least this often*. A document older than it
reads as stale rather than as current, which is how an agent that died without declaring a terminal
status is distinguished from one that is quietly working. Omit it and agentfs applies its own
`--stale-after`, which cannot know your cadence.

`run_id` ties the document to a run directory. A run that declares its own identity is rendered as
declared; one identified only by its directory name is marked inferred, because a name is a guess.

Timestamps are RFC 3339 **with an offset**. `2026-04-08T13:00:00` names no instant to a reader on
another host, and is refused.

## Write it atomically

A reader opens the file at moments you do not choose. A writer that truncates the target and streams
into it hands that reader bytes that are not JSON.

Write to a temporary file **in the same directory**, flush it, then rename over the target. The rename
is atomic; a temporary file on another filesystem would be copied rather than renamed, which
reintroduces the torn read you were avoiding.

```sh
tmp="$(mktemp "$(dirname "$target")/.state.XXXXXX")"
printf '%s' "$document" > "$tmp"
mv -f "$tmp" "$target"
```

agentfs holds the last complete reading for a cycle rather than flickering, and reports the torn read
as `AFS4010` naming this fix. That tolerance is a courtesy, not a substitute: a writer that is always
torn is reported as invalid once it settles.

## Reference writers

Vendor one file, no dependencies:

- [`contrib/python/agentfs_state.py`](../../contrib/python/agentfs_state.py)
- [`contrib/typescript/agentfs-state.ts`](../../contrib/typescript/agentfs-state.ts)

Both mirror the contract, emit `"schema": "agentfs/v1"`, and write atomically.

## Status and problem are different things

`problem` describes a fault. It does not set `status`, and `status` does not imply it:

```json
{"schema": "agentfs/v1", "status": "done",    "problem": "The first pass timed out and was retried."}
{"schema": "agentfs/v1", "status": "running", "problem": "A transient 503; retrying."}
```

A reader that derived one from the other could express neither. A `status` of `error` with no
`problem` leaves a reader nothing to act on, and raises a warning.

## Directories agentfs recognizes

A workspace directory holding `logs/`, `memory/`, `artifacts/`, `tools/` or `runs/` is recognized as
an agent even with no state document — enough to see an agent that only writes logs.

## Check your work

```sh
agentfs validate ./workspace
```

Every diagnostic carries a JSON Pointer, a line and column, and a hint. Applying the hint produces a
document that validates clean; the conformance suite asserts that for every hint agentfs emits.
