// Package conformance_test is the evaluation suite for the agentfs workspace
// contract.
//
// Each directory under cases/ holds one document and the diagnostics the
// decoder must report for it. The corpus is the regression suite for
// [agentstate.Decode] and the worked-example set an integrator reads: a case
// that carries a fixed/state.json shows the document after its hints are
// applied, which is what makes a hint actionable rather than decorative.
//
// The corpus has two halves. A conformance case is a document an integrator
// writes by mistake: a wrong type, a status outside the vocabulary, a timestamp
// with no offset. An adversarial case is a document written to reach the
// operator's terminal through the reader: an escape sequence in a member name,
// a bidirectional override in a value, an unpaired surrogate, a member name of
// several thousand runes. [TestNoCaseRendersATerminalControl] evaluates the
// second half, and [TestTheCorpusCarriesAdversarialCases] keeps it from
// thinning back into the first.
//
// Comparison is by diagnostic code and JSON Pointer, the two members of a
// diagnostic a consumer branches on. Messages and hints are prose for a human
// and carry no contract, so a case that matched on message text would fail on
// a reworded sentence and pass on a changed meaning.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// casesDir holds one directory per case.
const casesDir = "testdata/cases"

// fixedDir names the subdirectory holding the document a case's hints produce.
const fixedDir = "fixed"

// minimumCases is the size below which the corpus stops being an evaluation
// suite. A change that deletes cases to make a decoder edit pass fails here
// before it reaches the case it deleted.
const minimumCases = 40

// minimumSchemaChecked is the number of cases [TestSchemaAgreesWithDecoder]
// must actually check. The test skips documents the decoder refuses and
// documents read under the compatibility profile, so without a floor it would
// pass vacuously on a corpus that stopped exercising the schema.
const minimumSchemaChecked = 20

// observedNow is the observer's clock every document is decoded against. A
// fixed instant makes the future-timestamp case decidable from the corpus
// alone rather than from the day the suite runs.
var observedNow = time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)

// decodeOptions is the reading configuration every case is decoded under.
func decodeOptions() agentstate.Options {
	return agentstate.Options{Now: observedNow}
}

// expectation is the machine-readable half of a case.
type expectation struct {
	// Document names the file holding the document under test. The document's
	// filename is what selects the compatibility-filename diagnostic, so a
	// case that exercises one holds a document under that name. Empty means
	// [agentstate.StateFile].
	Document string `json:"document,omitempty"`
	// Codes lists every diagnostic code the decoder raises for the document,
	// deduplicated and sorted.
	Codes []string `json:"codes"`
	// Pointers lists every member a diagnostic names, deduplicated and
	// sorted. A finding about the document as a whole carries no pointer and
	// appears here as nothing, so a case that raises only those records an
	// empty list. Codes alone cannot separate one mistyped member from three,
	// which is the regression a decoder that abandons a document at its first
	// type error introduces.
	Pointers []string `json:"pointers,omitempty"`
	// Status is the wire spelling of the status the document decodes to.
	Status string `json:"status"`
	// Step is the step member as the decoder writes it back. Absent means the
	// document declares no step. Recording the JSON rather than a rendering
	// keeps an ordinal distinguishable from a label that spells a number, and
	// asserts the round trip [agentstate.Step] undertakes.
	Step json.RawMessage `json:"step,omitempty"`
	// Extra names the undefined members the decoder preserves, sorted. A
	// document whose undefined members are dropped for exceeding the ceiling
	// records none, which is what separates preservation from a claim of it.
	Extra []string `json:"extra,omitempty"`
	// Values records the offending value each diagnostic quotes, as
	// "code=value", deduplicated and sorted. A diagnostic that quotes nothing
	// is absent. The rendering is what a reader is shown, so recording it is
	// what separates a value that names the offending text from one that names
	// a neighbouring member or an empty string.
	Values []string `json:"values,omitempty"`
	// Notes states in one sentence what the case asserts.
	Notes string `json:"notes"`
}

// conformanceCase is one loaded case directory.
type conformanceCase struct {
	name     string
	dir      string
	document string
	src      []byte
	expect   expectation
}

