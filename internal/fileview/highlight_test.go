package fileview

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

// allKinds is every content type, so a property asserted over kinds cannot
// silently skip one.
var allKinds = []Kind{KindPlain, KindJSON, KindYAML, KindNDJSON, KindLog, KindBinary}

// renderSpans writes each span as role:text, which is the form the tables below
// declare their expectations in.
func renderSpans(line string, spans []Span) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Role.String()+":"+line[s.Start:s.End])
	}
	return out
}

// checkSpans asserts the guarantee Highlight documents: ascending,
// non-overlapping, in bounds, never inside a rune, and never more numerous
// than the ceiling.
func checkSpans(t *testing.T, kind Kind, line string, spans []Span) {
	t.Helper()
	if len(spans) > MaxSpans {
		t.Fatalf("%v %q yielded %d spans, want at most %d", kind, line, len(spans), MaxSpans)
	}
	prev := 0
	for i, s := range spans {
		switch {
		case s.Start < prev:
			t.Fatalf("%v %q span %d %+v starts before %d", kind, line, i, s, prev)
		case s.End <= s.Start:
			t.Fatalf("%v %q span %d %+v is empty or reversed", kind, line, i, s)
		case s.End > len(line):
			t.Fatalf("%v %q span %d %+v reaches past %d", kind, line, i, s, len(line))
		case !utf8.RuneStart(line[s.Start]):
			t.Fatalf("%v %q span %d %+v starts inside a rune", kind, line, i, s)
		case s.End < len(line) && !utf8.RuneStart(line[s.End]):
			t.Fatalf("%v %q span %d %+v ends inside a rune", kind, line, i, s)
		}
		prev = s.End
	}
}

func runHighlightTable(t *testing.T, kind Kind, cases []struct {
	line string
	want []string
},
) {
	t.Helper()
	for _, tc := range cases {
		spans := Highlight(kind, tc.line)
		checkSpans(t, kind, tc.line, spans)
		got := renderSpans(tc.line, spans)
		if !equalStrings(got, tc.want) {
			t.Errorf("Highlight(%v, %q) = %q, want %q", kind, tc.line, got, tc.want)
		}
	}
}

