package report

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/stxkxs/agentfs/internal/diag"
)

// The identifiers of the published read-side contracts. Each is the URL its
// document is served from, so a consumer that resolves an $id receives the
// document the generator renders.
const (
	// EnvelopeSchemaID identifies the one-shot result schema.
	EnvelopeSchemaID = "https://stxkxs.github.io/agentfs/schema/report.v1.json"
	// StreamSchemaID identifies the change-stream record schema.
	StreamSchemaID = "https://stxkxs.github.io/agentfs/schema/stream.v1.json"
)

// jsonSchemaDialect is the dialect both documents declare, which is the one
// the state contract declares: an integrator validates everything agentfs
// publishes with one validator.
const jsonSchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// The JSON types a published member holds.
const (
	typeString  = "string"
	typeInteger = "integer"
	typeObject  = "object"
	typeArray   = "array"
)

// diagnosticDef is the name the diagnostic object is defined under, so the
// envelope refers to one definition rather than repeating it inside the
// diagnostics member.
const diagnosticDef = "diagnostic"

// The rune ceilings a diagnostic's text is abbreviated to before it is
// emitted. They are published as maxLength, so a validator holds the output to
// them; [TestPublishedCeilingsHold] builds a diagnostic through diag and fails
// when what it carries exceeds what is published here.
const (
	pointerRunes = 200
	valueRunes   = 120
)

// Member describes one member of a published object.
//
// One table feeds both published forms: the JSON Schema an integrator
// validates against and the reference page an integrator reads. A member
// described here reaches both; a member described nowhere reaches neither, and
// [TestEveryEnvelopeMemberIsPublished] holds the tables against the structs the
// encoder writes so a member cannot be added to one and left out of the other.
type Member struct {
	// Name is the JSON member name, which is the encoder's struct tag.
	Name string
	// Type is the JSON type the member holds.
	Type string
	// Required marks a member every document carries. An optional member is
	// absent rather than null when it carries nothing, which is the encoder's
	// omitempty.
	Required bool
	// Summary is one sentence naming what the member holds.
	Summary string
	// Const is the only value the member holds, empty when it is not fixed.
	Const string
	// Enum is the closed vocabulary a string member is drawn from.
	Enum []string
	// Numbers is the closed set an integer member is drawn from.
	Numbers []int
	// Minimum is the smallest value an integer member outside a closed set
	// holds.
	Minimum int
	// MaxRunes is the length the producer abbreviates the member to, zero for
	// a member it emits whole.
	MaxRunes int
	// Format is the JSON Schema format annotation the member's text conforms
	// to, empty when none applies.
	Format string
	// Def names the definition an array member's elements conform to.
	Def string
}

// EnvelopeMembers returns the members of a one-shot result, in the order
// [Envelope.WriteJSON] emits them.
func EnvelopeMembers() []Member {
	return []Member{
		{
			Name: "schema", Type: typeString, Required: true, Const: EnvelopeSchema,
			Summary: "The result format the document declares.",
		},
		{
			Name: "kind", Type: typeString, Required: true, Enum: Kinds(),
			Summary: "The command whose result this is, which selects the shape of data.",
		},
		{
			Name: "version", Type: typeString, Required: true,
			Summary: "The agentfs build that produced the result.",
		},
		{
			Name: "root", Type: typeString,
			Summary: "The workspace the result describes, absent when the result describes no workspace.",
		},
		{
			Name: "exit", Type: typeInteger, Required: true, Numbers: exitValues(),
			Summary: "The code the process terminates with, carried in the result so a reader of a captured file reaches the verdict the process reached.",
		},
		{
			Name: "data", Type: typeObject,
			Summary: "The payload kind selects, absent for a result that carries none.",
		},
		{
			Name: "diagnostics", Type: typeArray, Required: true, Def: diagnosticDef,
			Summary: "Every finding the command accumulated, an empty array for a run that raised none.",
		},
	}
}