// docPath is the path the decoder is told the document was read from. The
// decoder reads its base name to detect a compatibility filename.
func (c *conformanceCase) docPath() string {
	return path.Join(casesDir, c.name, c.document)
}

// fixedPath is the path of the document a case's hints produce.
func (c *conformanceCase) fixedPath() string {
	return filepath.Join(c.dir, fixedDir, agentstate.StateFile)
}

// loadCases reads every case directory.
func loadCases(tb testing.TB) []*conformanceCase {
	tb.Helper()
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		tb.Fatalf("read %s: %v", casesDir, err)
	}
	cases := make([]*conformanceCase, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cases = append(cases, loadCase(tb, e.Name()))
	}
	if len(cases) < minimumCases {
		tb.Fatalf("corpus holds %d cases, want at least %d", len(cases), minimumCases)
	}
	return cases
}

// loadCase reads one case directory.
func loadCase(tb testing.TB, name string) *conformanceCase {
	tb.Helper()
	dir := filepath.Join(casesDir, name)

	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		tb.Fatalf("case %s: %v", name, err)
	}
	var exp expectation
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if decodeErr := dec.Decode(&exp); decodeErr != nil {
		tb.Fatalf("case %s: expect.json: %v", name, decodeErr)
	}

	document := exp.Document
	if document == "" {
		document = agentstate.StateFile
	}
	src, err := os.ReadFile(filepath.Join(dir, document))
	if err != nil {
		tb.Fatalf("case %s: %v", name, err)
	}
	return &conformanceCase{name: name, dir: dir, document: document, src: src, expect: exp}
}

// codesOf returns the distinct codes in ds, sorted, which is the form
// expect.json records.
func codesOf(ds []diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if !slices.Contains(out, string(d.Code)) {
			out = append(out, string(d.Code))
		}
	}
	slices.Sort(out)
	return out
}

// pointersOf returns the distinct members ds names, sorted, which is the form
// expect.json records. A finding about the document as a whole carries no
// pointer and contributes nothing.
func pointersOf(ds []diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if d.Pointer != "" && !slices.Contains(out, d.Pointer) {
			out = append(out, d.Pointer)
		}
	}
	slices.Sort(out)
	return out
}

// extraOf returns the undefined members the decoder preserved, sorted.
func extraOf(st *agentstate.State) []string {
	return slices.Sorted(maps.Keys(st.Extra))
}

// stepJSON returns the step as the decoder writes it back.
func stepJSON(tb testing.TB, st *agentstate.State) []byte {
	tb.Helper()
	out, err := st.Step.MarshalJSON()
	if err != nil {
		tb.Fatalf("marshal step: %v", err)
	}
	return compact(tb, out)
}

// wantStep is the step a case records, JSON null when it records none.
func wantStep(tb testing.TB, c *conformanceCase) []byte {
	tb.Helper()
	if len(c.expect.Step) == 0 {
		return []byte("null")
	}
	return compact(tb, c.expect.Step)
}

// compact strips a JSON value's layout, so a case and the decoder are compared
// on what the value is rather than on how it was typed.
func compact(tb testing.TB, value []byte) []byte {
	tb.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, value); err != nil {
		tb.Fatalf("compact %s: %v", value, err)
	}
	return buf.Bytes()
}

// render joins diagnostics one per line, for a failure message that names what
// the decoder actually reported.
func render(ds []diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString("\n\t")
		b.WriteString(d.String())
	}
	return b.String()
}

// hasError reports whether any diagnostic makes the document unusable as state.
func hasError(ds []diag.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d diag.Diagnostic) bool { return d.Severity == diag.Error })
}

