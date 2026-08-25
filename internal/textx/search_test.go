package textx_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

func TestFindAllReturnsEveryMatch(t *testing.T) {
	t.Parallel()
	spans := textx.FindAll("ab AB aB Ab", "ab")
	if len(spans) != 4 {
		t.Fatalf("got %d spans, want 4: %v", len(spans), spans)
	}
	for i, want := range []int{0, 3, 6, 9} {
		if spans[i].Start != want {
			t.Errorf("span %d starts at %d, want %d", i, spans[i].Start, want)
		}
	}
}

// A byte offset taken from a lowercased copy addresses the wrong byte of the
// original whenever folding changes byte length. U+0130 folds to a single-byte
// 'i', shifting every later offset by one.
func TestFindAllIsRuneSafeWhenFoldingChangesLength(t *testing.T) {
	t.Parallel()
	const line = "İabc"
	if len(line) == len(strings.ToLower(line)) {
		t.Fatal("fixture no longer exercises a length-changing fold")
	}

	spans := textx.FindAll(line, "abc")
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got := line[spans[0].Start:spans[0].End]; got != "abc" {
		t.Fatalf("span selects %q, want %q", got, "abc")
	}
}

func TestFindAllSpansAreValidUTF8(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ hay, needle string }{
		{"İabc", "abc"},
		{"straße", "SS"},
		{"ΣΣΣ", "σ"},
		{"日本語テスト", "テスト"},
		{"KELVIN K", "k"},
	} {
		for _, sp := range textx.FindAll(tc.hay, tc.needle) {
			got := tc.hay[sp.Start:sp.End]
			if !utf8.ValidString(got) {
				t.Errorf("FindAll(%q,%q) span %v selects invalid UTF-8 %q", tc.hay, tc.needle, sp, got)
			}
		}
	}
}

func TestFindAllMatchLengthTracksFolding(t *testing.T) {
	t.Parallel()
	spans := textx.FindAll("İ", "i̇")
	for _, sp := range spans {
		if sp.Len() == 0 {
			t.Fatalf("zero-length span %v", sp)
		}
	}
}

func TestFindAllEmptyNeedle(t *testing.T) {
	t.Parallel()
	if got := textx.FindAll("abc", ""); got != nil {
		t.Fatalf("empty needle returned %v, want nil", got)
	}
}

func TestContainsAgreesWithEqualFoldSemantics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"Hello", "hello", true},
		{"Hello", "ell", true},
		{"Hello", "xyz", false},
		{"İabc", "abc", true},
		{"", "a", false},
		{"a", "", true},
	}
	for _, tc := range cases {
		if got := textx.Contains(tc.hay, tc.needle); got != tc.want {
			t.Errorf("Contains(%q,%q)=%v, want %v", tc.hay, tc.needle, got, tc.want)
		}
	}
}

// FuzzFindAll asserts the property the corrupted implementation violated: every
// span must be sliceable out of the original string without splitting a rune.
func FuzzFindAll(f *testing.F) {
	f.Add("İabc", "abc")
	f.Add("straße", "SS")
	f.Add("日本語", "本")
	f.Add("aaaa", "aa")
	f.Add("\x00\xff", "\xff")

	f.Fuzz(func(t *testing.T, hay, needle string) {
		spans := textx.FindAll(hay, needle)
		prevEnd := 0
		for _, sp := range spans {
			if sp.Start < 0 || sp.End > len(hay) || sp.Start > sp.End {
				t.Fatalf("span %v out of range for %q", sp, hay)
			}
			if sp.Start < prevEnd {
				t.Fatalf("overlapping spans in %q: %v", hay, spans)
			}
			prevEnd = sp.End
			if utf8.ValidString(hay) && !utf8.ValidString(hay[sp.Start:sp.End]) {
				t.Fatalf("span %v splits a rune in %q", sp, hay)
			}
		}
		if len(spans) > 0 != textx.Contains(hay, needle) && needle != "" {
			t.Fatalf("FindAll and Contains disagree for %q/%q", hay, needle)
		}
	})
}
