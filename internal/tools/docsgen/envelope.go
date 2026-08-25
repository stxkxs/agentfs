package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/report"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// The JSON types a payload member holds. A payload is described by the type
// its members hold on the wire rather than by the Go type behind it, because
// the reader of this page has the JSON and not the Go.
const (
	jsonString  = "string"
	jsonInteger = "integer"
	jsonBoolean = "boolean"
	jsonObject  = "object"
	jsonArray   = "array"
)

// The nouns the two kind vocabularies are headed with. Both vocabularies carry
// "error", so a heading that named only the kind would leave two sections with
// one name and a reader following a reference to either.
const (
	nounResult = "result"
	nounRecord = "record"
)

// member is one row of a payload table.
type member struct {
	// name is the JSON member name the encoder writes.
	name string
	// kind is the JSON type the member holds.
	kind string
	// meaning states what the member holds, including what its name does not
	// say: a unit, a clock, or a transformation applied before it was written.
	meaning string
}

// payload describes what one kind's data member carries.
//
// source and typeName name the Go declaration the encoder marshals the payload
// from. TestEveryPayloadMemberMatchesItsType reads that declaration's struct
// tags, so a member added to the type without a row here fails the suite
// rather than reaching an integrator as an undocumented member, and a row
// describing a member the type does not hold fails the same way.
type payload struct {
	kind     string
	noun     string
	source   string
	typeName string
	lead     string
	members  []member
	// carriesNone states why a kind's result has no data member, and is empty
	// for a kind that carries one.
	carriesNone string
}

// The Go declarations the payloads are marshalled from.
const (
	workspaceSource = "internal/workspace/workspace.go"
	commandsSource  = "internal/cli/commands.go"
	streamSource    = "internal/cli/stream.go"
	buildinfoSource = "internal/buildinfo/buildinfo.go"
)

