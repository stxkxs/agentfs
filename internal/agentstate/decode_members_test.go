package agentstate_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// labelsDocument renders a conforming state document declaring labels.
func labelsDocument(t *testing.T, labels map[string]string) []byte {
	t.Helper()
	encoded, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("encoding labels: %v", err)
	}
	return []byte(`{"schema":"agentfs/v1","status":"running","labels":` + string(encoded) + `}`)
}

// pointers returns the JSON Pointers the diagnostics carrying code name.
func pointers(ds []diag.Diagnostic, code diag.Code) []string {
	var out []string
	for _, d := range ds {
		if d.Code == code {
			out = append(out, d.Pointer)
		}
	}
	return out
}

// hasCodeAt reports whether a diagnostic with code names pointer.
func hasCodeAt(ds []diag.Diagnostic, code diag.Code, pointer string) bool {
	for _, p := range pointers(ds, code) {
		if p == pointer {
			return true
		}
	}
	return false
}

// encoding/json reads null as a no-op for every scalar target, so without a
// guard a nulled member reads as a declared zero: a step at position zero, an
// epoch timestamp, a heartbeat of no seconds.
func TestNullMembersDeclareNothing(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{
	  "schema": "agentfs/v1",
	  "status": "running",
	  "agent": null,
	  "task": null,
	  "step": null,
	  "steps_total": null,
	  "heartbeat_seconds": null,
	  "started_at": null,
	  "updated_at": null,
	  "labels": null
	}`)
	for _, d := range ds {
		t.Errorf("unexpected diagnostic: %s", d)
	}
	if st.Agent != "" || st.Task != "" {
		t.Errorf("a nulled string member read as a value: agent %q task %q", st.Agent, st.Task)
	}
	if !st.Step.IsZero() {
		t.Errorf("a nulled step read as %v", st.Step)
	}
	if st.StepsTotal != 0 || st.Heartbeat != 0 {
		t.Errorf("a nulled number read as a value: steps_total %d heartbeat %v", st.StepsTotal, st.Heartbeat)
	}
	if !st.StartedAt.IsZero() || !st.UpdatedAt.IsZero() {
		t.Errorf("a nulled timestamp read as an instant: %v %v", st.StartedAt, st.UpdatedAt)
	}
	if st.Labels != nil {
		t.Errorf("nulled labels read as %v", st.Labels)
	}
}

// The status member is required, and a null member declares nothing. A
// document that nulls it holds no status, which is the same finding as one
// that omits it rather than a state that decodes clean and renders unknown.
func TestANullStatusIsUndeclared(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":null,"task":"research"}`)
	if st.Status != agentstate.StatusUnknown {
		t.Errorf("status = %v, want unknown", st.Status)
	}
	if !hasCode(ds, diag.CodeStatusMissing) {
		t.Errorf("codes = %v, want AFS3001", codes(ds))
	}
}

// An empty string is a declared member holding no value, which a reader cannot
// distinguish from an absent one. The document says which it meant.
func TestAnEmptyStringMemberIsReported(t *testing.T) {
	t.Parallel()
	_, ds := decode(t, `{"schema":"agentfs/v1","status":"running","agent":""}`)
	if !hasCodeAt(ds, diag.CodeEmptyString, "/agent") {
		t.Errorf("codes = %v, want AFS2002 at /agent", codes(ds))
	}
}

func TestAnEmptyStepLabelIsReported(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":"running","step":""}`)
	if !st.Step.IsZero() {
		t.Errorf("an empty step label read as %v", st.Step)
	}
	if !hasCodeAt(ds, diag.CodeEmptyString, "/step") {
		t.Errorf("codes = %v, want AFS2002 at /step", codes(ds))
	}
}

// A string member is bounded because the document is a file agentfs does not
// control. The value is reported and truncated rather than refused, so a
// document with an over-long member still yields the rest of its state.
func TestAStringMemberBeyondItsCeilingIsTruncatedAndReported(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", agentstate.MaxStringRunes+64)
	st, ds := decode(t, `{"schema":"agentfs/v1","status":"running","task":"`+long+`"}`)
	if !hasCodeAt(ds, diag.CodeStringTooLong, "/task") {
		t.Fatalf("codes = %v, want AFS2003 at /task", codes(ds))
	}
	if n := len([]rune(st.Task)); n > agentstate.MaxStringRunes {
		t.Errorf("task decoded to %d runes, past the %d-rune ceiling", n, agentstate.MaxStringRunes)
	}
	if st.Status != agentstate.StatusRunning {
		t.Errorf("status = %v, want the rest of the document to decode", st.Status)
	}
}