// TestConformance asserts the decoder reports exactly the codes and members
// each case records, and decodes the document to the state it records.
func TestConformance(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.name, func(t *testing.T) {
			st, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())
			if got := codesOf(ds); !slices.Equal(got, c.expect.Codes) {
				t.Errorf("codes: got %v, want %v%s", got, c.expect.Codes, render(ds))
			}
			if got, want := pointersOf(ds), c.expect.Pointers; !slices.Equal(got, want) {
				t.Errorf("members named: got %v, want %v%s", got, want, render(ds))
			}
			if got, want := valuesOf(ds), c.expect.Values; !slices.Equal(got, want) {
				t.Errorf("values quoted: got %v, want %v%s", got, want, render(ds))
			}
			if got := st.Status.String(); got != c.expect.Status {
				t.Errorf("status: got %q, want %q", got, c.expect.Status)
			}
			if got, want := stepJSON(t, &st), wantStep(t, c); !bytes.Equal(got, want) {
				t.Errorf("step: got %s, want %s", got, want)
			}
			if got, want := extraOf(&st), c.expect.Extra; !slices.Equal(got, want) {
				t.Errorf("preserved undefined members: got %v, want %v", got, want)
			}
		})
	}
}

// TestHintsAreActionable asserts every hint the corpus provokes resolves the
// finding it is attached to. A case whose diagnostics carry a hint holds the
// document those hints produce; that document decodes without an
// error-severity diagnostic, and raises none of the codes the hints were
// attached to. A hint that leaves its own finding standing is decoration, and
// a diagnostic's hint is contracted to name the edit that clears it.
func TestHintsAreActionable(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.name, func(t *testing.T) {
			_, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())

			src, err := os.ReadFile(c.fixedPath())
			if errors.Is(err, fs.ErrNotExist) {
				for _, d := range ds {
					if d.Hint != "" {
						t.Fatalf("%s carries a hint but the case holds no %s/%s: %s",
							d.Code, fixedDir, agentstate.StateFile, d)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("read fixed document: %v", err)
			}

			fixedDoc := path.Join(casesDir, c.name, fixedDir, agentstate.StateFile)
			_, fixedDiags := agentstate.Decode(fixedDoc, src, decodeOptions())
			for _, d := range fixedDiags {
				if d.Severity == diag.Error {
					t.Errorf("the repaired document still raises %s", d)
				}
			}

			hinted := make([]string, 0, len(ds))
			for _, d := range ds {
				if d.Hint != "" && !slices.Contains(hinted, string(d.Code)) {
					hinted = append(hinted, string(d.Code))
				}
			}
			for _, code := range codesOf(fixedDiags) {
				if slices.Contains(hinted, code) {
					t.Errorf("%s hints at an edit the repaired document applies and still raises it", code)
				}
			}
		})
	}
}

// exemptCodes are the codes no state document can provoke, because each
// describes a condition outside the document. The map is the allow-list
// [TestEveryCodeHasACase] works from, and every other registered code must
// appear in some case.
var exemptCodes = map[diag.Code]string{
	diag.CodeDocumentTooLarge:   "the reader refuses the file before there is a document to decode",
	diag.CodeUnreadable:         "the read itself fails, so no bytes reach the decoder",
	diag.CodeStale:              "the observer compares the document's age against its heartbeat",
	diag.CodeSettling:           "the observer sees the document change between two reads",
	diag.CodeDiagnosticsDropped: "the diagnostic store reaches its own ceiling",
	diag.CodeRootLost:           "the workspace root becomes unreadable",
	diag.CodeRootRecovered:      "the workspace root becomes readable again",
	diag.CodeNodeCeiling:        "retained state is shed to stay within the resident ceiling",
	diag.CodeEntriesTruncated:   "a directory holds more entries than the per-directory ceiling",
	diag.CodeDepthTruncated:     "a subtree is deeper than the depth ceiling",
	diag.CodeWatchBudget:        "the kernel watch budget is exhausted",
	diag.CodeBatchTruncated:     "a change batch exceeds its ceiling",
}

