# The published contracts

Three JSON Schema (2020-12) documents, one per contract a program on either side of agentfs has to
agree with.

| Document | The contract | Described by |
| --- | --- | --- |
| `agent-state.v1.json` | The document an agent writes to declare what it is doing — one `state.json` per agent workspace directory. | [state-schema.md](../docs/reference/state-schema.md) |
| `report.v1.json` | The object a one-shot command emits under `--format json`. | [report-envelope.md](../docs/reference/report-envelope.md) |
| `stream.v1.json` | One line of the NDJSON stream `agentfs watch --format ndjson` emits. | [report-envelope.md](../docs/reference/report-envelope.md) |

Each is generated from the Go table the running program reads: the state contract from the rule table
the decoder types members against, the two read-side contracts from the member tables the encoders are
held against. A published schema and the code on the other side of it cannot disagree.

```
schema/
├── agent-state.v1.json      what an agent writes
├── report.v1.json           what a command emits
├── stream.v1.json           what a watch emits, one line at a time
└── .frozen/                 a SHA-256 per document
```

---

## Identity

Each document declares the address it is served from:

```
"$id": "https://stxkxs.github.io/agentfs/schema/agent-state.v1.json"
"$id": "https://stxkxs.github.io/agentfs/schema/report.v1.json"
"$id": "https://stxkxs.github.io/agentfs/schema/stream.v1.json"
```

That URI is a contract's identity: the base a `$ref` in another schema resolves against, and the
string that separates v1 from any other version. It is what a document is validated *against*, not
where the bytes come from. The Pages workflow derives each served path by stripping the site base from
the `$id`, so an identifier and the path it resolves to cannot drift apart.

## Get the schemas

Read the files in this directory. The binary prints the state contract as well:

```sh
agentfs schema > agent-state.v1.json
```

`agentfs schema` renders the contract that build implements; take it when the installed binary and
this checkout are not the same version. `agentfs version` names the build.

## Validate a document

Any JSON Schema 2020-12 validator applies. With
[check-jsonschema](https://github.com/python-jsonschema/check-jsonschema):

```sh
uvx check-jsonschema --schemafile schema/agent-state.v1.json /srv/agents/indexer/state.json

agentfs scan --format json /srv/agents > result.json
uvx check-jsonschema --schemafile schema/report.v1.json result.json
```

A validator exits 0 when the document validates and 1 when it does not, naming the JSON path of each
violation. `stream.v1.json` describes one record, and the stream as a whole is not a JSON document, so
a validator is pointed at a line rather than at the stream.

agentfs applies the state contract across a whole workspace, and reports what a schema cannot state:

```sh
agentfs validate /srv/agents/indexer
```

Every finding carries a stable code, a JSON Pointer to the member it is about, and a hint naming the
edit that resolves it.

## What the state schema decides, and what it does not

The schema decides structure: which members a v1 document must declare, the type of each, the closed
`status` vocabulary, RFC 3339 formatting for the timestamps, and that a label value is a string.
`additionalProperties` is `true` — a member the contract does not define is preserved rather than
rejected, so an integrator's own fields survive a round trip.

What a document can be wrong about that a schema does not express:

- **Staleness.** A document not rewritten within the `heartbeat_seconds` it declares is a finding
  (`AFS4002`) about the file's age, which is not in the file.
- **The compatibility profile.** A document declaring no `schema` member fails this schema, which
  requires one. agentfs reads it under the compatibility profile instead — the alias vocabulary
  (`working` resolves to `running`) and the compatibility member names (`error` is read as `problem`)
  — each with a diagnostic naming the canonical form (`AFS1004`, `AFS1003`).
- **Ceilings.** A validator rejects a document whose member exceeds a `maxLength`. agentfs reports it
  (`AFS2003`) and reads the member abbreviated, because a document that says too much still says what
  the agent is doing.
- **Atomicity.** Write the document to a temporary file in the same directory and rename it over the
  target. A reader opens the file at moments the writer does not choose, and a document observed
  part-written is not well-formed JSON (`AFS1001`).

`internal/conformance/testdata/cases/` holds a document per case with the
diagnostics a reader must report for it.

## What the read-side schemas decide, and what they do not

They decide the frame: the members of an envelope and of a record, which of them are always written,
the closed `kind` vocabularies, the exit codes an envelope carries, and the whole shape of a
diagnostic.

`data` is typed as an object and left there. Its shape is selected by `kind`, and a schema that
enumerated the payloads would have to be reissued for a change inside one of them —
[report-envelope.md](../docs/reference/report-envelope.md) describes every payload, member by member,
rendered from the same tables.

Two properties a schema cannot state, both of which a consumer needs:

- **Delivery.** The record stream is at-least-once. `seq` exposes a loss as a gap and `dedup_key`
  identifies the event, so a repeat is discarded by key rather than by ordinal.
- **Preparation.** A diagnostic's `value` is prepared for display: a terminal control sequence is
  removed, a rune that reorders or hides the text around it is replaced by a visible stand-in, and the
  text is abbreviated. It validates as a string of the published length and it is not the document's
  bytes.

## Versioning

**A published contract is additive-only.** A document that validates against v1 validates against
every v1 published after it. That admits:

- a member added as optional
- a value added to a closed vocabulary
- a ceiling raised
- a description or a `$comment` reworded

It does not admit any of these, each of which is a v2:

- removing or renaming a member
- making an optional member required
- narrowing a member's type
- removing a value from a closed vocabulary
- lowering a ceiling
- changing what a member means while keeping its name

A breaking change is published as v2 under its own `$id`, alongside v1 rather than in place of it,
because a document written to v1 keeps validating against the schema it names. Every document names
its own version: `agentfs/v1` in a state document's `schema` member, `agentfs/report/v1` in an
envelope's, `agentfs/stream/v1` in a record's. A build that does not implement the version a state
document declares refuses the document and names the version it does implement (`AFS1005`), rather
than guessing which members it still understands. A consumer that meets an envelope or a record whose
`schema` it does not implement has the same choice, and the frame carries the same meaning in every
version, so it can report the `kind` and the `exit` it found rather than nothing at all.

The three contracts version independently. A stream reader is long-lived and a one-shot reader is not,
and neither is the agent writing state, so a change to one does not reissue the others.

## Regenerate, and the freeze

Every schema and every checksum here is generated:

```sh
go run ./internal/tools/schemagen    # or: task gen
```

`task verify` regenerates and fails when the tree is dirty afterwards, so a contract change that was
not regenerated does not reach a reviewer as a schema that disagrees with the code.

The checksums are what make these frozen artifacts. `.gitattributes` marks `schema/*.json` as
generated, which collapses them in a review; `.frozen/` is outside that glob, so a change to a
published contract arrives as a changed digest line a reviewer reads. The format is the one `shasum(1)`
and `sha256sum(1)` read, and the paths resolve from the module root:

```sh
shasum -a 256 -c schema/.frozen/*.sha256
```

Two suites hold the contracts to the code. `internal/tools/schemagen` holds each committed document to
what its generator renders, each checksum to the document beside it, and this directory to the table
that generates it — a document nothing generates fails rather than being served. `internal/report`
holds every published member against the struct the encoder writes, in both directions, and every
published ceiling against the value a diagnostic actually carries.
