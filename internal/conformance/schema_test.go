package conformance_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// The JSON types the checker distinguishes. JSON has one number type; the
// schema separates integers from numbers, so the checker does too.
const (
	kindString  = "string"
	kindInteger = "integer"
	kindNumber  = "number"
	kindObject  = "object"
	kindArray   = "array"
	kindBoolean = "boolean"
	kindNull    = "null"
	kindInvalid = "invalid"
)

// schemaDoc is the part of the published schema this suite applies.
type schemaDoc struct {
	Required   []string                `json:"required"`
	Properties map[string]memberSchema `json:"properties"`
}

// memberSchema is one member's declared constraints.
//
// The checker applies the constraints that decide whether a document is
// structurally the contract's: the declared type, a closed enum, the date-time
// format, and the type of a label value. The ceilings the schema declares are
// display limits the decoder reports and reads past, so a document that
// exceeds one is still structurally the contract's.
type memberSchema struct {
	Type                 string         `json:"type"`
	Enum                 []string       `json:"enum"`
	Format               string         `json:"format"`
	OneOf                []memberSchema `json:"oneOf"`
	AdditionalProperties *memberSchema  `json:"additionalProperties"`
}

// loadSchema renders and parses the published schema. Rendering it here rather
// than reading a checked-in copy is what makes the agreement test an assertion
// about the build rather than about a file someone remembered to regenerate.
func loadSchema(tb testing.TB) schemaDoc {
	tb.Helper()
	raw, err := agentstate.SchemaJSON()
	if err != nil {
		tb.Fatalf("render schema: %v", err)
	}
	var doc schemaDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		tb.Fatalf("parse schema: %v", err)
	}
	if len(doc.Required) == 0 || len(doc.Properties) == 0 {
		tb.Fatal("the schema declares no required members or no properties")
	}
	return doc
}

// checkAgainstSchema applies schema to one document and returns one sentence
// per violation, sorted by member so a failure reads the same on every run.
//
// A member the schema does not declare is allowed, because the schema declares
// additionalProperties and the decoder preserves what it does not define.
func checkAgainstSchema(schema schemaDoc, doc map[string]json.RawMessage) []string {
	var out []string
	for _, name := range schema.Required {
		if _, ok := doc[name]; !ok {
			out = append(out, fmt.Sprintf("required member %q is absent", name))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(doc)) {
		if rule, declared := schema.Properties[name]; declared {
			out = append(out, checkMember(name, &rule, doc[name])...)
		}
	}
	return out
}

// elideNulls drops the members a document declares as JSON null, which is the
// document as [agentstate.Decode] sees it: the decoder reads a null member as
// an absent one. The published schema draws that line elsewhere — it declares
// each member a type that admits no null — so the two disagree about a null
// member, and [TestNullIsTheOnlyDivergence] bounds the disagreement to exactly
// that.
func elideNulls(doc map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(doc))
	for name, value := range doc {
		if jsonKind(value) != kindNull {
			out[name] = value
		}
	}
	return out
}

// nulledMembers returns the members a document declares as JSON null, sorted.
func nulledMembers(doc map[string]json.RawMessage) []string {
	var out []string
	for name, value := range doc {
		if jsonKind(value) == kindNull {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// checkMember applies one member's constraints to the value it holds.
func checkMember(name string, rule *memberSchema, value json.RawMessage) []string {
	kind := jsonKind(value)

	if len(rule.OneOf) > 0 {
		if slices.ContainsFunc(rule.OneOf, func(b memberSchema) bool { return typeMatches(b.Type, kind) }) {
			return nil
		}
		return []string{fmt.Sprintf("member %q holds %s, which matches no branch of its union", name, kind)}
	}
	if !typeMatches(rule.Type, kind) {
		return []string{fmt.Sprintf("member %q is declared %s and holds %s", name, rule.Type, kind)}
	}

	var out []string
	if len(rule.Enum) > 0 && !slices.Contains(rule.Enum, unquote(value)) {
		out = append(out, fmt.Sprintf("member %q holds %s, which is outside the enum %v", name, value, rule.Enum))
	}
	if rule.Format == "date-time" {
		if _, err := time.Parse(time.RFC3339, unquote(value)); err != nil {
			out = append(out, fmt.Sprintf("member %q holds %s, which is not an RFC 3339 date-time", name, value))
		}
	}
	if rule.AdditionalProperties != nil && kind == kindObject {
		out = append(out, checkMapValues(name, rule.AdditionalProperties, value)...)
	}
	return out
}

// checkMapValues applies the value constraint of an object member to every
// value it holds.
func checkMapValues(name string, rule *memberSchema, value json.RawMessage) []string {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(value, &members); err != nil {
		return []string{fmt.Sprintf("member %q does not parse as an object: %v", name, err)}
	}
	var out []string
	for _, key := range slices.Sorted(maps.Keys(members)) {
		if kind := jsonKind(members[key]); !typeMatches(rule.Type, kind) {
			out = append(out, fmt.Sprintf("member %q holds %q as %s, and its values are declared %s",
				name, key, kind, rule.Type))
		}
	}
	return out
}

// typeMatches reports whether a value of the given kind satisfies a declared
// JSON Schema type. An integer satisfies number; the reverse does not hold.
func typeMatches(declared, kind string) bool {
	switch declared {
	case "":
		return true
	case kindNumber:
		return kind == kindInteger || kind == kindNumber
	default:
		return declared == kind
	}
}

// jsonKind names the JSON type of one value, separating an integer from a
// number with a fractional part.
func jsonKind(value json.RawMessage) string {
	t := bytes.TrimSpace(value)
	if len(t) == 0 {
		return kindInvalid
	}
	switch t[0] {
	case '"':
		return kindString
	case '{':
		return kindObject
	case '[':
		return kindArray
	case 't', 'f':
		return kindBoolean
	case 'n':
		return kindNull
	}
	var i int64
	if err := json.Unmarshal(t, &i); err == nil {
		return kindInteger
	}
	var f float64
	if err := json.Unmarshal(t, &f); err == nil {
		return kindNumber
	}
	return kindInvalid
}

// unquote returns the string a JSON value holds, empty when it holds anything
// else.
func unquote(value json.RawMessage) string {
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return ""
	}
	return s
}
