package diag_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/diag"
)

func TestEveryRegisteredCodeIsWellFormed(t *testing.T) {
	t.Parallel()
	codes := diag.Codes()
	if len(codes) == 0 {
		t.Fatal("the registry is empty")
	}
	seen := map[diag.Code]bool{}
	for _, info := range codes {
		if seen[info.Code] {
			t.Errorf("code %s is registered twice", info.Code)
		}
		seen[info.Code] = true
		if !strings.HasPrefix(string(info.Code), "AFS") {
			t.Errorf("code %s does not carry the AFS prefix", info.Code)
		}
		if info.Summary == "" {
			t.Errorf("code %s has no summary, so it cannot be documented", info.Code)
		}
		if !strings.HasSuffix(info.Summary, ".") {
			t.Errorf("code %s summary is not a sentence: %q", info.Code, info.Summary)
		}
	}
}

func TestCodesAreSorted(t *testing.T) {
	t.Parallel()
	codes := diag.Codes()
	for i := 1; i < len(codes); i++ {
		if codes[i-1].Code >= codes[i].Code {
			t.Fatalf("Codes is not ascending at %d: %s then %s", i, codes[i-1].Code, codes[i].Code)
		}
	}
}

func TestLocateResolvesPointersToPositions(t *testing.T) {
	t.Parallel()
	src := []byte("{\n  \"schema\": \"agentfs/v1\",\n  \"status\": \"running\",\n  \"labels\": {\n    \"team\": \"core\"\n  }\n}")
	cases := []struct {
		pointer   string
		line, col int
	}{
		{"", 1, 1},
		{"/schema", 2, 13},
		{"/status", 3, 13},
		{"/labels", 4, 13},
		{"/labels/team", 5, 13},
		{"/missing", 0, 0},
		{"not-a-pointer", 0, 0},
	}
	for _, tc := range cases {
		line, col := diag.Locate(src, tc.pointer)
		if line != tc.line || col != tc.col {
			t.Errorf("Locate(%q) = (%d,%d), want (%d,%d)", tc.pointer, line, col, tc.line, tc.col)
		}
	}
}

func TestLocateHandlesArraysAndEscapes(t *testing.T) {
	t.Parallel()
	src := []byte(`{"a/b": 1, "list": [10, 20]}`)
	if line, _ := diag.Locate(src, "/a~1b"); line != 1 {
		t.Errorf("an escaped pointer did not resolve")
	}
	if line, col := diag.Locate(src, "/list/1"); line != 1 || col == 0 {
		t.Errorf("an array index did not resolve: (%d,%d)", line, col)
	}
}

// Two member names of one object that differ only in what a pointer escapes
// are two members, and a finding about either resolves to that member's own
// position. A name spliced into a pointer unescaped reads as a path through
// members the document does not have, or as the sibling its escape decodes to.
func TestAPointerBuiltFromAMemberNameResolvesToThatMember(t *testing.T) {
	t.Parallel()
	src := []byte("{\n  \"a/b\": 1,\n  \"c~d\": 2,\n  \"c~0d\": 3\n}")
	for name, line := range map[string]int{"a/b": 2, "c~d": 3, "c~0d": 4} {
		pointer := "/" + diag.EscapeToken(name)
		got, col := diag.Locate(src, pointer)
		if got != line || col == 0 {
			t.Errorf("Locate(%q) = (%d,%d), want line %d for the member %q", pointer, got, col, line, name)
		}
	}
}

func TestLocateReportsNoPositionForAnUnresolvableMember(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, pointer string }{
		{`{"a":`, "/b"}, // the member is not present
		{`not json at all`, "/a"},
		{`{"a":1}`, "/a/b"}, // the member is a scalar, so it has no children
		{`[1,2]`, "/9"},     // the index is past the end
	}
	for _, tc := range cases {
		if line, col := diag.Locate([]byte(tc.src), tc.pointer); line != 0 || col != 0 {
			t.Errorf("Locate(%q,%q) = (%d,%d), want no position", tc.src, tc.pointer, line, col)
		}
	}
}