// envelopePayloads returns what each envelope kind's data member carries, in
// the order [report.Kinds] lists the kinds.
func envelopePayloads() []payload {
	return []payload{
		{
			kind: report.KindScan, noun: nounResult,
			source: workspaceSource, typeName: "Result",
			lead: "One reading of the workspace: what each agent declares, and the findings about the " +
				"workspace itself.",
			members: []member{
				{"agents", jsonArray, "One object per detected agent workspace, ordered by name. " +
					"See the agent table below."},
				{"diagnostics", jsonArray, "Findings about the workspace itself rather than about one " +
					"document, absent when there are none. A scan repeats exactly these in the " +
					"envelope's own diagnostics member; a finding about one document is reached " +
					"through that agent's entry instead."},
				{"observed_at", jsonString, "When the scan read the workspace, RFC 3339 with its " +
					"offset. It is the observer's clock, not the agent's, and it is what every age " +
					"in the result is measured against."},
			},
		},
		{
			kind: report.KindValidate, noun: nounResult,
			source: commandsSource, typeName: "validation",
			lead: "The tally of a conformance run. Every finding it counts is in the envelope's " +
				"diagnostics member, including the findings about individual documents that a scan " +
				"leaves under each agent.",
			members: []member{
				{"schema", jsonString, "The contract version the documents were read against."},
				{"documents", jsonInteger, "The number of agent workspaces examined."},
				{"errors", jsonInteger, "How many diagnostics are at error severity. A non-zero count " +
					"is what makes the command exit 1."},
				{"warnings", jsonInteger, "How many diagnostics are at warning severity. Under " +
					"`--strict` a warning is counted as an error instead."},
			},
		},
		{
			kind: report.KindDoctor, noun: nounResult,
			source: commandsSource, typeName: "diagnosis",
			lead: "How this workspace is observed and what observing it costs. The numbers are what " +
				"agentfs measured on this root, not defaults.",
			members: []member{
				{"root", jsonString, "The workspace the check ran against."},
				{"filesystem", jsonString, "What the root was probed as, in the name the platform " +
					"reports."},
				{"filesystem_kind", jsonString, "The class the probed type falls into, which is what " +
					"selects a detection mechanism."},
				{"events_complete", jsonBoolean, "Whether kernel notification observes a write made " +
					"by another client of this filesystem. False means a sweep is required to see " +
					"one."},
				{"mode", jsonString, "The resolved detection mechanism."},
				{"agents", jsonInteger, "How many agent workspaces the root holds."},
				{"nodes", jsonInteger, "How many filesystem nodes the bounded survey retained."},
				{"directories_read", jsonInteger, "How many directory reads the survey spent."},
				{"tracked_dirs", jsonInteger, "How many directories a sweep covers."},
				{"sweep_budget", jsonInteger, "How many of the tracked directories one sweep cycle " +
					"reads. A set larger than the budget is covered across successive cycles."},
				{"sweep_interval", jsonString, "How long one sweep cycle waits, as a Go duration " +
					"string such as `2s`."},
				{"watch_budget", jsonInteger, "The ceiling on kernel watches this process holds."},
				{"confinement", jsonString, "The sandbox the process runs under, as far as it can " +
					"determine."},
				{"schema_version", jsonString, "The state contract this build enforces."},
				{"operations_per_hour", jsonInteger, "The ceiling on filesystem operations this " +
					"process spends sweeping, per hour. Zero under a mode that does not sweep. " +
					"Multiply by the number of instances pointed at one shared export."},
			},
		},
		{
			kind: report.KindVersion, noun: nounResult,
			source: buildinfoSource, typeName: "Info",
			lead: "The identity of the binary that produced the result. The envelope's own version " +
				"member repeats the version from here.",
			members: []member{
				{"version", jsonString, "The released version without its leading `v`, or `dev` for a " +
					"build made outside a release."},
				{"commit", jsonString, "The revision the build was made from, or `unknown`. A build " +
					"from a tree carrying uncommitted changes is suffixed `-dirty`."},
				{"build_date", jsonString, "When the binary was built, RFC 3339 in UTC, or `unknown`."},
				{"go_version", jsonString, "The toolchain that compiled the binary."},
				{"schema", jsonString, "The state contract this build implements, read from the " +
					"contract itself."},
			},
		},
		{
			kind: report.KindError, noun: nounResult,
			carriesNone: "A command that could not produce a result carries no payload. The exit " +
				"member names the reason and the diagnostics describe it: a root that could not be " +
				"read arrives as `AFS5010` with the path in its value. Reading this kind the same " +
				"way as any other is what keeps a consumer from needing a second parser for the one " +
				"path where the command failed.",
		},
	}
}