// RecordMembers returns the members of one change-stream line, in the order
// [Stream.Write] emits them.
func RecordMembers() []Member {
	return []Member{
		{
			Name: "schema", Type: typeString, Required: true, Const: StreamSchema,
			Summary: "The record format the line declares.",
		},
		{
			Name: "kind", Type: typeString, Required: true, Enum: StreamKinds(),
			Summary: "What the record reports, which selects the shape of data.",
		},
		{
			Name: "seq", Type: typeInteger, Required: true, Minimum: 1,
			Summary: "The producer's record ordinal, counting from 1, so a gap names a record the consumer did not receive.",
		},
		{
			Name: "time", Type: typeString, Required: true, Format: "date-time",
			Summary: "The instant the record reports, RFC 3339 with its offset.",
		},
		{
			Name: "dedup_key", Type: typeString,
			Summary: "The identity of the event the record reports, absent for a record that reports no repeatable event.",
		},
		{
			Name: "data", Type: typeObject,
			Summary: "The payload kind selects, absent for a record that carries none.",
		},
	}
}

// DiagnosticMembers returns the members of one finding, in the order the
// encoder emits them.
func DiagnosticMembers() []Member {
	return []Member{
		{
			Name: "code", Type: typeString, Required: true, Enum: diagnosticCodes(),
			Summary: "The finding's permanent identifier, which is what a consumer branches on. A code is retired rather than reused.",
		},
		{
			Name: "severity", Type: typeString, Required: true, Enum: severities(),
			Summary: "How serious the finding is. A document carrying an error is not usable as state.",
		},
		{
			Name: "path", Type: typeString,
			Summary: "The workspace-relative path of the document the finding is about, absent for a finding about the workspace itself.",
		},
		{
			Name: "pointer", Type: typeString, Format: "json-pointer", MaxRunes: pointerRunes,
			Summary: "An RFC 6901 pointer to the member the finding is about, absent when the finding is about the document as a whole.",
		},
		{
			Name: "line", Type: typeInteger, Minimum: 1,
			Summary: "The 1-based line of pointer within the document, absent when the position does not resolve.",
		},
		{
			Name: "column", Type: typeInteger, Minimum: 1,
			Summary: "The 1-based column of pointer within the document, absent when the position does not resolve.",
		},
		{
			Name: "message", Type: typeString, Required: true,
			Summary: "Prose naming the condition, for a person to read. It carries no contract and is reworded without a schema version.",
		},
		{
			Name: "hint", Type: typeString,
			Summary: "Prose naming the edit that resolves the finding. Applying it to the document makes the finding go away.",
		},
		{
			Name: "value", Type: typeString, MaxRunes: valueRunes,
			Summary: "The offending value prepared for display: a terminal control sequence is replaced by a visible marker, a rune that reorders or hides the text around it is replaced by a visible stand-in, and the text is abbreviated to the ceiling. It does not reproduce the document's bytes.",
		},
	}
}

// exitValues returns the exit codes an envelope carries, from the table the
// process exits by, so the schema cannot admit a status agentfs never returns.
func exitValues() []int {
	codes := Codes()
	out := make([]int, 0, len(codes))
	for _, c := range codes {
		out = append(out, int(c.Code))
	}
	return out
}

// diagnosticCodes returns the diagnostic vocabulary from the registry the
// findings are raised from, so the schema and the diagnostic reference name
// the same set.
func diagnosticCodes() []string {
	codes := diag.Codes()
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, string(c.Code))
	}
	return out
}

// severities returns the severity vocabulary in ascending seriousness, spelled
// by the same method the encoder marshals a severity through.
//
// The set is enumerated rather than walked: [TestEverySeverityIsPublished]
// fails when diag names an ordinal past the last one here, so a level added
// there cannot ship as a value this schema forbids.
func severities() []string {
	return []string{diag.Info.String(), diag.Warning.String(), diag.Error.String()}
}

