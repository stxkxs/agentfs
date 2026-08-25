# Writing agentfs state from your agent

agentfs reads one JSON document per agent workspace directory. The document
declares what the agent is doing; agentfs never infers it from log files or
process tables. Writing it is the whole integration.

    /srv/agents/indexer/state.json

This directory holds a reference writer per language. Each is a single file
with no dependencies beyond its own standard library, meant to be copied into
your agent rather than installed.

| Language   | File                             | Entry point                        |
| ---------- | -------------------------------- | ---------------------------------- |
| Python     | `python/agentfs_state.py`        | `State(...).write(directory)`      |
| TypeScript | `typescript/agentfs-state.ts`    | `writeState(directory, state)`     |

## Vendor the writer

Copy the file into your source tree. Neither imports anything you have to
install: the Python module needs only the standard library, and the TypeScript
module only Node's. Typechecking the TypeScript module needs `@types/node`,
which is a type dependency and not a runtime one.

Run either file directly to write one document and print its path:

    python3 python/agentfs_state.py /srv/agents/indexer
    node typescript/agentfs-state.ts /srv/agents/indexer

## Declare the state

    from agentfs_state import State, Status

    State(
        status=Status.RUNNING,
        agent="indexer",
        task="Index the workspace tree",
        step=3,
        steps_total=8,
        problem="The first two fetches timed out and were retried.",
        heartbeat_seconds=15,
        started_at=started,
        updated_at=datetime.now(timezone.utc),
    ).write("/srv/agents/indexer")

    import { Status, writeState } from "./agentfs-state.ts";

    writeState("/srv/agents/indexer", {
      status: Status.Running,
      agent: "indexer",
      task: "Index the workspace tree",
      step: 3,
      stepsTotal: 8,
      problem: "The first two fetches timed out and were retried.",
      heartbeatSeconds: 15,
      startedAt: started,
      updatedAt: new Date(),
    });

Rewrite the document whenever the state changes, and at least as often as
`heartbeat_seconds` claims: a document older than its own heartbeat reads as
stale.

Four rules decide whether a reader can use what you wrote, and both writers
enforce them rather than leaving them to you:

- **The status vocabulary is closed.** `running`, `idle`, `blocked`, `error`,
  `done`. Matching is exact, so `not running` resolves to nothing.
- **Status is independent of problem.** Work that finished after a recovered
  fault declares `done` and a problem. A reader that derived one from the other
  could not represent it.
- **Timestamps carry an offset.** `2026-04-08T12:59:47Z`, not
  `2026-04-08T12:59:47`. A local date-time names no instant to a reader on
  another host.
- **The write is atomic.** Write the whole document to a temporary file in the
  same directory, flush it, `fsync` it, then rename it over the target. A
  reader opens the file at moments you do not choose; a writer that truncates
  the target and streams into it hands that reader a document that is not JSON.

## Verify what you wrote

    agentfs validate /srv/agents/indexer

Every finding carries a stable code, a JSON Pointer to the member it is about,
and a hint naming the edit that resolves it. Apply the hint and the finding
goes away.

    agentfs schema

prints the JSON Schema the contract publishes, for a validator in your own
pipeline.

## Write your own

A writer in another language is a JSON encoder and a rename. Take the member
names, the types, and the required members from `agentfs schema`.

Both writers here are run by that suite: it executes each one, decodes what it
wrote, and asserts the document raises no diagnostic, satisfies the published
schema, and carries the same version, ceilings and vocabulary the decoder
enforces. A writer in another language earns the same treatment by being added
to it.

`internal/conformance/testdata/cases/` is the
evaluation suite for the contract. Each case holds a document, the diagnostics
a reader must report for it, and — where a finding carries a hint — the
document that hint produces, under `fixed/`. Every `fixed/state.json` is a
document a conforming writer produces; the `state.json` beside it is the
document whose findings it avoids.
