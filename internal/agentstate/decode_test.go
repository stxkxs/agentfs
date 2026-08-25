package agentstate_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

func decode(t *testing.T, doc string) (agentstate.State, []diag.Diagnostic) {
	t.Helper()
	return agentstate.Decode("agent-a/state.json", []byte(doc), agentstate.Options{
		Now: time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC),
	})
}

func codes(ds []diag.Diagnostic) []diag.Code {
	out := make([]diag.Code, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func hasCode(ds []diag.Diagnostic, c diag.Code) bool {
	return slices.Contains(codes(ds), c)
}

// Substring matching made "not running" read as running and "unfailed" read as
// an error. Matching is exact against a closed vocabulary.
func TestStatusMatchIsExact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw   string
		want  agentstate.Status
		valid bool
	}{
		{"running", agentstate.StatusRunning, true},
		{"done", agentstate.StatusDone, true},
		{"RUNNING", agentstate.StatusRunning, true},
		{"  running  ", agentstate.StatusRunning, true},
		{"not running", agentstate.StatusUnknown, false},
		{"unfailed", agentstate.StatusUnknown, false},
		{"not-done", agentstate.StatusUnknown, false},
		{"errors_resolved", agentstate.StatusUnknown, false},
		{"runner", agentstate.StatusUnknown, false},
		{"", agentstate.StatusUnknown, false},
	}
	for _, tc := range cases {
		got, ok := agentstate.ParseStatus(tc.raw, agentstate.ProfileV1)
		if ok != tc.valid || got != tc.want {
			t.Errorf("ParseStatus(%q) = (%v,%v), want (%v,%v)", tc.raw, got, ok, tc.want, tc.valid)
		}
	}
}

func TestUnknownStatusIsADiagnosticNotAGuess(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":"not running"}`)
	if st.Status != agentstate.StatusUnknown {
		t.Fatalf("status = %v, want unknown", st.Status)
	}
	if !hasCode(ds, diag.CodeStatusUnknown) {
		t.Fatalf("codes = %v, want AFS3002", codes(ds))
	}
}

func TestAliasesResolveOnlyUnderTheCompatibilityProfile(t *testing.T) {
	t.Parallel()
	if _, ok := agentstate.ParseStatus("in_progress", agentstate.ProfileV1); ok {
		t.Error("v1 accepted a compatibility alias")
	}
	got, ok := agentstate.ParseStatus("in_progress", agentstate.ProfileCompat)
	if !ok || got != agentstate.StatusRunning {
		t.Errorf("compat profile resolved in_progress to (%v,%v)", got, ok)
	}
}

// encoding/json abandons a document at its first type error. A validator that
// reports one error per run is materially worse than one that reports them all.
func TestThreeBadMembersYieldThreeDiagnostics(t *testing.T) {
	t.Parallel()
	_, ds := decode(t, `{
	  "schema": "agentfs/v1",
	  "status": "running",
	  "task": 42,
	  "step": {"nested": true},
	  "updated_at": "not-a-time"
	}`)
	var wrongType, stepType, badTime int
	for _, d := range ds {
		switch d.Code {
		case diag.CodeWrongType:
			wrongType++
		case diag.CodeStepType:
			stepType++
		case diag.CodeTimeNotRFC3339:
			badTime++
		default:
		}
	}
	if wrongType != 1 || stepType != 1 || badTime != 1 {
		t.Fatalf("codes = %v, want one each of AFS2001, AFS2004, AFS3004", codes(ds))
	}
}