func TestStepsTotalIsANonNegativeInteger(t *testing.T) {
	t.Parallel()

	counted, ds := decode(t, `{"schema":"agentfs/v1","status":"running","steps_total":9}`)
	if counted.StepsTotal != 9 {
		t.Errorf("steps_total = %d, want 9", counted.StepsTotal)
	}
	for _, d := range ds {
		t.Errorf("unexpected diagnostic: %s", d)
	}

	worded, ds := decode(t, `{"schema":"agentfs/v1","status":"running","steps_total":"many"}`)
	if worded.StepsTotal != 0 {
		t.Errorf("a non-integer steps_total read as %d", worded.StepsTotal)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/steps_total") {
		t.Errorf("codes = %v, want AFS2001 at /steps_total", codes(ds))
	}

	negative, ds := decode(t, `{"schema":"agentfs/v1","status":"running","steps_total":-3}`)
	if negative.StepsTotal != 0 {
		t.Errorf("a negative steps_total read as %d", negative.StepsTotal)
	}
	if !hasCodeAt(ds, diag.CodeStepNegative, "/steps_total") {
		t.Errorf("codes = %v, want AFS2005 at /steps_total", codes(ds))
	}
}

// A heartbeat is what staleness is measured against, so a document that
// declares a value agentfs cannot measure with declares none.
func TestHeartbeatSecondsIsANonNegativeNumber(t *testing.T) {
	t.Parallel()

	worded, ds := decode(t, `{"schema":"agentfs/v1","status":"running","heartbeat_seconds":"fast"}`)
	if worded.Heartbeat != 0 {
		t.Errorf("a non-numeric heartbeat read as %v", worded.Heartbeat)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/heartbeat_seconds") {
		t.Errorf("codes = %v, want AFS2001 at /heartbeat_seconds", codes(ds))
	}

	negative, ds := decode(t, `{"schema":"agentfs/v1","status":"running","heartbeat_seconds":-1}`)
	if negative.Heartbeat != 0 {
		t.Errorf("a negative heartbeat read as %v", negative.Heartbeat)
	}
	if !hasCodeAt(ds, diag.CodeStepNegative, "/heartbeat_seconds") {
		t.Errorf("codes = %v, want AFS2005 at /heartbeat_seconds", codes(ds))
	}
}

func TestLabelsAreAnObjectOfStrings(t *testing.T) {
	t.Parallel()

	valid, ds := decode(t, `{"schema":"agentfs/v1","status":"running","labels":{"team":"core","tier":"1"}}`)
	if valid.Labels["team"] != "core" || valid.Labels["tier"] != "1" {
		t.Errorf("labels = %v, want team core and tier 1", valid.Labels)
	}
	for _, d := range ds {
		t.Errorf("unexpected diagnostic: %s", d)
	}

	numeric, ds := decode(t, `{"schema":"agentfs/v1","status":"running","labels":{"attempt":2}}`)
	if numeric.Labels != nil {
		t.Errorf("labels holding a number read as %v", numeric.Labels)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/labels") {
		t.Errorf("codes = %v, want AFS2001 at /labels", codes(ds))
	}

	listed, ds := decode(t, `{"schema":"agentfs/v1","status":"running","labels":["core"]}`)
	if listed.Labels != nil {
		t.Errorf("labels holding an array read as %v", listed.Labels)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/labels") {
		t.Errorf("codes = %v, want AFS2001 at /labels", codes(ds))
	}
}

// A timestamp names an instant, which an epoch number in a member declared as
// an RFC 3339 string does not.
func TestTimestampMembersAreStrings(t *testing.T) {
	t.Parallel()

	started, ds := decode(t, `{"schema":"agentfs/v1","status":"running","started_at":"2026-04-08T12:00:00Z"}`)
	if want := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC); !started.StartedAt.Equal(want) {
		t.Errorf("started_at = %v, want %v", started.StartedAt, want)
	}
	for _, d := range ds {
		t.Errorf("unexpected diagnostic: %s", d)
	}

	epoch, ds := decode(t, `{"schema":"agentfs/v1","status":"running","updated_at":1775649600}`)
	if !epoch.UpdatedAt.IsZero() {
		t.Errorf("a numeric timestamp read as %v", epoch.UpdatedAt)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/updated_at") {
		t.Errorf("codes = %v, want AFS2001 at /updated_at", codes(ds))
	}
}