// recordPayloads returns what each record kind's data member carries, in the
// order [report.StreamKinds] lists the kinds.
func recordPayloads() []payload {
	return []payload{
		{
			kind: report.RecordChange, noun: nounRecord,
			source: streamSource, typeName: "changeRecord",
			lead: "One path changed. The record says that it changed, not what it now holds: the " +
				"consumer re-reads the paths it cares about, which is what keeps the producer's cost " +
				"independent of what the workspace writes.",
			members: []member{
				{"path", jsonString, "The path that changed, workspace-relative and slash-separated. " +
					"A change whose path escapes the root is dropped at the source rather than " +
					"delivered."},
				{"op", jsonString, "What happened to it."},
				{"is_dir", jsonBoolean, "Whether the path is a directory, as far as the source could " +
					"tell. A removal cannot state it, so a removal reports false."},
			},
		},
		{
			kind: report.RecordStatus, noun: nounRecord,
			source: streamSource, typeName: "statusRecord",
			lead: "The stream's own condition: what it is watching, and every way it can be falling " +
				"behind. The counters are cumulative for the producer's lifetime, so a consumer reads " +
				"them to size a loss rather than to detect one — a loss is announced by an event.",
			members: []member{
				{"event", jsonString, "The condition this record reports."},
				{"watching", jsonString, "The workspace root."},
				{"mode", jsonString, "The resolved detection mechanism."},
				{"filesystem", jsonString, "What the root was probed as."},
				{"tracked", jsonInteger, "How many directories the sweep covers, zero under a mode " +
					"that does not sweep."},
				{"watches", jsonInteger, "How many kernel watches the producer holds."},
				{"watches_refused", jsonInteger, "How many directories the budget or the kernel " +
					"refused a watch for. Changes below them are seen by sweep or not at all."},
				{"coalesced", jsonInteger, "How many changes were folded into another change on the " +
					"same path."},
				{"deduplicated", jsonInteger, "How many changes were discarded because another source " +
					"had already reported them."},
				{"dropped", jsonInteger, "How many changes were lost to a batch ceiling. Every drop " +
					"is accompanied by a `resync` event."},
				{"errors", jsonInteger, "How many source faults the producer has observed. A rise " +
					"in it writes one error record carrying the new total, so a single record can " +
					"account for several faults."},
			},
		},
		{
			kind: report.RecordError, noun: nounRecord,
			source: streamSource, typeName: "errorRecord",
			lead: "A fault the producer met and could not resolve, such as a tracked directory it " +
				"cannot read. The stream continues: a fault is reported once, when the count rises " +
				"past what has been reported.",
			members: []member{
				{"message", jsonString, "What the source reported."},
				{"count", jsonInteger, "How many faults this producer has observed, the reported one " +
					"included."},
			},
		},
	}
}

// agentPayload describes one entry of a scan result's agents array. It is not
// a kind of its own, so it carries no kind or noun.
func agentPayload() payload {
	return payload{
		source: workspaceSource, typeName: "Agent",
		lead: "One detected agent workspace. Read `presence` before anything else: it says how the " +
			"declaration below it is known, and only `declared` carries a reading to act on.",
		members: []member{
			{"name", jsonString, "The agent's name: the `agent` member of its state document, or the " +
				"workspace directory name when the document declares none."},
			{"dir", jsonString, "The workspace-relative directory the agent occupies."},
			{"presence", jsonString, "How the state below is known."},
			{"state", jsonObject, "The decoded declaration. Under a presence of `settling` it is the " +
				"last reading that decoded, not the reading being observed."},
			{"diagnostics", jsonArray, "Findings this agent's document raised, absent when it raised " +
				"none. A scan leaves them here rather than in the envelope's diagnostics member."},
			{"document", jsonString, "The workspace-relative path of the state document, absent when " +
				"the directory declares none."},
			{"age", jsonInteger, "How long since the agent declared it wrote the document, as a count " +
				"of nanoseconds. It is measured from `updated_at`, falling back to `started_at`, " +
				"against `observed_at`. Zero when the document declares neither timestamp, and " +
				"negative when a document is stamped further ahead of the observer's clock than the " +
				"skew tolerance absorbs."},
			{"roles", jsonArray, "The conventional subdirectories the workspace carries, absent when " +
				"it carries none."},
		},
	}
}

// renderReportEnvelope writes the contracts an agent reads.
func renderReportEnvelope(b *strings.Builder, _ module) error {
	title(b, "The result envelope and the change stream",
		"Everything agentfs emits to a program. A one-shot command emits one envelope; `agentfs watch` "+
			"emits a stream of records, one per line. This page is rendered from the member tables the "+
			"encoders are held against and from the Go types the payloads are marshalled from, so a "+
			"member described here and a member agentfs writes are the same member.")

	para(b, "Both contracts are published as JSON Schema 2020-12 documents, generated from the same "+
		"tables: "+code("schema/report.v1.json")+" under "+code(report.EnvelopeSchemaID)+" and "+
		code("schema/stream.v1.json")+" under "+code(report.StreamSchemaID)+". The release workflow "+
		"serves each from the path its own `$id` names.")

	para(b, "Both admit members they do not declare. A build that adds a member must not make its "+
		"output fail validation against the schema an earlier build published under the same version, "+
		"so a consumer ignores what it does not recognize rather than refusing the document.")

	if err := renderEnvelopeSection(b); err != nil {
		return err
	}
	if err := renderDiagnosticSection(b); err != nil {
		return err
	}
	if err := renderEnvelopePayloads(b); err != nil {
		return err
	}
	if err := renderRecordSection(b); err != nil {
		return err
	}
	return renderRecordPayloads(b)
}