func TestHighlightJSON(t *testing.T) {
	t.Parallel()
	runHighlightTable(t, KindJSON, []struct {
		line string
		want []string
	}{
		{`{"status": "running"}`, []string{`punct:{`, `key:"status"`, `punct::`, `string:"running"`, `punct:}`}},
		{`  "steps_total": 12,`, []string{`key:"steps_total"`, `punct::`, `number:12`, `punct:,`}},
		{`{"ok": true, "off": false, "none": null}`, []string{
			`punct:{`, `key:"ok"`, `punct::`, `bool:true`, `punct:,`,
			`key:"off"`, `punct::`, `bool:false`, `punct:,`,
			`key:"none"`, `punct::`, `null:null`, `punct:}`,
		}},
		{`[-1.5e+10, 0]`, []string{`punct:[`, `number:-1.5e+10`, `punct:,`, `number:0`, `punct:]`}},
		{`{"a\"b": 1}`, []string{`punct:{`, `key:"a\"b"`, `punct::`, `number:1`, `punct:}`}},
		{`{`, []string{`punct:{`}},
		{`   `, nil},
		{"", nil},
		{`{"status": "runn`, []string{`plain:{"status": "runn`}},
		{`oops`, []string{`plain:oops`}},
		{`{"a": 1x}`, []string{`plain:{"a": 1x}`}},
		{`{"a": -}`, []string{`plain:{"a": -}`}},
		{`{"a": 1.}`, []string{`plain:{"a": 1.}`}},
		{`{"a": 1e}`, []string{`plain:{"a": 1e}`}},
		{`{"a": "b\`, []string{`plain:{"a": "b\`}},
	})
}

func TestHighlightNDJSONUsesTheJSONLexer(t *testing.T) {
	t.Parallel()
	const line = `{"event": "step", "n": 4}`
	if got, want := renderSpans(line, Highlight(KindNDJSON, line)), renderSpans(line, Highlight(KindJSON, line)); !equalStrings(got, want) {
		t.Errorf("ndjson spans = %q, want the json spans %q", got, want)
	}
}

func TestHighlightYAML(t *testing.T) {
	t.Parallel()
	runHighlightTable(t, KindYAML, []struct {
		line string
		want []string
	}{
		{"status: running", []string{"key:status", "punct::"}},
		{"count: 42", []string{"key:count", "punct::", "number:42"}},
		{"drift: -3.5", []string{"key:drift", "punct::", "number:-3.5"}},
		{"ok: true", []string{"key:ok", "punct::", "bool:true"}},
		{"ok: YES", []string{"key:ok", "punct::", "bool:YES"}},
		{"none: null", []string{"key:none", "punct::", "null:null"}},
		{"none: ~", []string{"key:none", "punct::", "null:~"}},
		{`name: "agent one"`, []string{"key:name", "punct::", `string:"agent one"`}},
		{"name: 'agent one'", []string{"key:name", "punct::", "string:'agent one'"}},
		{"  indented: on", []string{"key:indented", "punct::", "bool:on"}},
		{"- item", []string{"punct:-"}},
		{"- name: a", []string{"punct:-", "key:name", "punct::"}},
		{"- - deep", []string{"punct:-", "punct:-"}},
		{"list: [1, 2]", []string{"key:list", "punct::", "punct:[", "number:1", "punct:,", "number:2", "punct:]"}},
		{"---", []string{"punct:---"}},
		{"...", []string{"punct:..."}},
		{"# a comment", nil},
		{"key: value # trailing", []string{"key:key", "punct::"}},
		{"just some text", nil},
		{"path: /tmp/agentfs", []string{"key:path", "punct::"}},
		{`"quoted key": 1`, []string{`key:"quoted key"`, "punct::", "number:1"}},
		{"key:", []string{"key:key", "punct::"}},
		{": orphan", nil},
		{"-", []string{"punct:-"}},
		{"   ", nil},
		{`unterminated: "open`, []string{"key:unterminated", "punct::", `string:"open`}},
	})
}

func TestHighlightLog(t *testing.T) {
	t.Parallel()
	runHighlightTable(t, KindLog, []struct {
		line string
		want []string
	}{
		{"2026-08-24T10:00:00Z INFO starting", []string{"number:2026-08-24T10:00:00Z", "info:INFO"}},
		{"10:00:00 WARN retrying", []string{"number:10:00:00", "warn:WARN"}},
		{"[ERROR] the run failed", []string{"error:ERROR"}},
		{"debug detail", []string{"debug:debug"}},
		{"TRACE entering", []string{"trace:TRACE"}},
		{"FATAL giving up", []string{"error:FATAL"}},
		{`level=info msg="all good" count=3`, []string{"key:level", "info:info", "key:msg", `string:"all good"`, "key:count", "number:3"}},
		{"enabled=true other=nil", []string{"key:enabled", "bool:true", "key:other", "null:nil"}},
		{"plain message here", nil},
		{`msg="unfinished`, []string{"key:msg", `string:"unfinished`}},
		{"took 15ms", []string{"number:15ms"}},
		{"step: 3", []string{"number:3"}},
		{"", nil},
	})
}

func TestHighlightPlainAndBinaryCarryNoSpans(t *testing.T) {
	t.Parallel()
	const line = `{"status": "running"} INFO 2026-08-24T10:00:00Z`
	for _, kind := range []Kind{KindPlain, KindBinary, Kind(99)} {
		if spans := Highlight(kind, line); spans != nil {
			t.Errorf("Highlight(%v, ...) = %v, want none", kind, spans)
		}
	}
}

func TestHighlightSpansAreValidForEveryKind(t *testing.T) {
	t.Parallel()
	corpus := []string{
		"",
		"   ",
		`{"status": "running", "step": 3, "done": null}`,
		`{"unterminated": "value`,
		"status: running\t# note",
		"- - - deep",
		"2026-08-24T10:00:00Z ERROR héllo wörld",
		"héllo: wörld",
		`msg="héllo wörld" level=warn`,
		"日本語のログ INFO です",
		strings.Repeat("=", 200),
		"key: 'ünterminated",
		"---",
		"ééé",
	}
	for _, kind := range allKinds {
		for _, line := range corpus {
			checkSpans(t, kind, line, Highlight(kind, line))
		}
	}
}

func TestClampDropsInvalidSpans(t *testing.T) {
	t.Parallel()
	const line = "héllo"
	// h occupies byte 0, e-acute bytes 1 and 2, then l, l, o.
	spans := []Span{
		{Start: 0, End: 1, Role: RoleKey},
		{Start: 0, End: 2, Role: RoleString}, // starts before the previous end
		{Start: 2, End: 2, Role: RolePunct},  // empty
		{Start: 2, End: 4, Role: RoleNumber}, // starts inside a rune
		{Start: 1, End: 2, Role: RoleBool},   // ends inside a rune
		{Start: 1, End: 99, Role: RoleNull},  // past the end
	}
	if got := clamp(line, spans); len(got) != 1 || got[0].Role != RoleKey {
		t.Errorf("clamp = %+v, want only the first span", got)
	}
	if got := clamp(line, []Span{{Start: 4, End: 2}}); got != nil {
		t.Errorf("clamp = %+v, want none", got)
	}
}

func TestSanitizedLinesKeepSpansInBounds(t *testing.T) {
	t.Parallel()
	raw := "status: \x1b]52;c;YWdlbnRmcw==\x07\"value\"\x00"
	line := textx.Sanitize(raw)
	for _, kind := range allKinds {
		checkSpans(t, kind, line, Highlight(kind, line))
	}
}

func TestHighlightHoldsTheSpanCeiling(t *testing.T) {
	t.Parallel()
	// A line of pure punctuation is the shape that yields a span per byte.
	for _, tc := range []struct {
		kind Kind
		line string
	}{
		{KindJSON, strings.Repeat("{", MaxSpans*4)},
		{KindNDJSON, strings.Repeat("[", MaxSpans*4)},
		{KindYAML, "k: " + strings.Repeat("[", MaxSpans*4)},
		{KindLog, strings.Repeat("1 ", MaxSpans*4)},
	} {
		spans := Highlight(tc.kind, tc.line)
		checkSpans(t, tc.kind, tc.line, spans)
		if len(spans) != MaxSpans {
			t.Errorf("Highlight(%v, %d punctuation bytes) = %d spans, want the ceiling %d",
				tc.kind, len(tc.line), len(spans), MaxSpans)
		}
	}
}