// Workspaces on a shared mount are written by hosts whose clocks are not this
// one, so the tolerance applies without a caller configuring it.
func TestTheDefaultSkewToleranceApplies(t *testing.T) {
	t.Parallel()

	_, ahead := decode(t, `{"schema":"agentfs/v1","status":"running","updated_at":"2026-04-08T13:01:00Z"}`)
	if !hasCodeAt(ahead, diag.CodeTimeFuture, "/updated_at") {
		t.Errorf("codes = %v, want AFS3003 at /updated_at", codes(ahead))
	}

	_, within := decode(t, `{"schema":"agentfs/v1","status":"running","updated_at":"2026-04-08T13:00:01Z"}`)
	if hasCode(within, diag.CodeTimeFuture) {
		t.Errorf("codes = %v, want a timestamp inside the default tolerance to pass", codes(within))
	}
}

func TestAStepBeyondStepsTotalIsReported(t *testing.T) {
	t.Parallel()
	_, ds := decode(t, `{"schema":"agentfs/v1","status":"running","step":9,"steps_total":3}`)
	if !hasCodeAt(ds, diag.CodeStepBeyondTotal, "/step") {
		t.Errorf("codes = %v, want AFS3006 at /step", codes(ds))
	}

	_, within := decode(t, `{"schema":"agentfs/v1","status":"running","step":3,"steps_total":3}`)
	if hasCode(within, diag.CodeStepBeyondTotal) {
		t.Errorf("codes = %v, want the last step of a task to pass", codes(within))
	}
}

// A status that is not a string is a typing finding at /status, not a
// vocabulary one: the document never declared a spelling to match.
func TestAStatusOfTheWrongTypeIsTypedNotMatched(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v1","status":42}`)
	if st.Status != agentstate.StatusUnknown {
		t.Errorf("status = %v, want unknown", st.Status)
	}
	if !hasCodeAt(ds, diag.CodeWrongType, "/status") {
		t.Errorf("codes = %v, want AFS2001 at /status", codes(ds))
	}
	if hasCode(ds, diag.CodeStatusUnknown) {
		t.Errorf("codes = %v, want no vocabulary finding for a member that holds no spelling", codes(ds))
	}
}

// A schema member agentfs cannot read declares no version, so the document is
// read under the compatibility profile rather than refused for a version it
// never stated.
func TestASchemaMemberOfTheWrongTypeFallsBackToCompatibility(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":11,"status":"in_progress"}`)
	if !hasCodeAt(ds, diag.CodeWrongType, "/schema") {
		t.Fatalf("codes = %v, want AFS2001 at /schema", codes(ds))
	}
	if st.Profile != agentstate.ProfileCompat {
		t.Errorf("profile = %v, want compat", st.Profile)
	}
	if st.Status != agentstate.StatusRunning {
		t.Errorf("status = %v, want the compatibility alias to resolve", st.Status)
	}
}

// A version this build does not implement is refused rather than guessed at:
// the members of an unknown version are not this contract's members, so
// reading them would report findings against rules the document never claimed.
func TestAnUnimplementedSchemaVersionStopsTheDecode(t *testing.T) {
	t.Parallel()
	st, ds := decode(t, `{"schema":"agentfs/v99","status":"running","task":"research"}`)
	if !hasCodeAt(ds, diag.CodeSchemaUnknown, "/schema") {
		t.Fatalf("codes = %v, want AFS1005 at /schema", codes(ds))
	}
	if st.Schema != "agentfs/v99" {
		t.Errorf("schema = %q, want the version the document declared", st.Schema)
	}
	if st.Status != agentstate.StatusUnknown || st.Task != "" {
		t.Errorf("members of an unimplemented version were read: status %v task %q", st.Status, st.Task)
	}
}