// A member whose key is present resolves even when its value was truncated
// mid-write, which is what points a reader at the right line of a torn
// document.
func TestLocateResolvesAKeyWhoseValueIsTruncated(t *testing.T) {
	t.Parallel()
	line, col := diag.Locate([]byte(`{"a":`), "/a")
	if line != 1 || col == 0 {
		t.Fatalf("Locate on a truncated value = (%d,%d), want a position on line 1", line, col)
	}
}

func TestSinkRecordsSeverityFromTheRegistry(t *testing.T) {
	t.Parallel()
	s := diag.NewSink("a/state.json", []byte(`{"status":"nope"}`))
	s.Add(diag.CodeStatusUnknown, "/status", "unknown status", "use a vocabulary value", "nope")
	s.Add(diag.CodeUnknownMember, "/x", "undefined member", "", "")

	ds := s.Diagnostics()
	if len(ds) != 2 {
		t.Fatalf("recorded %d diagnostics, want 2", len(ds))
	}
	if ds[0].Severity != diag.Error {
		t.Errorf("AFS3002 recorded at %v, want error", ds[0].Severity)
	}
	if ds[1].Severity != diag.Info {
		t.Errorf("AFS1003 recorded at %v, want info", ds[1].Severity)
	}
	if !s.HasError() {
		t.Error("HasError is false with an error diagnostic recorded")
	}
	if worst, ok := s.Worst(); !ok || worst != diag.Error {
		t.Errorf("Worst = (%v,%v)", worst, ok)
	}
}

func TestEmptySinkHasNoWorstSeverity(t *testing.T) {
	t.Parallel()
	s := diag.NewSink("a", nil)
	if _, ok := s.Worst(); ok {
		t.Error("an empty sink reported a worst severity")
	}
	if s.HasError() {
		t.Error("an empty sink reported an error")
	}
}

func TestDiagnosticStringNamesCodePathAndPosition(t *testing.T) {
	t.Parallel()
	d := diag.Diagnostic{
		Code: diag.CodeStatusUnknown, Severity: diag.Error,
		Path: "a/state.json", Pointer: "/status", Line: 3, Column: 13,
		Message: "unknown status", Hint: "use running",
	}
	got := d.String()
	for _, want := range []string{"AFS3002", "error", "a/state.json", "3:13", "/status", "unknown status", "use running"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestSeverityMarshalsAsItsName(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(struct {
		S diag.Severity `json:"s"`
	}{diag.Warning})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"s":"warning"}` {
		t.Fatalf("marshalled as %s", b)
	}
}

func TestAbbreviateCutsOnRuneBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in string
		n  int
	}{
		{"日本語のテキストです", 5},
		{"abcdef", 3},
		{"日本語", 10},
		{"", 5},
	}
	for _, tc := range cases {
		got := diag.Abbreviate(tc.in, tc.n)
		if !isValidUTF8(got) {
			t.Errorf("Abbreviate(%q,%d) = %q, which is not valid UTF-8", tc.in, tc.n, got)
		}
		if len([]rune(got)) > tc.n && tc.n > 0 {
			t.Errorf("Abbreviate(%q,%d) = %q, %d runes", tc.in, tc.n, got, len([]rune(got)))
		}
	}
}

func FuzzLocate(f *testing.F) {
	f.Add(`{"a":1}`, "/a")
	f.Add(`{`, "/a")
	f.Add(`[1,2]`, "/1")
	f.Add(``, "")

	f.Fuzz(func(t *testing.T, src, pointer string) {
		line, col := diag.Locate([]byte(src), pointer)
		if line < 0 || col < 0 {
			t.Fatalf("Locate(%q,%q) = (%d,%d)", src, pointer, line, col)
		}
		if (line == 0) != (col == 0) {
			t.Fatalf("Locate(%q,%q) = (%d,%d): a position is either fully resolved or not at all", src, pointer, line, col)
		}
	})
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD && !strings.Contains(s, "�") {
			return false
		}
	}
	return true
}
