# Getting started

Build agentfs, create a workspace by hand, and watch it change. About five minutes.

You need Go 1.26.7 or later. Nothing else.

## 1. Build it

```sh
git clone https://github.com/stxkxs/agentfs.git
cd agentfs
go build -o /tmp/agentfs .
```

Check it runs:

```sh
/tmp/agentfs version
```

## 2. Make a workspace

A workspace is a directory holding one subdirectory per agent. Create two:

```sh
mkdir -p /tmp/ws/agent-researcher/logs /tmp/ws/agent-writer

cat > /tmp/ws/agent-researcher/state.json <<'JSON'
{
  "schema": "agentfs/v1",
  "status": "running",
  "task": "Retrieve and rank sources",
  "step": 3,
  "steps_total": 8,
  "model": "claude-opus-5"
}
JSON

cat > /tmp/ws/agent-writer/state.json <<'JSON'
{
  "schema": "agentfs/v1",
  "status": "idle"
}
JSON
```

## 3. Read it once

```sh
/tmp/agentfs scan /tmp/ws
```

```
agent-researcher         declared   running    Retrieve and rank sources · step 3 · claude-opus-5
agent-writer             declared   idle
```

The second column is *how the state is known*, and it is separate from the status. `declared` means a
document was read and decoded. An agent that wrote nothing reads as `absent`, one whose document does
not decode reads as `invalid`, and one that stopped rewriting reads as `stale` — four different facts
that a reader collapsing them into "unknown" could not tell apart.

## 4. Check it against the contract

```sh
/tmp/agentfs validate /tmp/ws
```

```
2 documents · 0 errors · 0 warnings · contract agentfs/v1
```

Now break one on purpose:

```sh
echo '{"schema":"agentfs/v1","status":"not running"}' > /tmp/ws/agent-writer/state.json
/tmp/agentfs validate /tmp/ws
```

```
AFS3002 error agent-writer/state.json:1:33 at /status: The status "not running" is not in the contract vocabulary. — Use one of: running, idle, blocked, error, done. Matching is exact, not by substring.
2 documents · 1 errors · 0 warnings · contract agentfs/v1
```

The status vocabulary is matched exactly. `"not running"` contains the word `running` and is not a
running agent, so it is a diagnostic rather than a guess. The command exits `1`, which is what makes
it usable as a gate — see [validate in CI](../how-to/validate-in-ci.md).

Put it back:

```sh
echo '{"schema":"agentfs/v1","status":"idle"}' > /tmp/ws/agent-writer/state.json
```

## 5. Watch it

```sh
/tmp/agentfs /tmp/ws
```

The tree is on the left, the selected file in the top right, and the activity feed below it. Move with
`j` and `k`, open a directory with `enter`, move between panes with `tab`, and press `?` for every
binding. `q` quits.

Leave it running and, in another terminal, change a document:

```sh
cat > /tmp/ws/agent-researcher/state.json <<'JSON'
{
  "schema": "agentfs/v1",
  "status": "done",
  "task": "Retrieve and rank sources",
  "step": 8,
  "steps_total": 8,
  "problem": "The first retrieval pass timed out and was retried."
}
JSON
```

The status bar changes to `done`, the file appears in the activity feed, and the row is marked as
recently changed. Note that the agent declares both a terminal status *and* a problem: the two are
independent, so "finished, having recovered from a fault" is something an agent can say.

## 6. See how it is being watched

```sh
/tmp/agentfs doctor /tmp/ws
```

```
workspace     /tmp/ws
filesystem    <the filesystem under the root>
detection     notify
confinement   os.Root — a path that resolves outside the workspace is refused
agents        2
tracked dirs  4 (sweep budget 512 per cycle)
watch budget  8192
contract      agentfs/v1
tree          5 nodes from 4 directory reads
```

On a local filesystem, kernel notification observes every write. On a network export it does not, and
agentfs sweeps as well — see [watch a network mount](../how-to/watch-a-network-mount.md).

## Next

- [Emit state from an agent](../how-to/emit-state-from-an-agent.md) — the integrator's side
- [The state contract](../reference/state-schema.md) — every member
- [Keys](../reference/keys.md) — every binding

Clean up with `rm -rf /tmp/ws /tmp/agentfs`.