// renderEnvelopeSection writes the one-shot frame.
func renderEnvelopeSection(b *strings.Builder) error {
	section(b, "The one-shot envelope")
	para(b, "Every command run with `--format json` writes one object followed by a newline. The "+
		"object is built in memory before any byte reaches the output, so a payload that cannot be "+
		"marshalled fails without leaving a truncated object for a consumer to choke on.")

	if err := memberTable(b, report.EnvelopeMembers()); err != nil {
		return err
	}

	para(b, "`schema`, `kind`, `version` and `exit` are the stable frame: they carry the same meaning "+
		"in every version of this format. A consumer that meets a `schema` it does not implement can "+
		"still report the `kind` and the `exit` it found, rather than failing somewhere inside a "+
		"payload it half-recognized.")

	para(b, "`diagnostics` is always present, an empty array included, so a consumer iterates it "+
		"without first testing whether the key is there. `root` and `data` are absent rather than "+
		"null when the result has nothing to put in them. `exit` is the status the process "+
		"terminates with; [exit-codes.md](exit-codes.md) says what each one means.")

	sample, err := sampleEnvelope()
	if err != nil {
		return err
	}
	para(b, "One result, indented for reading — the encoder writes it as a single line:")
	fence(b, "json", sample)
	return nil
}

// renderDiagnosticSection writes the finding object both formats carry.
func renderDiagnosticSection(b *strings.Builder) error {
	section(b, "A diagnostic")
	para(b, "Every finding agentfs reports has this shape, in the envelope's `diagnostics` member and "+
		"under each agent of a scan result. Branch on `code` and `pointer`. [diagnostics.md](diagnostics.md) "+
		"is the registry: what each code means, the severity it is raised at, and which codes no JSON "+
		"Schema validator can produce.")

	if err := memberTable(b, report.DiagnosticMembers()); err != nil {
		return err
	}

	para(b, "`value` is prepared for display rather than reproduced: a terminal control sequence is "+
		"replaced by a visible marker, a rune that reorders or hides the text around it is replaced "+
		"by a visible stand-in, and a value past the ceiling is abbreviated with a trailing ellipsis. "+
		"A value its message also quotes carries any quotation mark of its own as an apostrophe, so "+
		"the quoted run and the value read the same. A consumer that needs the offending bytes reads "+
		"them from the document at `path`, `line` and `column`.")
	para(b, "`message` and `hint` are prose for a person. They are reworded without a schema version, "+
		"so a consumer that matches on their text breaks on a release that carries no contract change.")
	return nil
}