// TestEveryCodeHasACase asserts the corpus covers every diagnostic code a
// document can provoke, and that the exemption list holds nothing the corpus
// covers. A code with no case is a claim the reference suite does not test.
func TestEveryCodeHasACase(t *testing.T) {
	covered := map[diag.Code]string{}
	for _, c := range loadCases(t) {
		for _, code := range c.expect.Codes {
			covered[diag.Code(code)] = c.name
		}
	}

	for _, info := range diag.Codes() {
		reason, exempt := exemptCodes[info.Code]
		switch {
		case exempt && covered[info.Code] != "":
			t.Errorf("%s is exempt as %q but case %s covers it; drop the exemption",
				info.Code, reason, covered[info.Code])
		case !exempt && covered[info.Code] == "":
			t.Errorf("%s (%s) has no case", info.Code, info.Summary)
		}
	}

	for code := range exemptCodes {
		if _, registered := diag.Lookup(code); !registered {
			t.Errorf("%s is exempt but is not a registered code; drop the exemption", code)
		}
	}
}

// TestCorpusIsWellFormed asserts each case records its expectation in the form
// the other tests read: codes sorted and distinct, a registered code, a status
// the contract spells, and one sentence saying what the case asserts.
func TestCorpusIsWellFormed(t *testing.T) {
	spellings := append(agentstate.Vocabulary(), agentstate.StatusUnknown.String())
	for _, c := range loadCases(t) {
		t.Run(c.name, func(t *testing.T) {
			if !slices.IsSorted(c.expect.Codes) {
				t.Errorf("codes %v are not sorted", c.expect.Codes)
			}
			if len(slices.Compact(slices.Clone(c.expect.Codes))) != len(c.expect.Codes) {
				t.Errorf("codes %v repeat a code", c.expect.Codes)
			}
			for _, code := range c.expect.Codes {
				if _, ok := diag.Lookup(diag.Code(code)); !ok {
					t.Errorf("code %q is not registered", code)
				}
			}
			if !slices.Contains(spellings, c.expect.Status) {
				t.Errorf("status %q is not a contract spelling", c.expect.Status)
			}
			if !strings.HasSuffix(c.expect.Notes, ".") {
				t.Errorf("notes %q is not a sentence", c.expect.Notes)
			}
			if !slices.IsSorted(c.expect.Pointers) {
				t.Errorf("pointers %v are not sorted", c.expect.Pointers)
			}
			if len(slices.Compact(slices.Clone(c.expect.Pointers))) != len(c.expect.Pointers) {
				t.Errorf("pointers %v repeat a member", c.expect.Pointers)
			}
			if !slices.IsSorted(c.expect.Extra) {
				t.Errorf("extra %v is not sorted", c.expect.Extra)
			}
			declared := declaredMembers(c.src)
			reported := reportedPointers(declared)
			for _, pointer := range c.expect.Pointers {
				if !slices.Contains(reported, pointer) {
					t.Errorf("pointer %q names a member the document does not declare", pointer)
				}
			}
			for _, name := range c.expect.Extra {
				if !slices.Contains(declared, name) {
					t.Errorf("extra %q names a member the document does not declare", name)
				}
			}
			if !agentstate.IsStateFile(c.document) {
				t.Errorf("document %q is not a name the contract accepts", c.document)
			}
			_, err := os.Stat(c.fixedPath())
			switch {
			case err == nil && len(c.expect.Codes) == 0:
				t.Errorf("the case raises nothing, so its %s/%s repairs nothing", fixedDir, agentstate.StateFile)
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				t.Errorf("stat the repaired document: %v", err)
			}
		})
	}
}

// reportedPointers returns the pointer a diagnostic carries for each member a
// document declares.
//
// A member name is workspace input, so a diagnostic neutralizes and bounds it
// rather than quoting it back. A case whose document names a member with an
// escape or a zero-width joiner in it therefore records a pointer that is not
// the name spelled character for character, and the check asks
// [diag.About] what pointer it builds rather than restating the transformation.
func reportedPointers(declared []string) []string {
	out := make([]string, 0, len(declared))
	for _, name := range declared {
		out = append(out, diag.About(diag.CodeUnknownMember, "", "/"+name, "", "", "").Pointer)
	}
	return out
}

// declaredMembers returns the member names a document declares, empty when the
// bytes are not a JSON object.
func declaredMembers(src []byte) []string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(src, &doc); err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(doc))
}