// A compatibility member name is read under the compatibility profile and
// named as undefined under the versioned contract, so a document that declares
// v1 is held to v1's member names.
func TestCompatibilityMemberNamesAreReadOnlyUnderTheCompatibilityProfile(t *testing.T) {
	t.Parallel()

	compat, ds := decode(t, `{"status":"running","error":"upstream 503"}`)
	if compat.Problem != "upstream 503" {
		t.Errorf("problem = %q, want the compatibility member to be read", compat.Problem)
	}
	if !hasCodeAt(ds, diag.CodeUnknownMember, "/error") {
		t.Errorf("codes = %v, want AFS1003 at /error", codes(ds))
	}

	versioned, ds := decode(t, `{"schema":"agentfs/v1","status":"running","error":"upstream 503"}`)
	if versioned.Problem != "" {
		t.Errorf("problem = %q, want a compatibility name to declare nothing under v1", versioned.Problem)
	}
	if !hasCodeAt(ds, diag.CodeUnknownMember, "/error") {
		t.Errorf("codes = %v, want AFS1003 at /error", codes(ds))
	}
}

// Undefined members are preserved so a round trip does not discard an
// integrator's own fields, and the ceiling is what keeps that from making the
// document unbounded storage.
func TestPreservedMembersStopAtTheCeiling(t *testing.T) {
	t.Parallel()
	doc := []byte(`{"schema":"agentfs/v1","status":"running","vendor_field":"` + strings.Repeat("x", 64) + `"}`)

	kept, ds := agentstate.Decode("a/state.json", doc, agentstate.Options{})
	if _, ok := kept.Extra["vendor_field"]; !ok {
		t.Errorf("Extra = %v, want the undefined member preserved", kept.Extra)
	}
	if hasCode(ds, diag.CodeExtraTooLarge) {
		t.Errorf("codes = %v, want no ceiling finding under the default ceiling", codes(ds))
	}

	shed, ds := agentstate.Decode("a/state.json", doc, agentstate.Options{MaxExtraBytes: 8})
	if _, ok := shed.Extra["vendor_field"]; ok {
		t.Errorf("Extra = %v, want the member past the ceiling shed", shed.Extra)
	}
	if !hasCodeAt(ds, diag.CodeExtraTooLarge, "/vendor_field") {
		t.Errorf("codes = %v, want AFS1006 at /vendor_field", codes(ds))
	}

	// A member is held under its name, so the ceiling charges the name with the
	// value. Charging the value alone leaves a document that carries its bulk
	// in its member names retaining that bulk with the ceiling unreached, and
	// the ceiling is what makes unbounded retention impossible.
	var b strings.Builder
	b.WriteString(`{"schema":"agentfs/v1","status":"running"`)
	for i := range 8 {
		fmt.Fprintf(&b, `,%q:1`, strings.Repeat("n", 16<<10)+string(rune('a'+i)))
	}
	b.WriteByte('}')

	named, ds := agentstate.Decode("a/state.json", []byte(b.String()), agentstate.Options{})
	if !hasCode(ds, diag.CodeExtraTooLarge) {
		t.Errorf("codes = %v, want AFS1006 for members carrying their bulk in their names", codes(ds))
	}
	if held := retained(named); held > agentstate.DefaultMaxExtraBytes {
		t.Errorf("the reading retained %d bytes of undefined members against a ceiling of %d",
			held, agentstate.DefaultMaxExtraBytes)
	}
}

// retained is what a decoded state holds for the members the contract does not
// define: each member's name together with its value.
func retained(st agentstate.State) int {
	total := 0
	for name, value := range st.Extra {
		total += len(name) + len(value)
	}
	return total
}

// A finding about a document that is not JSON has no member to point at, so it
// carries the document's opening line as evidence. Output renders one
// diagnostic per line, which a value carrying the rest of the document would
// break.
func TestMalformedDocumentEvidenceIsBoundedToOneLine(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{
		"{\n  \"schema\": \"agentfs/v1\",\n  \"status\": \"run",
		"[\n  \"running\"\n]",
	} {
		_, ds := decode(t, doc)
		if len(ds) == 0 {
			t.Fatalf("document %q raised no diagnostic", doc)
		}
		for _, d := range ds {
			if d.Value == "" {
				t.Errorf("%s carries no evidence", d.Code)
			}
			if strings.ContainsAny(d.Value, "\n\r") {
				t.Errorf("%s carries evidence spanning lines: %q", d.Code, d.Value)
			}
		}
	}
}

