package agentstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaID is the canonical identifier of the v1 contract schema. It is the
// URL the published schema is served from, so a consumer that resolves the $id
// receives the same document [SchemaJSON] produces.
const SchemaID = "https://stxkxs.github.io/agentfs/schema/agent-state.v1.json"

// SchemaJSON renders the contract as a JSON Schema 2020-12 document.
//
// The schema is built from [Rules], the same table the decoder types members
// against, so a member cannot be accepted by one and refused by the other. A
// conformance test asserts the two agree on every fixture.
func SchemaJSON() ([]byte, error) {
	props := newObject()
	var required []string
	for _, r := range Rules() {
		if r.Required {
			required = append(required, r.Member)
		}
		props.set(r.Member, memberSchema(r))
	}

	root := newObject()
	root.set("$schema", "https://json-schema.org/draft/2020-12/schema")
	root.set("$id", SchemaID)
	root.set("title", "agentfs agent state")
	root.set("description", "The document an agent writes to declare what it is doing. "+
		"agentfs reads one of these per agent workspace directory. "+
		"Members the contract does not define are preserved and ignored.")
	root.set("type", "object")
	root.set("required", required)
	root.set("additionalProperties", true)
	root.set("properties", props)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("render schema: %w", err)
	}
	return buf.Bytes(), nil
}

func memberSchema(r Rule) *object {
	o := newObject()
	o.set("description", r.Summary)

	switch r.Type {
	case "integer|string":
		o.set("oneOf", []any{
			mapOf("type", "integer", "minimum", 0),
			mapOf("type", "string", "minLength", 1),
		})
	case "integer":
		o.set("type", "integer")
		o.set("minimum", 0)
	case "number":
		o.set("type", "number")
		o.set("minimum", 0)
	case "object":
		o.set("type", "object")
		o.set("additionalProperties", mapOf("type", "string"))
	default:
		o.set("type", "string")
		if len(r.Enum) > 0 {
			o.set("enum", r.Enum)
		}
		if r.Format != "" {
			o.set("format", r.Format)
		}
		if r.MaxRunes > 0 {
			o.set("maxLength", r.MaxRunes)
		}
	}
	if len(r.Compat) > 0 {
		o.set("$comment", "compatibility member names: "+strings.Join(r.Compat, ", "))
	}
	return o
}

// mapOf builds a nested schema object from alternating keys and values. A key
// that is not a string names no JSON member, so it is skipped rather than
// panicking a generator run.
func mapOf(kv ...any) *object {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		o.set(key, kv[i+1])
	}
	return o
}

// object is a JSON object that marshals its members in insertion order, so the
// generated schema is byte-stable and `task gen` produces no diff when nothing
// changed.
type object struct {
	keys   []string
	values map[string]any
}

func newObject() *object { return &object{values: map[string]any{}} }

func (o *object) set(k string, v any) {
	if _, seen := o.values[k]; !seen {
		o.keys = append(o.keys, k)
	}
	o.values[k] = v
}

// MarshalJSON implements [encoding/json.Marshaler].
func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	// bytes.Buffer writes never fail, so the returns are not checked here; the
	// errors this function reports come from marshalling its members.
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