// memberTypingCodes is the code range that reports a member holding a value the
// contract does not admit: a wrong type, an empty string, an over-long string,
// a step that is neither form, a negative ordinal.
const memberTypingCodes = "AFS2"

// TestCorpusCoversTheContract asserts the corpus exercises the contract's
// surface rather than a subset of it. [TestEveryCodeHasACase] covers the
// diagnostic registry; this covers the table the diagnostics are raised
// against, so a member or a status added to the contract without a case fails
// here rather than shipping untested.
func TestCorpusCoversTheContract(t *testing.T) {
	cases := loadCases(t)

	declaredBy := map[string]string{}
	mistypedBy := map[string]string{}
	v1StatusBy := map[string]string{}
	aliasStatusBy := map[string]string{}

	for _, c := range cases {
		for _, name := range declaredMembers(c.src) {
			declaredBy[name] = c.name
		}
		st, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())
		for _, d := range ds {
			if strings.HasPrefix(string(d.Code), memberTypingCodes) && d.Pointer != "" {
				mistypedBy[strings.TrimPrefix(d.Pointer, "/")] = c.name
			}
		}
		spelling := st.Status.String()
		switch {
		case st.Profile == agentstate.ProfileV1 && st.Schema == agentstate.SchemaVersion:
			v1StatusBy[spelling] = c.name
		case st.Profile == agentstate.ProfileCompat && declaredStatus(c.src) != spelling:
			aliasStatusBy[spelling] = c.name
		}
	}

	t.Run("members", func(t *testing.T) {
		for _, r := range agentstate.Rules() {
			if declaredBy[r.Member] == "" {
				t.Errorf("no case declares the member %q", r.Member)
			}
			if mistypedBy[r.Member] == "" {
				t.Errorf("no case holds a %q the contract does not admit", r.Member)
			}
			for _, name := range r.Compat {
				if declaredBy[name] == "" {
					t.Errorf("no case declares %q, the compatibility name for %q", name, r.Member)
				}
			}
		}
	})

	t.Run("statuses", func(t *testing.T) {
		for _, spelling := range agentstate.Vocabulary() {
			if v1StatusBy[spelling] == "" {
				t.Errorf("no versioned case declares the status %q", spelling)
			}
			if aliasStatusBy[spelling] == "" {
				t.Errorf("no compatibility case reaches the status %q through an alias", spelling)
			}
		}
	})
}

// declaredStatus returns the status a document spells, empty when it declares
// none or declares it as something other than a string.
func declaredStatus(src []byte) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(src, &raw); err != nil {
		return ""
	}
	var spelling string
	if err := json.Unmarshal(raw["status"], &spelling); err != nil {
		return ""
	}
	return spelling
}

// TestSchemaAgreesWithDecoder asserts the published schema accepts what the
// decoder accepts. A document the decoder reads without an error-severity
// diagnostic must satisfy the structural constraints [agentstate.SchemaJSON]
// declares, so an integrator who validates against the published schema and an
// integrator who runs agentfs reach the same verdict.
//
// The schema describes the versioned contract, so the check applies to
// documents that declare it. A document read under the compatibility profile
// is by construction one the schema does not describe.
//
// The check runs against the document with its null members elided, which is
// the document the decoder sees. That is the one place the two verdicts part,
// and [TestNullIsTheOnlyDivergence] holds it to that.
func TestSchemaAgreesWithDecoder(t *testing.T) {
	schema := loadSchema(t)
	checked := 0
	for _, c := range loadCases(t) {
		st, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())
		if hasError(ds) || st.Schema != agentstate.SchemaVersion {
			continue
		}
		checked++
		t.Run(c.name, func(t *testing.T) {
			for _, violation := range checkAgainstSchema(schema, elideNulls(parseObject(t, c.src))) {
				t.Errorf("the decoder accepts the document but the schema does not: %s", violation)
			}
		})
	}
	if checked < minimumSchemaChecked {
		t.Errorf("checked %d cases against the schema, want at least %d", checked, minimumSchemaChecked)
	}
}