// Deriving status from the presence of an error field makes both of these
// states unrepresentable.
func TestStatusAndProblemAreIndependent(t *testing.T) {
	t.Parallel()

	done, ds := decode(t, `{"schema":"agentfs/v1","status":"done","problem":"retried twice"}`)
	if done.Status != agentstate.StatusDone {
		t.Errorf("done-with-a-problem resolved to %v", done.Status)
	}
	if done.Problem != "retried twice" {
		t.Errorf("problem = %q", done.Problem)
	}
	for _, d := range ds {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error diagnostic: %s", d)
		}
	}

	running, _ := decode(t, `{"schema":"agentfs/v1","status":"running","problem":"transient 503, retrying"}`)
	if running.Status != agentstate.StatusRunning {
		t.Errorf("running-after-a-fault resolved to %v", running.Status)
	}
}

func TestErrorStatusWithoutAProblemIsReported(t *testing.T) {
	t.Parallel()
	_, ds := decode(t, `{"schema":"agentfs/v1","status":"error"}`)
	if !hasCode(ds, diag.CodeErrorWithoutProblem) {
		t.Fatalf("codes = %v, want AFS3007", codes(ds))
	}
}

func TestMissingStatusIsDistinguishableFromMalformed(t *testing.T) {
	t.Parallel()

	_, missing := decode(t, `{"schema":"agentfs/v1","task":"research"}`)
	if !hasCode(missing, diag.CodeStatusMissing) {
		t.Errorf("missing status codes = %v, want AFS3001", codes(missing))
	}

	_, torn := decode(t, `{"schema":"agentfs/v1","status":"run`)
	if !hasCode(torn, diag.CodeNotJSON) {
		t.Errorf("torn document codes = %v, want AFS1001", codes(torn))
	}

	_, notObject := decode(t, `["running"]`)
	if !hasCode(notObject, diag.CodeNotObject) {
		t.Errorf("array document codes = %v, want AFS1002", codes(notObject))
	}
}

func TestStepIsATypedUnion(t *testing.T) {
	t.Parallel()

	ordinal, _ := decode(t, `{"schema":"agentfs/v1","status":"running","step":3}`)
	if n, ok := ordinal.Step.Ordinal(); !ok || n != 3 {
		t.Errorf("ordinal step = (%d,%v)", n, ok)
	}
	if ordinal.Step.String() != "3" {
		t.Errorf("ordinal renders as %q", ordinal.Step.String())
	}

	label, _ := decode(t, `{"schema":"agentfs/v1","status":"running","step":"retrieval"}`)
	if s, ok := label.Step.Label(); !ok || s != "retrieval" {
		t.Errorf("label step = (%q,%v)", s, ok)
	}

	_, ds := decode(t, `{"schema":"agentfs/v1","status":"running","step":-1}`)
	if !hasCode(ds, diag.CodeStepNegative) {
		t.Errorf("negative step codes = %v, want AFS2005", codes(ds))
	}
}

func TestStepRoundTrips(t *testing.T) {
	t.Parallel()
	for _, s := range []agentstate.Step{
		agentstate.OrdinalStep(0),
		agentstate.OrdinalStep(42),
		agentstate.LabelStep("retrieval"),
		agentstate.NoStep,
	} {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %v: %v", s, err)
		}
		doc := `{"schema":"agentfs/v1","status":"running","step":` + string(b) + `}`
		back, _ := decode(t, doc)
		if back.Step != s {
			t.Errorf("round trip of %v via %s gave %v", s, b, back.Step)
		}
	}
}

// A vocabulary type with an UnmarshalJSON aborts the whole document at its own
// failure, which is what the multi-pass decoder exists to avoid.
func TestVocabularyTypesDeclareNoUnmarshalJSON(t *testing.T) {
	t.Parallel()
	for _, typ := range []reflect.Type{
		reflect.TypeOf(agentstate.Status(0)),
		reflect.TypeOf(agentstate.Step{}),
	} {
		ptr := reflect.PointerTo(typ)
		if _, found := ptr.MethodByName("UnmarshalJSON"); found {
			t.Errorf("%s declares UnmarshalJSON", typ)
		}
	}
}

