package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// What a program on the other side of agentfs reads: two published schemas and
// one generated reference page. The gates below hold all three against the
// structs the encoders write, in both directions — a member the encoder emits
// and nothing publishes is a contract an integrator cannot conform to, and a
// member published and never emitted is a promise nothing keeps.

// Where the published contracts and the page describing them live, relative to
// the module root.
const (
	schemaDir      = "schema"
	frozenDir      = ".frozen"
	referencePage  = "docs/reference/report-envelope.md"
	howToPage      = "docs/how-to/read-agentfs-output.md"
	stateSchemaDoc = "agent-state.v1.json"
	reportDoc      = "report.v1.json"
	streamDoc      = "stream.v1.json"
)

// regenerate is what a failing gate tells a reader to run.
const regenerate = "run: task gen"

// moduleRoot returns the directory holding go.mod, walking up from this
// package. The published artifacts are read through it, so the gates run from
// any working directory the go tool chooses.
func moduleRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("no go.mod at or above the working directory")
		}
		dir = parent
	}
}

// readRepoFile reads a committed artifact by its module-relative path.
func readRepoFile(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join(moduleRoot(tb), filepath.FromSlash(name)))
	if err != nil {
		tb.Fatalf("%v; %s", err, regenerate)
	}
	return b
}

// publishedObject is the part of a rendered schema these gates read.
type publishedObject struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
	Defs       map[string]publishedObject `json:"$defs"`
}

// parseSchema renders a document and returns the object the gate is about:
// the root, or the definition named by def.
func parseSchema(tb testing.TB, render func() ([]byte, error), def string) publishedObject {
	tb.Helper()
	doc, err := render()
	if err != nil {
		tb.Fatalf("render: %v", err)
	}
	var root publishedObject
	if err := json.Unmarshal(doc, &root); err != nil {
		tb.Fatalf("parse the rendered schema: %v", err)
	}
	if def == "" {
		return root
	}
	object, ok := root.Defs[def]
	if !ok {
		tb.Fatalf("the schema defines no %q", def)
	}
	return object
}

// field is one JSON-tagged field of an encoded struct.
type field struct {
	// name is the member the encoder writes.
	name string
	// omitted reports whether the encoder leaves the member out when the field
	// is empty, which is what makes a member optional in the schema.
	omitted bool
}

// encodedFields returns the members a struct encodes to, in the order
// [encoding/json] emits them, which is declaration order.
func encodedFields(tb testing.TB, v any) []field {
	tb.Helper()
	typ := reflect.TypeOf(v)
	out := make([]field, 0, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			tb.Fatalf("%s.%s carries no json tag, so what it encodes to is the field name", typ, f.Name)
		}
		name, options, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		out = append(out, field{name: name, omitted: slices.Contains(strings.Split(options, ","), "omitempty")})
	}
	return out
}

// publishedContracts are the three objects an agent reads, each paired with the
// struct the encoder writes it from and the table both the schema and the
// reference page are rendered from.
func publishedContracts() []struct {
	name    string
	render  func() ([]byte, error)
	def     string
	encoded any
	table   []Member
} {
	return []struct {
		name    string
		render  func() ([]byte, error)
		def     string
		encoded any
		table   []Member
	}{
		{"envelope", EnvelopeSchemaJSON, "", Envelope{}, EnvelopeMembers()},
		{"diagnostic", EnvelopeSchemaJSON, diagnosticDef, diag.Diagnostic{}, DiagnosticMembers()},
		{"record", StreamSchemaJSON, "", Record{}, RecordMembers()},
	}
}

// TestEveryEnvelopeMemberIsPublished asserts each published schema declares
// exactly the members its struct encodes, requires exactly the members the
// encoder always writes, and declares them in the order they arrive on the
// wire.
//
// A field added to one of these structs without a table entry fails here, which
// is the whole point: a consumer's parser is written against the schema, and a
// member that reaches it undeclared reaches it unhandled.
func TestEveryEnvelopeMemberIsPublished(t *testing.T) {
	for _, c := range publishedContracts() {
		t.Run(c.name, func(t *testing.T) {
			object := parseSchema(t, c.render, c.def)
			fields := encodedFields(t, c.encoded)

			for _, f := range fields {
				if _, ok := object.Properties[f.name]; !ok {
					t.Errorf("the encoder writes %q and the schema declares no such property; %s",
						f.name, regenerate)
					continue
				}
				required := slices.Contains(object.Required, f.name)
				if required == f.omitted {
					t.Errorf("%q is %s by the encoder and %s by the schema",
						f.name, presence(!f.omitted), presence(required))
				}
			}
			for name := range object.Properties {
				if !slices.ContainsFunc(fields, func(f field) bool { return f.name == name }) {
					t.Errorf("the schema declares the property %q, which the encoder never writes", name)
				}
			}

			// The tables are what both published forms are rendered from, so
			// an entry out of declaration order publishes a member order no
			// output has.
			var members, wire []string
			for _, m := range c.table {
				members = append(members, m.Name)
			}
			for _, f := range fields {
				wire = append(wire, f.name)
			}
			if !slices.Equal(members, wire) {
				t.Errorf("the table publishes %v and the encoder writes %v", members, wire)
			}
		})
	}
}