// EnvelopeSchemaJSON renders the one-shot result contract as a JSON Schema
// 2020-12 document.
//
// The members come from [EnvelopeMembers] and [DiagnosticMembers], the tables
// the reference page is rendered from, so an integrator validating against
// this document and one reading that page are held to the same contract.
//
// additionalProperties is true because the format is additive: a member added
// to the envelope must not make a build's output fail validation against the
// schema an older build published under the same version.
func EnvelopeSchemaJSON() ([]byte, error) {
	defs := newObject()
	defs.set(diagnosticDef, objectSchema(
		"agentfs diagnostic",
		"One machine-readable finding about one workspace document.",
		DiagnosticMembers()))

	root := objectSchema(
		"agentfs one-shot result",
		"The object every agentfs command emits under --format json. "+
			"kind selects the shape of data; schema, kind, version and exit carry the same "+
			"meaning in every version of this format.",
		EnvelopeMembers())
	root.set("$defs", defs)
	return renderSchema(EnvelopeSchemaID, root)
}

// StreamSchemaJSON renders the change-stream record contract as a JSON Schema
// 2020-12 document.
//
// One record is one line of the NDJSON stream: a validator is pointed at a
// line rather than at the stream, which has no JSON document of its own.
func StreamSchemaJSON() ([]byte, error) {
	root := objectSchema(
		"agentfs change-stream record",
		"One line of the NDJSON stream agentfs watch emits under --format ndjson. "+
			"Each line is a complete JSON object; the stream as a whole is not one. "+
			"kind selects the shape of data.",
		RecordMembers())
	return renderSchema(StreamSchemaID, root)
}

// objectSchema builds the schema of one object from its member table.
func objectSchema(title, description string, members []Member) *object {
	required := make([]string, 0, len(members))
	props := newObject()
	for _, m := range members {
		if m.Required {
			required = append(required, m.Name)
		}
		props.set(m.Name, memberSchema(m))
	}

	o := newObject()
	o.set("title", title)
	o.set("description", description)
	o.set("type", typeObject)
	o.set("required", required)
	o.set("additionalProperties", true)
	o.set("properties", props)
	return o
}

// memberSchema builds the schema of one member.
func memberSchema(m Member) *object {
	o := newObject()
	o.set("description", m.Summary)
	o.set("type", m.Type)

	switch m.Type {
	case typeArray:
		items := newObject()
		items.set("$ref", "#/$defs/"+m.Def)
		o.set("items", items)
	case typeInteger:
		if len(m.Numbers) > 0 {
			o.set("enum", m.Numbers)
			break
		}
		o.set("minimum", m.Minimum)
	case typeString:
		if m.Const != "" {
			o.set("const", m.Const)
		}
		if len(m.Enum) > 0 {
			o.set("enum", m.Enum)
		}
		if m.Format != "" {
			o.set("format", m.Format)
		}
		if m.MaxRunes > 0 {
			o.set("maxLength", m.MaxRunes)
		}
	}
	return o
}

// renderSchema stamps the dialect and the identifier onto an object schema and
// encodes it.
//
// The identifier leads the document, which is where a reader looks for what
// the file is, and the encoding is deterministic: equal tables produce equal
// bytes, so the published file has a checksum that only a contract change
// moves.
func renderSchema(id string, root *object) ([]byte, error) {
	doc := newObject()
	doc.set("$schema", jsonSchemaDialect)
	doc.set("$id", id)
	for _, k := range root.keys {
		doc.set(k, root.values[k])
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("render %s: %w", id, err)
	}
	return buf.Bytes(), nil
}

// object is a JSON object that marshals its members in insertion order, so a
// rendered schema is byte-stable and regenerating an unchanged contract
// produces no diff.
type object struct {
	keys   []string
	values map[string]any
}

func newObject() *object { return &object{values: map[string]any{}} }

// set adds a member, keeping the position a name was first given.
func (o *object) set(k string, v any) {
	if _, seen := o.values[k]; !seen {
		o.keys = append(o.keys, k)
	}
	o.values[k] = v
}

// MarshalJSON implements [encoding/json.Marshaler].
func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	// Writes to a bytes.Buffer do not fail, so the errors reported here are
	// the ones marshalling a member raises.
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, fmt.Errorf("member %q: %w", k, err)
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