func TestTimestampWithoutAnOffsetIsRefused(t *testing.T) {
	t.Parallel()
	_, ds := decode(t, `{"schema":"agentfs/v1","status":"running","updated_at":"2026-04-08T13:00:00"}`)
	if !hasCode(ds, diag.CodeTimeNoOffset) {
		t.Fatalf("codes = %v, want AFS3005", codes(ds))
	}
}

func TestClockSkewWithinToleranceIsNotReported(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)
	doc := `{"schema":"agentfs/v1","status":"running","updated_at":"2026-04-08T13:00:03Z"}`

	_, within := agentstate.Decode("s.json", []byte(doc), agentstate.Options{Now: now, SkewTolerance: 5 * time.Second})
	if hasCode(within, diag.CodeTimeFuture) {
		t.Error("a timestamp inside the skew tolerance was reported as being from the future")
	}

	_, beyond := agentstate.Decode("s.json", []byte(doc), agentstate.Options{Now: now, SkewTolerance: time.Second})
	if !hasCode(beyond, diag.CodeTimeFuture) {
		t.Error("a timestamp beyond the skew tolerance was not reported")
	}
}

func TestDiagnosticsCarryPositions(t *testing.T) {
	t.Parallel()
	doc := "{\n  \"schema\": \"agentfs/v1\",\n  \"status\": \"nope\"\n}"
	_, ds := agentstate.Decode("agent-a/state.json", []byte(doc), agentstate.Options{})
	for _, d := range ds {
		if d.Code != diag.CodeStatusUnknown {
			continue
		}
		if d.Line != 3 {
			t.Errorf("line = %d, want 3 (%s)", d.Line, d)
		}
		if d.Pointer != "/status" {
			t.Errorf("pointer = %q, want /status", d.Pointer)
		}
		if d.Hint == "" {
			t.Error("diagnostic carries no hint")
		}
		return
	}
	t.Fatalf("no AFS3002 raised: %v", codes(ds))
}

func TestUndefinedMembersArePreserved(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":"running","vendor_field":{"a":1}}`)
	if _, ok := st.Extra["vendor_field"]; !ok {
		t.Fatalf("Extra = %v, want vendor_field preserved", st.Extra)
	}
	if !hasCode(ds, diag.CodeUnknownMember) {
		t.Errorf("codes = %v, want AFS1003", codes(ds))
	}
}

func TestLegacyFilenameIsReportedButRead(t *testing.T) {
	t.Parallel()
	st, ds := agentstate.Decode("agent-a/status.json", []byte(`{"status":"running"}`), agentstate.Options{})
	if st.Status != agentstate.StatusRunning {
		t.Fatalf("status = %v, want running", st.Status)
	}
	if !hasCode(ds, diag.CodeLegacyFilename) {
		t.Errorf("codes = %v, want AFS1007", codes(ds))
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(`{"schema":"agentfs/v1","status":"running"}`)
	f.Add(`{"status":"in_progress","step":"a"}`)
	f.Add(`{`)
	f.Add(`[]`)
	f.Add(`{"step":1e400}`)
	f.Add(`{"labels":{"a":1}}`)

	f.Fuzz(func(t *testing.T, doc string) {
		st, ds := agentstate.Decode("s.json", []byte(doc), agentstate.Options{Now: time.Unix(0, 0)})
		usable := true
		for _, d := range ds {
			if _, known := diag.Lookup(d.Code); !known {
				t.Fatalf("decode raised unregistered code %q", d.Code)
			}
			if d.Severity != diag.Error {
				continue
			}
			usable = false
			if d.Hint == "" {
				t.Fatalf("decode refused the document with %s and named no edit that resolves it", d.Code)
			}
		}
		if usable && !slices.Contains(agentstate.Vocabulary(), st.Status.String()) {
			t.Fatalf("a state with no error diagnostic declares status %v", st.Status)
		}
		if st.Status != agentstate.StatusUnknown && st.Status.String() == "unknown" {
			t.Fatalf("status %d renders as unknown", st.Status)
		}
	})
}