// presence names how a member is declared, for a failure that says which side
// disagrees.
func presence(always bool) string {
	if always {
		return "always written"
	}
	return "written only when it carries something"
}

// TestPublishedSchemasMatchTheGenerator asserts every committed contract is
// what this build renders, byte for byte, and that its frozen checksum covers
// those bytes.
//
// The published files are what an integrator vendors. A hand edit to one, or a
// table change that was not regenerated, leaves them describing a build nobody
// runs.
func TestPublishedSchemasMatchTheGenerator(t *testing.T) {
	published := []struct {
		name   string
		render func() ([]byte, error)
	}{
		{stateSchemaDoc, agentstate.SchemaJSON},
		{reportDoc, EnvelopeSchemaJSON},
		{streamDoc, StreamSchemaJSON},
	}

	for _, p := range published {
		t.Run(p.name, func(t *testing.T) {
			want, err := p.render()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			committed := readRepoFile(t, path.Join(schemaDir, p.name))
			if !bytes.Equal(committed, want) {
				t.Errorf("the committed %s is not what this build renders; %s", p.name, regenerate)
			}

			sum := sha256.Sum256(committed)
			frozen := path.Join(schemaDir, frozenDir, strings.TrimSuffix(p.name, ".json")+".sha256")
			wantLine := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), path.Join(schemaDir, p.name))
			if got := string(readRepoFile(t, frozen)); got != wantLine {
				t.Errorf("the frozen checksum does not cover the committed %s; %s\nrecorded: %s\ncomputed: %s",
					p.name, regenerate, strings.TrimSpace(got), strings.TrimSpace(wantLine))
			}
		})
	}
}

// TestEveryPayloadKindIsDescribed asserts the generated reference carries a
// section for every kind either vocabulary names.
//
// The two vocabularies both carry "error", so each heading names the kind and
// the form it belongs to. The headings are the ones docsgen renders; a kind
// added to a vocabulary and described nowhere leaves a consumer with a payload
// and no shape for it.
func TestEveryPayloadKindIsDescribed(t *testing.T) {
	page := string(readRepoFile(t, referencePage))

	headings := make([]string, 0, len(Kinds())+len(StreamKinds()))
	for _, k := range Kinds() {
		headings = append(headings, "### The `"+k+"` result")
	}
	for _, k := range StreamKinds() {
		headings = append(headings, "### The `"+k+"` record")
	}

	for _, h := range headings {
		switch strings.Count(page, h+"\n") {
		case 1:
		case 0:
			t.Errorf("%s carries no %q section; %s", referencePage, h, regenerate)
		default:
			t.Errorf("%s carries %q more than once, so a reference to it resolves to two places", referencePage, h)
		}
	}
}

// TestTheHowToShowsTheWholeEnvelope asserts the one JSON sample on the how-to
// page carries every member of the frame and nothing else.
//
// The page is written by hand, and it is the first thing the author of a
// consumer reads. A sample short of a member teaches a parser that the member
// is optional; a sample carrying one the encoder does not write teaches it to
// expect something that never arrives.
func TestTheHowToShowsTheWholeEnvelope(t *testing.T) {
	const fence = "```json\n"
	page := string(readRepoFile(t, howToPage))
	start := strings.Index(page, fence)
	if start < 0 {
		t.Fatalf("%s shows no JSON sample of the envelope", howToPage)
	}
	body := page[start+len(fence):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatalf("the JSON sample in %s is not closed", howToPage)
	}

	var sample map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body[:end]), &sample); err != nil {
		t.Fatalf("the sample in %s is not JSON: %v", howToPage, err)
	}

	for _, m := range EnvelopeMembers() {
		if _, ok := sample[m.Name]; !ok {
			t.Errorf("%s shows an envelope without %q", howToPage, m.Name)
		}
	}
	for name := range sample {
		if !slices.ContainsFunc(EnvelopeMembers(), func(m Member) bool { return m.Name == name }) {
			t.Errorf("%s shows an envelope member %q that the encoder never writes", howToPage, name)
		}
	}

	var declared, kind string
	if err := json.Unmarshal(sample["schema"], &declared); err != nil || declared != EnvelopeSchema {
		t.Errorf("the sample declares the schema %q, and the encoder stamps %q", declared, EnvelopeSchema)
	}
	if err := json.Unmarshal(sample["kind"], &kind); err != nil || !slices.Contains(Kinds(), kind) {
		t.Errorf("the sample is of kind %q, which is not in the vocabulary", kind)
	}
}

