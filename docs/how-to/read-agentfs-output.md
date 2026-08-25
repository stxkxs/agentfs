# Read agentfs output from a program

Consume agentfs from something that is not a person: a script, a pipeline, or another agent.

[report-envelope.md](../reference/report-envelope.md) is the shape of everything here — every member,
every payload, and the two JSON Schema documents to validate against. This page is how to use it.

## Branch on the envelope, not the prose

Every one-shot command with `--format json` emits one object:

```json
{
  "schema": "agentfs/report/v1",
  "kind": "scan",
  "version": "1.0.0",
  "root": "/tmp/ws",
  "exit": 0,
  "data": {},
  "diagnostics": []
}
```

`schema`, `kind`, `version` and `exit` are the stable frame: they carry the same meaning in every
schema version. A consumer that meets a `schema` it does not implement can still report the `kind` and
`exit` it found, rather than failing somewhere inside a payload it half-recognized.

`kind` selects the shape of `data` — `scan`, `validate`, `doctor`, `version`, or `error` for a command
that could not produce its result at all. Select the payload from the kind rather than probing the
payload to work out which command produced it. `diagnostics` is always an array, so a consumer
iterates it without first testing whether the key is there.

## Commands

```sh
agentfs scan     --format json ./workspace   # what the agents declare
agentfs validate --format json ./workspace   # conformance, exit 1 on findings
agentfs doctor   --format json ./workspace   # how the workspace is observed
agentfs version  --format json
agentfs watch    --format ndjson ./workspace # the change stream, until interrupted
```

## Read `presence` before `status`

A scan reports how each agent's state is known — `presence` — separately from what the agent said —
`status`. Only a presence of `declared` carries a reading to act on. A `status` of `unknown` under a
presence of `invalid` means the document did not decode, which is a different fact from an agent that
declared itself idle.

Both encode as words rather than numbers, so adding a value cannot silently change what an existing
number means.

## Diagnostics

Branch on `code` and `pointer`. `message` and `hint` are prose for a person: they are reworded without
a schema version and carry no contract. A code is permanent — retired rather than reused — so a
consumer that suppresses one never has it come to mean something else.
[diagnostics.md](../reference/diagnostics.md) is the registry.

Where a finding arrives depends on the command. `validate` puts every finding in the envelope's
`diagnostics`, which is what makes it usable as a gate. `scan` puts a document's findings under the
agent that raised them and keeps the envelope's `diagnostics` for findings about the workspace itself,
so a consumer that reads only the envelope's array sees the workspace and not the documents.

## Gate a pipeline on the exit code

`validate` separates "the workspace is conformant" from "the workspace could not be read": exit `1` is
a result to act on and exit `3` is a mount or a path to retry. That distinction is what lets it run
unattended without its output being parsed. [exit-codes.md](../reference/exit-codes.md) is the whole
table.

Under `--format json` a command that cannot read the workspace writes an envelope of kind `error`
carrying the `AFS5010` finding, so the failure is read the same way the result would have been.

```sh
agentfs validate --format json ./workspace |
  jq -r '.diagnostics[] | select(.severity == "error") | "\(.path)\t\(.code)\t\(.hint)"'
```

## A closed pipe is not a failure

A reader that closes the pipe has decided it has enough, which is not a fault in agentfs: the failed
write is recognized and the command keeps the verdict it had reached. This still exits `1` on a
workspace with findings and `0` on one without:

```sh
agentfs validate --format json ./workspace | head -c 200
```

A producer learns the pipe is closed on its next write. A one-shot command writes once, so it learns
at once. `agentfs watch` writes when the workspace changes, so on a workspace that is quiet it does not
learn at all — `agentfs watch --format ndjson ./workspace | head -n 1` keeps running after `head`
exits. End a stream by signalling the process, which ends it with `130`, rather than by closing the
reader and waiting.

## Watch a workspace from a program

`agentfs watch --format ndjson` writes one record per line and keeps writing until it is interrupted
or a write fails. Drawing needs a terminal, so a `watch` whose output is a pipe writes the stream
whether or not it asked for it by name — `agentfs watch ./workspace | your-consumer` is the same
thing.

`kind` is `change`, `status` or `error`, and selects the shape of `data`. A `change` record says that a
path changed, not what it now holds: re-read the paths you care about. That is what keeps the
producer's cost independent of what the workspace writes.

### The stream is at-least-once

A consumer that reconnects, or that reads a stream whose producer restarted, sees records it has
already processed. The two identifiers answer different questions:

- **`seq`** is dense from 1 for one producer, so a gap is a loss. It says nothing about a record you
  have already accepted, and a producer that restarts begins again at 1.
- **`dedup_key`** names the event rather than the record. Discard a repeat by key. A consumer that
  treats `seq` as an identity double-counts across a restart.

The reasoning behind that contract is in
[change-detection.md](../explanation/change-detection.md#the-record-stream-and-at-least-once-delivery).

### Act on a status record

`event` names one condition per record, so a batch that both recovers a root and demands a resync
writes one record for each.

- **`resync`** — the changes are not a complete account. Rebuild from the filesystem rather than
  applying what came before.
- **`root_lost`** — the workspace root became unreadable, so nothing is being observed until it
  returns. **`root_recovered`** — it came back, and a `resync` follows it.

The counters on a status record are cumulative for the producer's lifetime. They size a loss the
events announce; they are not how a loss is detected.

## Read a workspace once

`agentfs scan --format json ./workspace` reads the workspace once and exits, for a caller that wants
the state rather than the changes.

## The contract the workspace writes

`agentfs schema` prints the JSON Schema for a state document. It is the same document published at the
`$id` inside it, and the same table the decoder types members against. The two schemas for what
agentfs *emits* are published alongside it, as `schema/report.v1.json` and `schema/stream.v1.json`.
