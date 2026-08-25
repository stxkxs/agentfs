package textx_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

// A sequence a terminal acts on is replaced by one visible rune rather than
// dropped. Dropping it makes the sanitized text a claim about content the
// workspace does not hold: a status of ESC [ 2 J r u n n i n g renders as
// "running", and a diagnostic quoting that back reports a valid status as
// invalid.
func TestSanitizeMarksTerminalSequences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"osc52 clipboard write", "before\x1b]52;c;aGVsbG8=\x07after", "before\u238bafter"},
		{"osc8 hyperlink", "\x1b]8;;https://example.com\x1b\\text\x1b]8;;\x1b\\", "\u238btext\u238b"},
		{"csi screen clear", "a\x1b[2Jb", "a\u238bb"},
		{"csi cursor home", "a\x1b[Hb", "a\u238bb"},
		{"sgr colour", "\x1b[31mred\x1b[0m", "\u238bred\u238b"},
		{"dcs payload", "a\x1bPq#0;2;0;0;0\x1b\\b", "a\u238bb"},
		{"apc payload", "a\x1b_G\x1b\\b", "a\u238bb"},
		{"pm payload", "a\x1b^status\x1b\\b", "a\u238bb"},
		{"sos payload", "a\x1bXstring\x1b\\b", "a\u238bb"},
		{"two byte escape", "a\x1bcb", "a\u238bb"},
		{"bare escape at end", "ab\x1b", "ab\u238b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := textx.Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeReplacesBidiOverrides(t *testing.T) {
	t.Parallel()
	// A right-to-left override makes "gpj.exe" render as "exe.jpg".
	in := "invoice\u202egpj.exe"
	got := textx.Sanitize(in)
	if strings.ContainsRune(got, 0x202E) {
		t.Fatalf("Sanitize left a bidi override in %q", got)
	}
	if !strings.ContainsRune(got, textx.BidiMarker) {
		t.Fatalf("Sanitize(%q) = %q, want a visible bidi marker", in, got)
	}
}

func TestSanitizeReplacesZeroWidth(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF, 0x00AD} {
		got := textx.Sanitize("a" + string(r) + "b")
		if strings.ContainsRune(got, r) {
			t.Errorf("Sanitize left U+%04X in %q", r, got)
		}
	}
}

func TestSanitizeReplacesControlCharacters(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{0x00, 0x07, 0x0A, 0x0D, 0x7F, 0x85, 0x9B} {
		got := textx.Sanitize("a" + string(r) + "b")
		if strings.ContainsRune(got, r) {
			t.Errorf("Sanitize left U+%04X in %q", r, got)
		}
	}
}

func TestSanitizeExpandsTabs(t *testing.T) {
	t.Parallel()
	got := textx.Sanitize("a\tb")
	want := "a" + strings.Repeat(" ", textx.TabWidth) + "b"
	if got != want {
		t.Fatalf("Sanitize(%q) = %q, want %q", "a\tb", got, want)
	}
}

func TestSanitizeYieldsOneLine(t *testing.T) {
	t.Parallel()
	if got := textx.Sanitize("a\nb\r\nc"); strings.ContainsAny(got, "\n\r") {
		t.Fatalf("Sanitize produced a line break: %q", got)
	}
}

// markers are the runes Sanitize substitutes. Text carrying one already is
// indistinguishable from text where one was substituted.
var markers = string([]rune{textx.Replacement, textx.BidiMarker, textx.ZeroWidthMarker})

// isSafeToDraw reports a rune a terminal draws rather than acts on, which
// Sanitize must therefore leave alone.
func isSafeToDraw(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return false
	case unicode.In(r, unicode.Cf), unicode.Is(unicode.Cs, r):
		return false
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return false
	case r == utf8.RuneError:
		// Indistinguishable from the replacement Sanitize substitutes.
		return false
	case holdsItsClusterOpen(r):
		return false
	default:
		return true
	}
}

// holdsItsClusterOpen reports a rune that takes the character behind it into
// its own grapheme cluster, so a pane ending in one takes a cell from the pane
// beside it. The oracle measures the property rather than reading the set the
// implementation built from it, so the two agree only when both are right.
func holdsItsClusterOpen(r rune) bool {
	s := string(r)
	return textx.Width(s) > 0 && textx.Width(s+" ") == textx.Width(s)
}

// FuzzSanitize asserts the properties every render path depends on: the result
// is valid UTF-8, it carries nothing a terminal acts on, and — the half that
// stops the whole thing being satisfied by returning nothing — every rune that
// was already safe to draw is still there.
func FuzzSanitize(f *testing.F) {
	f.Add("\x1b]52;c;aGk=\x07")
	f.Add("\u202eexe.jpg")
	f.Add("plain text")
	f.Add("\xff\xfe invalid")
	f.Add("\x1b[38;2;255;0;0mtrue colour\x1b[m")
	f.Add("\u0600123") // an Arabic number sign joins the digits after it
	f.Add("\u061Cabc") // an Arabic letter mark reorders
	f.Add("a\ufe0fb")  // a variation selector changes the rune before it

	f.Fuzz(func(t *testing.T, in string) {
		got := textx.Sanitize(in)
		if !utf8.ValidString(got) {
			t.Fatalf("Sanitize(%q) produced invalid UTF-8 %q", in, got)
		}
		for _, r := range got {
			switch {
			case r == 0x1B:
				t.Fatalf("Sanitize(%q) left an escape in %q", in, got)
			case r < 0x20 && r != ' ':
				t.Fatalf("Sanitize(%q) left C0 U+%04X in %q", in, r, got)
			case r == 0x7F, r >= 0x80 && r <= 0x9F:
				t.Fatalf("Sanitize(%q) left control U+%04X in %q", in, r, got)
			case unicode.In(r, unicode.Cf):
				// The oracle names the Unicode category rather than the list
				// the implementation happens to carry. A list checked against
				// itself passes for every character neither side thought of.
				t.Fatalf("Sanitize(%q) left the format character U+%04X in %q", in, r, got)
			case unicode.Is(unicode.Cs, r):
				t.Fatalf("Sanitize(%q) left the surrogate U+%04X in %q", in, r, got)
			case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
				t.Fatalf("Sanitize(%q) left the variation selector U+%04X in %q", in, r, got)
			}
		}
		if textx.Sanitize(got) != got {
			t.Fatalf("Sanitize is not idempotent for %q", in)
		}

		// The negative half above is satisfied by returning nothing, so a
		// positive half is what makes it a property rather than a filter.
		//
		// It is stated over inputs carrying no escape: inside a sequence, a
		// character that is safe on its own is part of the structure being
		// removed, and asserting it survives would assert the opposite of what
		// the function is for.
		//
		// A tab expands to spaces and a substituted rune is drawn as a marker,
		// so an input carrying either has no rune-for-rune correspondence to
		// assert. Excluding them leaves the property stated over an input space
		// large enough that a filter cannot satisfy it.
		if !strings.ContainsAny(in, "\x1b\t") && !strings.ContainsAny(in, markers) {
			var want []rune
			for _, r := range in {
				if isSafeToDraw(r) {
					want = append(want, r)
				}
			}
			kept := []rune{}
			for _, r := range got {
				// A marker stands where an unsafe rune was, and the input was
				// checked to carry none of its own.
				if isSafeToDraw(r) && !strings.ContainsRune(markers, r) {
					kept = append(kept, r)
				}
			}
			if string(kept) != string(want) {
				t.Fatalf("Sanitize(%q) = %q: safe runes %q became %q",
					in, got, string(want), string(kept))
			}
		}
	})
}