// Labels are integrator-defined, so their size is chosen by the document rather
// than by agentfs. Without a ceiling a state document hands a reader an
// unbounded map to hold and an unbounded string to render — the one member of
// the contract with no shape of its own to constrain it.
func TestLabelsAreBounded(t *testing.T) {
	t.Parallel()

	t.Run("member count", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString(`{"schema":"agentfs/v1","status":"running","labels":{`)
		for i := range agentstate.MaxLabels * 4 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"k%d":"v"`, i)
		}
		b.WriteString(`}}`)

		st, ds := agentstate.Decode("a/state.json", []byte(b.String()), agentstate.Options{})
		if len(st.Labels) > agentstate.MaxLabels {
			t.Errorf("decoded %d labels, past the ceiling of %d", len(st.Labels), agentstate.MaxLabels)
		}
		if !hasCode(ds, diag.CodeStringTooLong) {
			t.Errorf("a labels object past its ceiling raised %v", codes(ds))
		}
	})

	// The ceiling applies to the name and to the value. One document over-long
	// in both halves cannot tell which half the decoder measured, because the
	// guard shortens the pair whichever half it reads. Each half therefore gets
	// a document over-long in it alone, whose conforming half comes through
	// untouched.
	long := strings.Repeat("x", agentstate.MaxLabelRunes*4)

	t.Run("name length", func(t *testing.T) {
		t.Parallel()
		st, ds := agentstate.Decode("a/state.json", labelsDocument(t, map[string]string{long: "v"}), agentstate.Options{})

		for name, value := range st.Labels {
			if n := utf8.RuneCountInString(name); n > agentstate.MaxLabelRunes {
				t.Errorf("a label name of %d runes survived", n)
			}
			if value != "v" {
				t.Errorf("the label's value decoded as %q, want the value the document declared", value)
			}
		}
		if !hasCode(ds, diag.CodeStringTooLong) {
			t.Errorf("a label name of %d runes raised %v", utf8.RuneCountInString(long), codes(ds))
		}
	})

	t.Run("value length", func(t *testing.T) {
		t.Parallel()
		st, ds := agentstate.Decode("a/state.json", labelsDocument(t, map[string]string{"k": long}), agentstate.Options{})

		if _, ok := st.Labels["k"]; !ok {
			t.Errorf("labels = %v, want the name the document declared", slices.Sorted(maps.Keys(st.Labels)))
		}
		for _, value := range st.Labels {
			if n := utf8.RuneCountInString(value); n > agentstate.MaxLabelRunes {
				t.Errorf("a label value of %d runes survived", n)
			}
		}
		if !hasCodeAt(ds, diag.CodeStringTooLong, "/labels/k") {
			t.Errorf("a label value of %d runes raised %v", utf8.RuneCountInString(long), codes(ds))
		}
	})
}

// Decoding is a function of the document's bytes. A map read in place hands its
// members out in an order the runtime varies, which a reader sees as findings
// that reorder between readings of one file and as a label that is present in
// one reading and gone from the next.
func TestLabelsAreReadInNameOrder(t *testing.T) {
	t.Parallel()

	t.Run("findings follow the names", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("v", agentstate.MaxLabelRunes*2)
		doc := labelsDocument(t, map[string]string{"charlie": long, "alpha": long, "bravo": long})
		want := []string{"/labels/alpha", "/labels/bravo", "/labels/charlie"}

		for range 200 {
			_, ds := agentstate.Decode("a/state.json", doc, agentstate.Options{})
			if got := pointers(ds, diag.CodeStringTooLong); !slices.Equal(got, want) {
				t.Fatalf("AFS2003 named %v, want %v", got, want)
			}
		}
	})

	t.Run("one document decodes to one set of labels", func(t *testing.T) {
		t.Parallel()
		// Two names that differ past the ceiling shorten to one name, so the
		// reading order decides which value the shortened name carries.
		head := strings.Repeat("x", agentstate.MaxLabelRunes*2)
		doc := labelsDocument(t, map[string]string{head + "A": "one", head + "B": "two"})

		first, _ := agentstate.Decode("a/state.json", doc, agentstate.Options{})
		for range 200 {
			st, _ := agentstate.Decode("a/state.json", doc, agentstate.Options{})
			if !maps.Equal(st.Labels, first.Labels) {
				t.Fatalf("one document decoded to %v and to %v", first.Labels, st.Labels)
			}
		}
	})
}

// A label within the ceiling is read under the name the document gave it.
// Shortening an over-long name produces a name the object may already carry,
// and the label that loses that collision is the over-long one: a conforming
// label is not overwritten by one the contract refuses, and no label leaves the
// reading without a finding naming it.
func TestAConformingLabelIsNotDisplacedByShortening(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("x", agentstate.MaxLabelRunes-1)
	conforming := head + "…" // the name an over-long name of this shape shortens to
	oversize := head + "LONGTAILLONGTAIL"

	st, ds := agentstate.Decode("a/state.json",
		labelsDocument(t, map[string]string{conforming: "kept", oversize: "shed"}), agentstate.Options{})

	if got := st.Labels[conforming]; got != "kept" {
		t.Errorf("the conforming label carries %q, want the value the document declared", got)
	}
	if len(st.Labels) != 1 {
		t.Errorf("labels = %v, want the conforming label alone", st.Labels)
	}
	named := pointers(ds, diag.CodeStringTooLong)
	if len(named) != 1 || !strings.HasPrefix(named[0], "/labels/"+strings.Repeat("x", 100)) {
		t.Errorf("AFS2003 named %v, want the label that was not read", named)
	}
}

// A pointer is an RFC 6901 JSON Pointer and a member name is chosen by the
// document. A name carrying a slash spliced in raw names a path through members
// no document has; a name carrying a tilde escape names the sibling that escape
// decodes to. Either sends a reader to a position that is not the member the
// finding is about, which is what the resolved line reports.
func TestAPointerResolvesToTheMemberItNames(t *testing.T) {
	t.Parallel()

	doc := "{\n" +
		`  "schema": "agentfs/v1",` + "\n" +
		`  "status": "running",` + "\n" +
		`  "a/b": 1,` + "\n" +
		`  "c~d": 2,` + "\n" +
		`  "c~0d": 3,` + "\n" +
		`  "labels": {` + "\n" +
		`    "e/f": "` + strings.Repeat("v", agentstate.MaxLabelRunes*2) + `"` + "\n" +
		"  }\n" +
		"}"

	_, ds := agentstate.Decode("a/state.json", []byte(doc), agentstate.Options{})

	lines := map[diag.Code]map[string]int{}
	for _, d := range ds {
		if lines[d.Code] == nil {
			lines[d.Code] = map[string]int{}
		}
		lines[d.Code][d.Pointer] = d.Line
	}
	for code, want := range map[diag.Code]map[string]int{
		diag.CodeUnknownMember: {"/a~1b": 4, "/c~0d": 5, "/c~00d": 6},
		diag.CodeStringTooLong: {"/labels/e~1f": 8},
	} {
		if got := lines[code]; !maps.Equal(got, want) {
			t.Errorf("%s named %v, want %v", code, got, want)
		}
	}
}

// A diagnostic quotes the workspace back, so its length is chosen by the
// document. Every member it carries is bounded, or a member name of two hundred
// thousand runes produces a two-hundred-kilobyte finding on one line.
func TestEveryDiagnosticMemberIsBounded(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("m", 200_000)
	doc := `{"schema":"agentfs/v1","status":"running","` + huge + `":1}`

	_, ds := agentstate.Decode("a/state.json", []byte(doc), agentstate.Options{})
	if len(ds) == 0 {
		t.Fatal("an undefined member of two hundred thousand runes raised nothing")
	}
	for _, d := range ds {
		if n := utf8.RuneCountInString(d.Message); n > diag.MessageRunes {
			t.Errorf("%s carries a message of %d runes, past the ceiling of %d",
				d.Code, n, diag.MessageRunes)
		}
		if n := utf8.RuneCountInString(d.Pointer); n > 200 {
			t.Errorf("%s carries a pointer of %d runes", d.Code, n)
		}
		if n := utf8.RuneCountInString(d.Value); n > 120 {
			t.Errorf("%s carries a value of %d runes", d.Code, n)
		}
	}
}
