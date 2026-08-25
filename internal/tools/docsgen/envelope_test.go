package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The payload tables on this page describe Go types the encoder marshals from,
// and no compiler checks a table against a type. These gates read the
// declarations instead: a member added to a payload without a row, or a row
// describing a member the type stopped holding, fails here rather than
// reaching an integrator as a page that describes a shape agentfs does not
// emit.

// parseSource parses one module-relative Go file.
func parseSource(t *testing.T, source string) *ast.File {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locate the module root: %v", err)
	}
	name := filepath.Join(root, filepath.FromSlash(source))
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	return file
}

// encodedMembers returns the JSON members a struct declaration encodes to, in
// declaration order, which is the order [encoding/json] emits them.
func encodedMembers(t *testing.T, source, typeName string) []string {
	t.Helper()

	var out []string
	found := false
	ast.Inspect(parseSource(t, source), func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != typeName {
			return true
		}
		structure, ok := spec.Type.(*ast.StructType)
		if !ok {
			t.Fatalf("%s in %s is not a struct", typeName, source)
		}
		found = true
		for _, f := range structure.Fields.List {
			if f.Tag == nil {
				t.Errorf("%s.%v in %s carries no tag, so it encodes under its field name",
					typeName, f.Names, source)
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				t.Fatalf("read the tag of %s.%v: %v", typeName, f.Names, err)
			}
			name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			out = append(out, name)
		}
		return false
	})
	if !found {
		t.Fatalf("%s declares no type %s", source, typeName)
	}
	return out
}

// TestEveryPayloadMemberMatchesItsType asserts each payload table describes
// exactly the members its Go type encodes, in the order the encoder writes
// them.
func TestEveryPayloadMemberMatchesItsType(t *testing.T) {
	described := slices.Concat(envelopePayloads(), recordPayloads(), []payload{agentPayload()})

	for _, p := range described {
		if p.carriesNone != "" {
			continue
		}
		t.Run(p.typeName, func(t *testing.T) {
			want := encodedMembers(t, p.source, p.typeName)
			got := make([]string, 0, len(p.members))
			for _, m := range p.members {
				got = append(got, m.name)
			}
			if !slices.Equal(got, want) {
				t.Errorf("the page describes %v and %s.%s encodes %v", got, p.source, p.typeName, want)
			}
		})
	}
}

// TestAKindThatCarriesNoPayloadNamesNoType asserts a kind documented as
// carrying no data member declares no type either, so the gate above cannot be
// skipped by a row that both names a type and claims there is nothing to
// describe.
func TestAKindThatCarriesNoPayloadNamesNoType(t *testing.T) {
	for _, p := range slices.Concat(envelopePayloads(), recordPayloads()) {
		if p.carriesNone == "" {
			continue
		}
		if p.typeName != "" || p.source != "" || len(p.members) > 0 {
			t.Errorf("the %s kind is documented as carrying no payload and names %s.%s with %d members",
				p.kind, p.source, p.typeName, len(p.members))
		}
	}
}

// eventPrefix is the name a status record's event constants share. The
// vocabulary they hold is published on the reference page, and there is no
// exported table to read it from, so the declarations are the table.
const eventPrefix = "event"

// statusEvents returns the values of the constants a status record's event
// member is written from.
func statusEvents(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, decl := range parseSource(t, streamSource).Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || !strings.HasPrefix(value.Names[0].Name, eventPrefix) {
				continue
			}
			for _, v := range value.Values {
				literal, ok := v.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				name, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("read the value of %s: %v", value.Names[0].Name, err)
				}
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s declares no constant named %s*, so the event vocabulary has moved", streamSource, eventPrefix)
	}
	return out
}

// TestEveryStatusEventIsDescribed asserts the page names every condition a
// status record reports. A consumer branches on this vocabulary, and one the
// page does not carry is a record a consumer meets with no idea what to do.
func TestEveryStatusEventIsDescribed(t *testing.T) {
	page := string(generateInto(t, t.TempDir())["report-envelope.md"])
	for _, event := range statusEvents(t) {
		if !strings.Contains(page, "`"+event+"`") {
			t.Errorf("a status record reports %q and the page describes it nowhere", event)
		}
	}
}

// TestVocabularyWalkStopsAtTheUnnamedOrdinal asserts the walk that takes a
// vocabulary from a String method ends where the type names nothing, and
// reports a type that names everything rather than publishing a truncated
// vocabulary as a closed one.
func TestVocabularyWalkStopsAtTheUnnamedOrdinal(t *testing.T) {
	names := []string{"first", "second"}
	got, err := vocabulary(func(i int) string {
		if i < len(names) {
			return names[i]
		}
		return "unknown"
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !slices.Equal(got, names) {
		t.Errorf("walked %v, want %v", got, names)
	}

	if _, err := vocabulary(func(int) string { return "endless" }); err == nil {
		t.Error("a String method that names every ordinal walked to a vocabulary")
	}
}

// TestASampleCarriesAValuePerDocumentedMember asserts the sample assembler
// refuses a payload it has no value for, so a member added to a sampled payload
// fails the generator rather than rendering as an empty one.
func TestASampleCarriesAValuePerDocumentedMember(t *testing.T) {
	extra := []payload{{
		kind:    "sampled",
		members: []member{{"undescribed", jsonString, "A member the sample carries no value for."}},
	}}
	if _, err := samplePayload(extra, "sampled"); err == nil {
		t.Error("assembled a sample for a member with no value")
	}
	if _, err := samplePayload(extra, "absent"); err == nil {
		t.Error("assembled a sample for a kind the page describes nowhere")
	}
}
