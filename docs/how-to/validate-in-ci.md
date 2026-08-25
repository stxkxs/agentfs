# Validate a workspace in CI

Use `agentfs validate` as a gate: it exits `1` when any state document raises an error, so it drops
into a pipeline without its output being parsed.

## The gate

```sh
agentfs validate ./workspace
```

```
AFS3002 error agent-writer/state.json:1:33 at /status: The status "not running" is not in the contract vocabulary. — Use one of: running, idle, blocked, error, done. Matching is exact, not by substring.
2 documents · 1 errors · 0 warnings · contract agentfs/v1
```

It checks every state document in the workspace, including those recorded under `runs/`.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Every document is conformant. Warnings do not fail the gate. |
| `1` | At least one document raised an error. |
| `2` | The invocation was malformed. |
| `3` | The workspace could not be read. |

`1` and `3` are deliberately different: a findings status is a result to act on, and a path status is
a problem with the invocation. A gate that conflated them would report a broken mount as a broken
workspace. The full table is in [exit-codes.md](../reference/exit-codes.md).

## In a workflow

```yaml
- name: Validate the agent workspace
  run: |
    go install github.com/stxkxs/agentfs@latest
    agentfs validate ./workspace
```

A non-zero status fails the step. Nothing else is needed.

## Machine-readable findings

```sh
agentfs validate --format json ./workspace
```

```json
{
  "schema": "agentfs/report/v1",
  "kind": "validate",
  "version": "dev",
  "root": "/tmp/ws",
  "exit": 1,
  "data": {
    "schema": "agentfs/v1",
    "documents": 2,
    "errors": 1,
    "warnings": 0
  },
  "diagnostics": [ ... ]
}
```

`exit` is repeated inside the envelope, so a consumer reading a captured file reaches the same verdict
as one that watched the process.

## Filtering by code

Branch on `code` and `pointer`. Never on `message`: the prose is for a person and carries no contract,
while a code is permanent — retired rather than reused.

Fail only on a specific finding:

```sh
agentfs validate --format json ./workspace \
  | jq -e '[.diagnostics[] | select(.code == "AFS3002")] | length == 0'
```

Treat a warning as fatal:

```sh
agentfs validate --format json ./workspace \
  | jq -e '[.diagnostics[] | select(.severity != "info")] | length == 0'
```

Report what was found, without failing:

```sh
agentfs validate --format json ./workspace \
  | jq -r '.diagnostics[] | "\(.severity)\t\(.code)\t\(.path)\t\(.hint)"'
```

Every code is in [diagnostics.md](../reference/diagnostics.md).

## Validating against the schema instead

`agentfs schema` prints the contract as JSON Schema 2020-12, for a pipeline that already has a
validator:

```sh
agentfs schema > agent-state.schema.json
check-jsonschema --schemafile agent-state.schema.json workspace/*/state.json
```

A schema validator checks the document's shape. It cannot raise the codes marked *semantic* in the
diagnostic reference — staleness, clock skew, a torn read — because those come from what agentfs
observed rather than from what the document contains. Use `agentfs validate` for those.

## Strictness

`--strict` reports a well-formedness error on the first reading rather than waiting for a second
stable one. In CI nothing is being rewritten underneath you, so the settle rule only delays the
answer:

```sh
agentfs validate --strict ./workspace
```
