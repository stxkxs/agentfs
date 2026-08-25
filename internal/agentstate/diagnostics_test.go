package agentstate_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// minimumCodesRaised is the number of distinct codes the corpus must provoke.
// Without a floor the properties below would pass on a corpus that stopped
// reaching the decoder.
const minimumCodesRaised = 18

// memberValues holds, per member name, the values documents declare: the
// conforming form first, then the forms the decoder has to have an answer for.
var memberValues = map[string][]string{
	"schema":            {`"agentfs/v1"`, `"agentfs/v99"`, `11`, `null`, `""`},
	"status":            {`"running"`, `"done"`, `"in_progress"`, `"not running"`, `42`, `null`, `""`},
	"agent":             {`"researcher"`, `""`, `7`, `null`},
	"task":              {`"summarize the corpus"`, `""`, `{}`, `null`},
	"step":              {`3`, `"retrieval"`, `-1`, `""`, `{"phase":1}`, `null`},
	"steps_total":       {`9`, `-2`, `"many"`, `null`},
	"model":             {`"a-model"`, `false`, `null`},
	"run_id":            {`"2026-04-08T12-00-00"`, `[]`, `null`},
	"problem":           {`"upstream 503"`, `""`, `3.5`, `null`},
	"heartbeat_seconds": {`30`, `2.5`, `-1`, `"fast"`, `null`},
	"started_at":        {`"2026-04-08T12:00:00Z"`, `"2026-04-08T12:00:00"`, `"yesterday"`, `1775649600`, `null`},
	"updated_at":        {`"2026-04-08T12:59:00Z"`, `"2026-04-08T14:00:00Z"`, `"nope"`, `null`},
	"labels":            {`{"team":"core"}`, `{"attempt":2}`, `["core"]`, `null`},
	"error":             {`"upstream 503"`, `null`},
	"vendor_field":      {`{"a":1}`, `"a value that outgrows a small ceiling"`},
}

// document renders a state document declaring the given members over a
// conforming base, one member per line so every pointer resolves to a
// position.
func document(members map[string]string) string {
	merged := map[string]string{"schema": `"agentfs/v1"`, "status": `"running"`}
	maps.Copy(merged, members)

	var b strings.Builder
	b.WriteString("{\n")
	for i, name := range slices.Sorted(maps.Keys(merged)) {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "  %q: %s", name, merged[name])
	}
	b.WriteString("\n}")
	return b.String()
}

// corpus renders one document per member value, one declaring every member at
// its conforming value, and the documents that are not state at all.
func corpus() []string {
	out := []string{
		"",
		"{",
		`["running"]`,
		`{"schema":"agentfs/v1"}`,
		`{}`,
	}
	conforming := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(memberValues)) {
		values := memberValues[name]
		conforming[name] = values[0]
		for _, value := range values {
			out = append(out, document(map[string]string{name: value}))
		}
	}
	return append(out,
		document(conforming),
		document(map[string]string{"agent": `"` + strings.Repeat("n", 300) + `"`}),
		document(map[string]string{"step": "9", "steps_total": "3"}),
		document(map[string]string{"status": `"error"`}),
	)
}

// decodeCorpus decodes every document under every reading configuration and
// hands each diagnostic to check.
func decodeCorpus(t *testing.T, check func(t *testing.T, doc string, st agentstate.State, ds []diag.Diagnostic)) {
	t.Helper()
	observed := time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)
	configurations := []agentstate.Options{
		{},
		{Now: observed},
		{Now: observed, SkewTolerance: time.Nanosecond},
		{Now: observed, MaxExtraBytes: 4},
	}
	for _, doc := range corpus() {
		for _, opts := range configurations {
			for _, path := range []string{"agent-a/" + agentstate.StateFile, "agent-a/status.json"} {
				st, ds := agentstate.Decode(path, []byte(doc), opts)
				check(t, doc, st, ds)
			}
		}
	}
}

// A diagnostic is actionable when a consumer can branch on its code and a
// human can apply its hint. A code outside the registry is one no reference
// documents and no consumer can suppress; an error carrying no hint is a
// refusal with no repair, which leaves an integrator holding a rejected
// document and no next step.
func TestEveryDiagnosticCarriesARegisteredCodeAndAnActionableHint(t *testing.T) {
	t.Parallel()
	raised := map[diag.Code]bool{}

	decodeCorpus(t, func(t *testing.T, doc string, _ agentstate.State, ds []diag.Diagnostic) {
		for _, d := range ds {
			info, registered := diag.Lookup(d.Code)
			if !registered {
				t.Fatalf("the document\n%s\nraised the unregistered code %q", doc, d.Code)
			}
			raised[d.Code] = true

			if d.Severity != info.Severity {
				t.Errorf("%s was raised at %v, and the registry declares %v", d.Code, d.Severity, info.Severity)
			}
			if d.Message == "" {
				t.Errorf("%s names no condition, so its finding reads as a bare code", d.Code)
			}
			if d.Severity == diag.Error && d.Hint == "" {
				t.Errorf("%s refuses the document and names no edit that resolves it: %s", d.Code, d)
			}
		}
	})

	if len(raised) < minimumCodesRaised {
		t.Errorf("the corpus raised %d distinct codes, want at least %d", len(raised), minimumCodesRaised)
	}
}

// A state carrying no error-severity diagnostic is one a reader uses, so its
// status is a value from the vocabulary rather than the zero. A document that
// declares no readable status and raises nothing would render as unknown with
// nothing naming why, which is the guess the closed vocabulary exists to
// prevent.
func TestAStateWithNoErrorDeclaresAStatusInTheVocabulary(t *testing.T) {
	t.Parallel()
	decodeCorpus(t, func(t *testing.T, doc string, st agentstate.State, ds []diag.Diagnostic) {
		for _, d := range ds {
			if d.Severity == diag.Error {
				return
			}
		}
		if !slices.Contains(agentstate.Vocabulary(), st.Status.String()) {
			t.Errorf("the document\n%s\ndecoded to status %v with no error diagnostic: %v", doc, st.Status, codes(ds))
		}
	})
}