// renderEnvelopePayloads writes a section per envelope kind.
func renderEnvelopePayloads(b *strings.Builder) error {
	section(b, "What `data` holds, by kind")
	para(b, "One section per value of `kind`. A consumer selects the shape from the kind rather than "+
		"by probing the payload.")

	payloads := envelopePayloads()
	if err := coverKinds(report.Kinds(), payloads); err != nil {
		return err
	}
	for _, p := range payloads {
		if err := renderPayload(b, p); err != nil {
			return err
		}
		if p.kind == report.KindScan {
			if err := renderAgent(b); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderAgent writes the entry shape of a scan result's agents array.
func renderAgent(b *strings.Builder) error {
	a := agentPayload()
	subsection(b, "An agent in a `scan` result")
	para(b, a.lead)
	if err := payloadTable(b, a.members); err != nil {
		return err
	}

	presences, err := vocabulary(func(i int) string { return workspace.Presence(i).String() })
	if err != nil {
		return fmt.Errorf("the presence vocabulary: %w", err)
	}
	para(b, "`presence` is one of "+codeList(presences)+". Only `declared` is a reading to act on: a "+
		"`status` of `unknown` under a presence of `invalid` means the document did not decode, which "+
		"is a different fact from an agent that declared itself idle.")
	para(b, "`state` is the document the agent wrote, decoded. Its members are the state contract, "+
		"which [state-schema.md](state-schema.md) describes and "+
		code("schema/agent-state.v1.json")+" publishes.")
	return nil
}

// renderRecordSection writes the change-stream frame.
func renderRecordSection(b *strings.Builder) error {
	section(b, "The change-stream record")
	para(b, "`agentfs watch --format ndjson` writes one record per line and keeps writing until it is "+
		"interrupted or a write fails. Each line is a complete JSON object; the stream as a whole is "+
		"not one, so a validator is pointed at a line.")

	if err := memberTable(b, report.RecordMembers()); err != nil {
		return err
	}

	para(b, "The stream is at-least-once. The two identifiers answer different questions: `seq` orders "+
		"the records one producer emitted and exposes a loss as a gap, and `dedup_key` names the "+
		"underlying event, so a consumer discards a repeat by key. A producer that restarts begins its "+
		"sequence again at 1 and emits the same keys, which is why a consumer that treats `seq` as an "+
		"identity double-counts across a restart.")

	para(b, "`dedup_key` is built from what the record reports, with a NUL between the parts. For a "+
		"change it is the workspace-relative path and the operation. For a status record it is the "+
		"record's instant in RFC 3339 and the event name; for an error record, the instant and the "+
		"message. The NUL arrives as `\\u0000` because the JSON encoder escapes it — a consumer "+
		"compares the decoded string and never has to parse it.")

	sample, err := sampleRecord()
	if err != nil {
		return err
	}
	para(b, "One record, indented for reading — the encoder writes it as a single line:")
	fence(b, "json", sample)
	return nil
}

// renderRecordPayloads writes a section per record kind.
func renderRecordPayloads(b *strings.Builder) error {
	section(b, "What a record's `data` holds, by kind")

	payloads := recordPayloads()
	if err := coverKinds(report.StreamKinds(), payloads); err != nil {
		return err
	}
	for _, p := range payloads {
		if err := renderPayload(b, p); err != nil {
			return err
		}
		switch p.kind {
		case report.RecordChange:
			ops, err := vocabulary(func(i int) string { return watch.Op(i).String() })
			if err != nil {
				return fmt.Errorf("the operation vocabulary: %w", err)
			}
			para(b, "`op` is one of "+codeList(ops)+". Only kernel notification reports `rename`, at "+
				"the path renamed away from; a sweep reports the same rename as a removal at the old "+
				"path and a creation at the new one. A consumer that re-reads the paths a record "+
				"names handles both without telling them apart.")
		case report.RecordStatus:
			para(b, "`event` is one of `opened`, `resync`, `root_lost` or `root_recovered`. Each record "+
				"names one condition, so a batch that both recovers a root and demands a resync writes "+
				"one record for each. A `resync` means the changes delivered before it are not a "+
				"complete account: rebuild from the filesystem rather than applying what came before.")
		}
	}
	return nil
}

// renderPayload writes one kind's section.
func renderPayload(b *strings.Builder, p payload) error {
	subsection(b, "The "+code(p.kind)+" "+p.noun)
	if p.carriesNone != "" {
		para(b, p.carriesNone)
		return nil
	}
	para(b, p.lead)
	return payloadTable(b, p.members)
}

// memberTable writes a published member table.
func memberTable(b *strings.Builder, members []report.Member) error {
	t := newTable("Member", "Type", "Always present", "Meaning")
	for _, m := range members {
		t.row(code(m.Name), memberJSONType(m), yesNo(m.Required), memberMeaning(m))
	}
	return t.write(b)
}

// memberJSONType renders a member's type, naming the element type of an array
// so a cell reading "array" does not leave the reader to guess.
func memberJSONType(m report.Member) string {
	if m.Type == jsonArray {
		return code(m.Type) + " of " + code(m.Def)
	}
	return code(m.Type)
}

// vocabularyPages point a member at the page listing its vocabulary, for a
// vocabulary too long to read inside a table cell. A cell holding every
// diagnostic code is a cell nobody reads, and the registry describes each one
// rather than only naming it.
var vocabularyPages = map[string]string{
	"code": "[diagnostics.md](diagnostics.md)",
}

// memberMeaning renders a member's summary with the constraints the table
// cannot carry as columns of their own.
func memberMeaning(m report.Member) string {
	out := m.Summary
	switch {
	case m.Const != "":
		out += " Always " + code(m.Const) + "."
	case len(m.Enum) > 0 && vocabularyPages[m.Name] != "":
		out += " One of the codes in " + vocabularyPages[m.Name] + "."
	case len(m.Enum) > 0:
		out += " One of " + codeList(m.Enum) + "."
	case len(m.Numbers) > 0:
		values := make([]string, 0, len(m.Numbers))
		for _, n := range m.Numbers {
			values = append(values, fmt.Sprint(n))
		}
		out += " One of " + codeList(values) + "."
	}
	if m.MaxRunes > 0 {
		out += fmt.Sprintf(" Abbreviated to %d runes.", m.MaxRunes)
	}
	return out
}

// payloadTable writes one payload's members.
func payloadTable(b *strings.Builder, members []member) error {
	t := newTable("Member", "Type", "Meaning")
	for _, m := range members {
		t.row(code(m.name), code(m.kind), m.meaning)
	}
	return t.write(b)
}

// coverKinds reports a kind the page describes nowhere, and a section that
// describes a kind the vocabulary does not carry, so a kind cannot be added to
// a vocabulary and reach no page.
func coverKinds(kinds []string, payloads []payload) error {
	described := make([]string, 0, len(payloads))
	for _, p := range payloads {
		described = append(described, p.kind)
	}
	for _, k := range kinds {
		if !slices.Contains(described, k) {
			return fmt.Errorf("the kind %q is in the vocabulary and this page describes it nowhere", k)
		}
	}
	for _, k := range described {
		if !slices.Contains(kinds, k) {
			return fmt.Errorf("this page describes the kind %q, which no vocabulary carries", k)
		}
	}
	return nil
}

// vocabularyCeiling bounds an ordinal walk. A type whose String method spells
// its unnamed answer differently would otherwise be walked to exhaustion.
const vocabularyCeiling = 64

// vocabulary returns the values an ordinal type spells, walking from zero and
// stopping where the type names nothing.
//
// Taking a vocabulary from the same String method the encoder marshals through
// is what keeps a value added to the type from reaching this page as a gap. A
// walk that finds no end is an error rather than a truncated list, because a
// list that is short by one publishes a closed vocabulary that is not closed.
func vocabulary(name func(int) string) ([]string, error) {
	const unnamed = "unknown"
	out := make([]string, 0, vocabularyCeiling)
	for i := range vocabularyCeiling {
		n := name(i)
		if n == unnamed {
			return out, nil
		}
		out = append(out, n)
	}
	return nil, fmt.Errorf("%d ordinals each name a value, so the walk found no end", vocabularyCeiling)
}

// sampleInstant is the clock every sample on this page is stamped with, so
// regenerating the page produces the bytes it already holds.
var sampleInstant = time.Date(2026, time.April, 8, 13, 0, 0, 0, time.UTC)

// sampleDocument is the state document the sample finding is about. The
// finding's position is resolved against these bytes by the locator the
// scanner uses, so the sample's line and column are the ones agentfs would
// report for this document rather than numbers written beside it.
const sampleDocument = `{"schema": "agentfs/v1", "status": "not running"}`

// samplePath is the document the samples on this page are about.
const samplePath = "indexer/state.json"

// sampleFinding returns the diagnostic the decoder raises for [sampleDocument].
//
// A finding written out beside the document it is about states the code, the
// message, the hint and the position as four independent claims, and each one
// drifts the first time the decoder rewords anything. Decoding the sample
// leaves the page nothing of its own to state: the reference shows a finding
// because agentfs raised it.
func sampleFinding() (diag.Diagnostic, error) {
	_, ds := agentstate.Decode(samplePath, []byte(sampleDocument), agentstate.Options{})
	for _, d := range ds {
		if d.Code == diag.CodeStatusUnknown {
			return d, nil
		}
	}
	var none diag.Diagnostic
	return none, fmt.Errorf(
		"the sample document raises %v rather than %s, so the reference would show a "+
			"finding the decoder does not produce", codesOf(ds), diag.CodeStatusUnknown)
}

// codesOf names the codes a decode raised, for an error that reports what came
// back instead.
func codesOf(ds []diag.Diagnostic) []diag.Code {
	out := make([]diag.Code, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

// sampleEnvelope renders one result through the encoder that writes them, so
// the sample cannot show a frame the encoder does not produce.
func sampleEnvelope() (string, error) {
	data, err := samplePayload(envelopePayloads(), report.KindValidate)
	if err != nil {
		return "", err
	}

	finding, err := sampleFinding()
	if err != nil {
		return "", err
	}

	e := report.NewEnvelope(report.KindValidate, "1.4.0", "/srv/agents", report.CodeFindings)
	e.Data = data
	e.Diagnostics = []diag.Diagnostic{finding}

	var buf bytes.Buffer
	if err := e.WriteJSON(&buf); err != nil {
		return "", fmt.Errorf("render the sample envelope: %w", err)
	}
	return indented(buf.Bytes())
}

// sampleRecord renders one record through the encoder that writes them.
func sampleRecord() (string, error) {
	data, err := samplePayload(recordPayloads(), report.RecordChange)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	s := report.NewStream(&buf)
	key := samplePath + "\x00" + watch.OpModify.String()
	if err := s.Write(report.RecordChange, key, sampleInstant, data); err != nil {
		return "", fmt.Errorf("render the sample record: %w", err)
	}
	return indented(buf.Bytes())
}

// sampleValues is one value per member a sample carries. A member with no
// value here fails the generator rather than rendering a sample that is not
// JSON, which is what keeps a member added to a sampled payload from being
// published as an empty one.
var sampleValues = map[string]string{
	"schema":    `"agentfs/v1"`,
	"documents": "4",
	"errors":    "1",
	"warnings":  "2",
	"path":      `"` + samplePath + `"`,
	"op":        `"modify"`,
	"is_dir":    "false",
}

// samplePayload assembles a kind's payload from the same table the kind's
// section is rendered from, so a sample cannot show a member the table does not
// describe, or omit one it does.
func samplePayload(payloads []payload, kind string) (json.RawMessage, error) {
	var members []member
	for _, p := range payloads {
		if p.kind == kind {
			members = p.members
		}
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("the sample is of a %s payload, which this page describes nowhere", kind)
	}

	var b strings.Builder
	b.WriteByte('{')
	for i, m := range members {
		value, ok := sampleValues[m.name]
		if !ok {
			return nil, fmt.Errorf("the %s payload declares %q and the sample carries no value for it",
				kind, m.name)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%s", m.name, value)
	}
	b.WriteByte('}')
	return json.RawMessage(b.String()), nil
}

// indented reformats an encoded document for reading. The bytes are the
// encoder's; only the whitespace between them is this function's.
func indented(doc []byte) (string, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimRight(doc, "\n"), "", "  "); err != nil {
		return "", fmt.Errorf("indent the sample: %w", err)
	}
	return out.String(), nil
}