// TestNullIsTheOnlyDivergence bounds what the elision in
// [TestSchemaAgreesWithDecoder] is allowed to hide. Every violation the schema
// reports against a document the decoder accepts as written must name a member
// that document declares as JSON null; a violation naming anything else is a
// disagreement the agreement test would report as agreement.
//
// A document a validator refuses and agentfs reads is an integrator holding two
// verdicts, so the set of documents in that position is a contract decision
// rather than an artifact of the checker.
func TestNullIsTheOnlyDivergence(t *testing.T) {
	schema := loadSchema(t)
	for _, c := range loadCases(t) {
		st, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())
		if hasError(ds) || st.Schema != agentstate.SchemaVersion {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			doc := parseObject(t, c.src)
			nulled := nulledMembers(doc)
			for _, violation := range checkAgainstSchema(schema, doc) {
				if !slices.ContainsFunc(nulled, func(name string) bool {
					return strings.Contains(violation, strconv.Quote(name))
				}) {
					t.Errorf("the decoder accepts the document and the schema does not, over no null member: %s", violation)
				}
			}
		})
	}
}

// TestFixedDocumentsSatisfySchema asserts every repaired document is a worked
// example an integrator can copy: it declares the contract version and
// satisfies the published schema.
func TestFixedDocumentsSatisfySchema(t *testing.T) {
	schema := loadSchema(t)
	for _, c := range loadCases(t) {
		src, err := os.ReadFile(c.fixedPath())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("case %s: %v", c.name, err)
		}
		t.Run(c.name, func(t *testing.T) {
			doc := parseObject(t, src)
			for _, violation := range checkAgainstSchema(schema, doc) {
				t.Errorf("the repaired document does not satisfy the schema: %s", violation)
			}
		})
	}
}

// parseObject reads a document's members, failing the test when the bytes are
// not a JSON object.
func parseObject(tb testing.TB, src []byte) map[string]json.RawMessage {
	tb.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(src, &doc); err != nil {
		tb.Fatalf("parse document: %v", err)
	}
	return doc
}

// valuesOf returns the offending value each diagnostic quotes, as
// "code=value", deduplicated and sorted. A diagnostic quoting nothing
// contributes nothing.
func valuesOf(ds []diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if d.Value != "" {
			out = append(out, string(d.Code)+"="+d.Value)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// quoted returns the runs of text a message quotes.
func quoted(message string) []string {
	var out []string
	rest := message
	for {
		i := strings.Index(rest, `"`)
		if i < 0 {
			return out
		}
		rest = rest[i+1:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j+1:]
	}
}

// TestAQuotedValueAgreesWithItsMessage asserts a diagnostic that carries both a
// message quoting text and a Value quotes the same text in both.
//
// The two are written at the same call site from the same variable, so they
// agree when the code is written and diverge silently afterwards: a call site
// that gains a second quoted run, or passes the member name where it meant the
// member's contents, produces a finding whose two halves name different things.
// A reader resolving the finding trusts whichever half they read first.
func TestAQuotedValueAgreesWithItsMessage(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.name, func(t *testing.T) {
			_, ds := agentstate.Decode(c.docPath(), c.src, decodeOptions())
			for _, d := range ds {
				// A message naming the member it is about quotes the member
				// name, which the pointer already carries. That run is not a
				// claim about the offending value and is not compared to one.
				member := d.Pointer[strings.LastIndex(d.Pointer, "/")+1:]
				runs := slices.DeleteFunc(quoted(d.Message), func(r string) bool { return r == member })
				if d.Value == "" || len(runs) == 0 {
					continue
				}
				// An abbreviated value keeps its head, so a long value agrees
				// with its message by prefix rather than by equality.
				if slices.ContainsFunc(runs, func(r string) bool {
					return r == d.Value || strings.HasPrefix(r, strings.TrimSuffix(d.Value, "\u2026"))
				}) {
					continue
				}
				t.Errorf("%s quotes %q as the offending value but its message quotes %q:\n%s",
					d.Code, d.Value, runs, d)
			}
		})
	}
}