// TestEverySeverityIsPublished asserts the published severity vocabulary is the
// whole vocabulary: the ordinal past the last published level names nothing.
//
// A level added to diag without a row here would be a value agentfs emits and
// the schema forbids, which fails a consumer's validator on output agentfs
// considers correct.
func TestEverySeverityIsPublished(t *testing.T) {
	published := severities()
	if next := diag.Severity(len(published)).String(); next != "unknown" {
		t.Errorf("diag names the severity %q past the %d published levels; add it to severities()",
			next, len(published))
	}
	for i, name := range published {
		if got := diag.Severity(i).String(); got != name {
			t.Errorf("severity %d is %q and the schema publishes %q", i, got, name)
		}
	}
}

// TestPublishedCeilingsHold asserts a diagnostic built the way agentfs builds
// them carries no member longer than the maxLength published for it.
//
// The ceilings are stated in two places — where diag abbreviates and where the
// schema publishes — so a validator holding output to the published number is
// held here to the number the producer applies.
func TestPublishedCeilingsHold(t *testing.T) {
	long := strings.Repeat("verylong/", 200)
	d := diag.About(diag.CodeWrongType, "agent/state.json", "/"+long,
		"A member holds a JSON type the contract does not allow.", "Give the member a string.", long)

	value := reflect.ValueOf(d)
	for _, m := range DiagnosticMembers() {
		if m.MaxRunes == 0 {
			continue
		}
		field := value.FieldByNameFunc(func(name string) bool {
			f, _ := reflect.TypeOf(d).FieldByName(name)
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			return tag == m.Name
		})
		if !field.IsValid() {
			t.Errorf("no field of a diagnostic encodes to %q", m.Name)
			continue
		}
		if got := utf8.RuneCountInString(field.String()); got > m.MaxRunes {
			t.Errorf("%q is published with a ceiling of %d runes and a diagnostic carries %d",
				m.Name, m.MaxRunes, got)
		}
	}
}

// TestRenderedSchemasAreDeterministic asserts two renders of an unchanged table
// produce identical bytes, so the gate that fails on a dirty tree after
// regeneration reports a changed contract rather than a generator that varies.
func TestRenderedSchemasAreDeterministic(t *testing.T) {
	for _, render := range []func() ([]byte, error){EnvelopeSchemaJSON, StreamSchemaJSON} {
		first, err := render()
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		second, err := render()
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Errorf("two renders differ:\nfirst:\n%s\nsecond:\n%s", first, second)
		}
	}
}

// TestPublishedIdentifiersShareTheStateSchemaBase asserts the read-side
// documents are identified under the same base as the contract an agent writes,
// which is the base the Pages workflow serves and derives a path from.
func TestPublishedIdentifiersShareTheStateSchemaBase(t *testing.T) {
	base := agentstate.SchemaID[:strings.LastIndex(agentstate.SchemaID, "/")+1]
	for name, id := range map[string]string{reportDoc: EnvelopeSchemaID, streamDoc: StreamSchemaID} {
		if want := base + name; id != want {
			t.Errorf("%s is identified as %q, and the site serves it from %q", name, id, want)
		}
	}
}

// TestOrderedObjectKeepsInsertionOrder asserts the helper the schemas are built
// from marshals members in the order they were set, and keeps a member's
// position when its value is replaced. Order is what makes a rendered schema
// byte-stable.
func TestOrderedObjectKeepsInsertionOrder(t *testing.T) {
	o := newObject()
	o.set("zebra", 1)
	o.set("alpha", 2)
	o.set("zebra", 3)

	got, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"zebra":3,"alpha":2}`; string(got) != want {
		t.Errorf("marshalled %s, want %s", got, want)
	}
}

// TestOrderedObjectReportsAnUnmarshallableMember asserts a member that cannot
// be encoded fails by name, so a table entry holding a value JSON has no form
// for names itself rather than producing a truncated document.
func TestOrderedObjectReportsAnUnmarshallableMember(t *testing.T) {
	o := newObject()
	o.set("broken", func() {})

	_, err := json.Marshal(o)
	if err == nil {
		t.Fatal("marshalled a member JSON has no form for")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error %q does not name the member that failed", err)
	}
}
