package agentstate_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// schemaMember is the part of one member's schema a validator applies.
type schemaMember struct {
	Type                 string          `json:"type"`
	Enum                 []string        `json:"enum"`
	Format               string          `json:"format"`
	MaxLength            int             `json:"maxLength"`
	Minimum              *int            `json:"minimum"`
	MinLength            *int            `json:"minLength"`
	OneOf                []schemaMember  `json:"oneOf"`
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
	Comment              string          `json:"$comment"`
}

// schemaDocument is the rendered contract schema.
type schemaDocument struct {
	ID                   string                  `json:"$id"`
	Dialect              string                  `json:"$schema"`
	Type                 string                  `json:"type"`
	Required             []string                `json:"required"`
	AdditionalProperties bool                    `json:"additionalProperties"`
	Properties           map[string]schemaMember `json:"properties"`
}

// renderSchema renders the contract and parses it, failing when either step
// does not succeed.
func renderSchema(tb testing.TB) schemaDocument {
	tb.Helper()
	raw, err := agentstate.SchemaJSON()
	if err != nil {
		tb.Fatalf("render schema: %v", err)
	}
	if !json.Valid(raw) {
		tb.Fatalf("the rendered schema is not well-formed JSON:\n%s", raw)
	}
	var doc schemaDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		tb.Fatalf("parse schema: %v", err)
	}
	return doc
}

// An integrator validating against the published schema and one running
// agentfs must reach the same verdict, which they cannot when the schema omits
// a member the decoder types or requires a member the decoder does not.
func TestSchemaDeclaresEveryContractMember(t *testing.T) {
	t.Parallel()
	doc := renderSchema(t)
	rules := agentstate.Rules()

	var wantRequired []string
	for _, r := range rules {
		if _, declared := doc.Properties[r.Member]; !declared {
			t.Errorf("the schema declares no property for the member %q", r.Member)
		}
		if r.Required {
			wantRequired = append(wantRequired, r.Member)
		}
	}
	if !slices.Equal(doc.Required, wantRequired) {
		t.Errorf("required = %v, want %v", doc.Required, wantRequired)
	}
	if len(doc.Properties) != len(rules) {
		t.Errorf("the schema declares %d properties for %d contract members", len(doc.Properties), len(rules))
	}
}

// The schema and the decoder are typed from one table, so a constraint that
// appears in one and not the other is a document accepted by one reader and
// refused by the other.
func TestSchemaConstraintsMatchTheContractTable(t *testing.T) {
	t.Parallel()
	doc := renderSchema(t)

	for _, r := range agentstate.Rules() {
		member := doc.Properties[r.Member]
		if r.Type == "integer|string" {
			if member.Type != "" || len(member.OneOf) == 0 {
				t.Errorf("member %q declares type %q and %d union branches, want a union",
					r.Member, member.Type, len(member.OneOf))
			}
		} else if member.Type != r.Type {
			t.Errorf("member %q is declared %q, want %q", r.Member, member.Type, r.Type)
		}
		if !slices.Equal(member.Enum, r.Enum) {
			t.Errorf("member %q declares enum %v, want %v", r.Member, member.Enum, r.Enum)
		}
		if member.Format != r.Format {
			t.Errorf("member %q declares format %q, want %q", r.Member, member.Format, r.Format)
		}
		if member.MaxLength != r.MaxRunes {
			t.Errorf("member %q declares maxLength %d, want %d", r.Member, member.MaxLength, r.MaxRunes)
		}
		for _, name := range r.Compat {
			if !strings.Contains(member.Comment, name) {
				t.Errorf("member %q names no compatibility spelling %q: %q", r.Member, name, member.Comment)
			}
		}
	}
}

// The step member is a closed union of an ordinal and a phase name. A schema
// that widened it to a bare string or a bare integer would accept documents
// the decoder refuses.
func TestSchemaEmitsAUnionForTheStepMember(t *testing.T) {
	t.Parallel()
	step := renderSchema(t).Properties["step"]
	if len(step.OneOf) != 2 {
		t.Fatalf("the step member declares %d union branches, want 2", len(step.OneOf))
	}

	var ordinal, label int
	for _, branch := range step.OneOf {
		switch {
		case branch.Type == "integer" && branch.Minimum != nil && *branch.Minimum == 0:
			ordinal++
		case branch.Type == "string" && branch.MinLength != nil && *branch.MinLength == 1:
			label++
		default:
			t.Errorf("the step union carries a branch matching neither form: %+v", branch)
		}
	}
	if ordinal != 1 || label != 1 {
		t.Errorf("the step union carries %d ordinal and %d label branches, want one of each", ordinal, label)
	}
}

// Labels carry integrator metadata, not nested documents.
func TestSchemaTypesLabelValuesAsStrings(t *testing.T) {
	t.Parallel()
	labels := renderSchema(t).Properties["labels"]
	var values schemaMember
	if err := json.Unmarshal(labels.AdditionalProperties, &values); err != nil {
		t.Fatalf("the labels member declares no value schema: %v", err)
	}
	if values.Type != "string" {
		t.Errorf("label values are declared %q, want string", values.Type)
	}
}

// The published document is regenerated and compared byte for byte by the
// generator's gate, so a render that varied between calls would fail a clean
// tree.
func TestSchemaRendersTheSameBytesEveryCall(t *testing.T) {
	t.Parallel()
	first, err := agentstate.SchemaJSON()
	if err != nil {
		t.Fatalf("render schema: %v", err)
	}
	second, err := agentstate.SchemaJSON()
	if err != nil {
		t.Fatalf("render schema: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two renders differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// A consumer that resolves the $id receives the document this build renders,
// so the identifier is the address the schema is served from rather than a
// label.
func TestSchemaIdentifiesThePublishedContract(t *testing.T) {
	t.Parallel()
	doc := renderSchema(t)
	if doc.ID != agentstate.SchemaID {
		t.Errorf("$id = %q, want %q", doc.ID, agentstate.SchemaID)
	}
	if doc.Dialect != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q, want the 2020-12 dialect", doc.Dialect)
	}
	if doc.Type != "object" {
		t.Errorf("type = %q, want object", doc.Type)
	}
	if !doc.AdditionalProperties {
		t.Error("the schema refuses undefined members, which the decoder preserves")
	}
}
