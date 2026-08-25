package textx_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

func TestFitProducesExactWidth(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"",
		"short",
		strings.Repeat("x", 200),
		"日本語のテキスト",
		"emoji 👩‍👩‍👧‍👦 family",
		"\x1b[31mstyled\x1b[0m",
		"mixed 日本 abc 🎉",
		// Grapheme_Cluster_Break=Prepend. Each joins the character after it
		// into its own cluster, so the first space of a pad appended behind one
		// occupies no cell and a fill counted before it is placed lands short.
		"\u0d4e",
		"\U000111c2",
		"\U000113d1",
		// The character the cluster takes is whatever follows it: the pad when
		// the class ends the string, the text when it does not.
		"a\u0d4e",
		"\u0d4ea",
	}
	for _, in := range inputs {
		for _, w := range []int{1, 2, 3, 8, 40, 200} {
			got := textx.Fit(in, w)
			if width := textx.Width(got); width != w {
				t.Errorf("Fit(%q,%d) width = %d, want %d (got %q)", in, w, width, w, got)
			}
		}
	}
}

func TestFitZeroWidthIsEmpty(t *testing.T) {
	t.Parallel()
	for _, w := range []int{0, -1, -100} {
		if got := textx.Fit("anything", w); got != "" {
			t.Errorf("Fit(_, %d) = %q, want empty", w, got)
		}
	}
}

func TestTruncateMarksElision(t *testing.T) {
	t.Parallel()
	got := textx.Truncate("abcdefgh", 4)
	if !strings.HasSuffix(got, textx.Ellipsis) {
		t.Fatalf("Truncate lost the elision marker: %q", got)
	}
	if textx.Width(got) > 4 {
		t.Fatalf("Truncate(%q,4) = %q, width %d", "abcdefgh", got, textx.Width(got))
	}
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	t.Parallel()
	if got := textx.Truncate("abc", 10); got != "abc" {
		t.Fatalf("Truncate(%q,10) = %q", "abc", got)
	}
}

func TestElideKeepsTheBaseName(t *testing.T) {
	t.Parallel()
	p := "agent-researcher/runs/run-001/artifacts/output.json"
	got := textx.Elide(p, 30)
	if textx.Width(got) > 30 {
		t.Fatalf("Elide(%q,30) = %q, width %d", p, got, textx.Width(got))
	}
	if !strings.Contains(got, "output.json") {
		t.Fatalf("Elide dropped the base name: %q", got)
	}
}

func TestElideLeavesShortPathsAlone(t *testing.T) {
	t.Parallel()
	if got := textx.Elide("a/b.json", 40); got != "a/b.json" {
		t.Fatalf("Elide rewrote a path that already fits: %q", got)
	}
}

// FuzzFit asserts that Fit returns exactly the requested number of cells for one
// segment. The render boundary needs more than that — segments placed side by
// side must occupy the sum of their widths, which
// TestSanitizedSegmentsConcatenateToTheSumOfTheirWidths covers.
func FuzzFit(f *testing.F) {
	f.Add("hello", 10)
	f.Add("日本語", 4)
	f.Add("\x1b[31mred\x1b[0m", 2)
	f.Add("👩‍👩‍👧‍👦", 3)
	f.Add("", 1)
	// Grapheme_Cluster_Break=Prepend, where the first space of a pad joins the
	// cluster before it and adds no cell. The class is held as a seed because a
	// seed corpus is what runs under `task test`; the search beyond it has a
	// time budget and reaches this class only sometimes.
	f.Add("\u0d4e", 2)
	f.Add("\u0d4e", 220)
	f.Add("\U000111c2", 2)
	f.Add("\U000111c2", 220)
	f.Add("\U000113d1", 2)
	f.Add("\U000113d1", 220)

	f.Fuzz(func(t *testing.T, s string, w int) {
		if w < 0 || w > 4096 {
			t.Skip()
		}
		got := textx.Fit(textx.Sanitize(s), w)
		if width := textx.Width(got); width != w {
			t.Fatalf("Fit(sanitize(%q),%d) width = %d, want %d", s, w, width, w)
		}
	})
}

// The render boundary composes panes by concatenation and advances by each
// segment's declared width, so two segments fitted to ten cells each must
// render as twenty. A rune that holds its grapheme cluster open takes the first
// cell of the segment behind it, and a segment that already fills its width
// takes no fill to close the cluster with.
func TestSanitizedSegmentsConcatenateToTheSumOfTheirWidths(t *testing.T) {
	t.Parallel()

	for r := rune(0); r <= 0x10FFFF; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		left := textx.Sanitize(strings.Repeat("a", 9) + string(r))
		if textx.Width(left) == 0 {
			continue
		}
		for _, width := range []int{10, 11, 20} {
			l := textx.Fit(left, width)
			right := textx.Fit("RIGHT", width)
			if got := textx.Width(l + right); got != 2*width {
				t.Fatalf("two segments of %d cells ending U+%04X concatenate to %d: %q",
					width, r, got, l+right)
			}
		}
	}
}

// The bound [absorbs] refuses outside of holds for the whole code space, so a
// rune that joins what follows it cannot reach the render boundary by lying
// beyond a range nobody re-measured.
func TestNoRuneOutsideTheAbsorbingBoundAbsorbs(t *testing.T) {
	t.Parallel()

	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0x0D4E && r <= 0x11FFF {
			continue
		}
		s := string(r)
		if textx.Width(s) > 0 && textx.Width(s+" ") == textx.Width(s) {
			t.Errorf("U+%04X joins what follows it and lies outside the bound", r)
		}
	}
}
