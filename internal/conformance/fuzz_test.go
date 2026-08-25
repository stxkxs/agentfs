package conformance_test

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// FuzzDecode asserts the properties that hold for every byte string a
// workspace can present, not only the ones the corpus names: the decoder
// returns rather than panics, every code it raises is registered, every
// pointer it reports is an RFC 6901 pointer, and two reads of the same bytes
// report the same thing. A consumer branches on code and pointer, so a code
// outside the registry or a pointer that names nothing is unusable.
//
// The corpus seeds it, so the documents the suite already reasons about are
// the starting point for mutation.
func FuzzDecode(f *testing.F) {
	for _, c := range loadCases(f) {
		f.Add(c.document, c.src)
	}
	f.Fuzz(func(t *testing.T, document string, src []byte) {
		docPath := path.Join(casesDir, "fuzz", document)
		state, ds := agentstate.Decode(docPath, src, decodeOptions())

		for _, d := range ds {
			if _, registered := diag.Lookup(d.Code); !registered {
				t.Fatalf("unregistered code %q: %s", d.Code, d)
			}
			if d.Pointer != "" && !strings.HasPrefix(d.Pointer, "/") {
				t.Fatalf("pointer %q is not an RFC 6901 pointer: %s", d.Pointer, d)
			}
			if d.Line < 0 || d.Column < 0 {
				t.Fatalf("position %d:%d is not a document position: %s", d.Line, d.Column, d)
			}
		}

		again, dsAgain := agentstate.Decode(docPath, src, decodeOptions())
		if !slices.Equal(codesOf(ds), codesOf(dsAgain)) {
			t.Fatalf("codes differ between reads: %v then %v", codesOf(ds), codesOf(dsAgain))
		}
		if state.Status != again.Status {
			t.Fatalf("status differs between reads: %q then %q", state.Status, again.Status)
		}
	})
}

// FuzzCheckAgainstSchema asserts the schema checker survives any document a
// workspace can present: it returns for every input, names a member in every
// violation it reports, and reports the same violations on a second pass. The
// checker is what [TestSchemaAgreesWithDecoder] trusts, so a checker that
// panicked or reported an empty sentence would take the agreement claim with
// it.
func FuzzCheckAgainstSchema(f *testing.F) {
	for _, c := range loadCases(f) {
		f.Add(c.src)
	}
	schema := loadSchema(f)
	f.Fuzz(func(t *testing.T, src []byte) {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(src, &doc); err != nil {
			return
		}
		violations := checkAgainstSchema(schema, doc)
		for _, v := range violations {
			if strings.TrimSpace(v) == "" {
				t.Fatal("a violation names nothing")
			}
		}
		if again := checkAgainstSchema(schema, doc); !slices.Equal(violations, again) {
			t.Fatalf("violations differ between passes: %v then %v", violations, again)
		}
	})
}
