// Package textx turns untrusted workspace bytes into cells that are safe to
// render and correct to measure.
//
// Workspace content is written by autonomous agents and rendered into the
// operator's terminal. A log line carrying an OSC 52 sequence writes the
// system clipboard; one carrying a bidi override reorders the text around it;
// one carrying a CSI clear erases the frame. [Sanitize] is the single boundary
// where that is neutralized, and every path from a workspace byte to the screen
// passes through it.
package textx

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// TabWidth is the number of cells a horizontal tab expands to. Tabs are
// expanded rather than passed through because a tab moves the terminal cursor
// by an amount the width calculation cannot predict.
const TabWidth = 4

// Replacement stands in for a rune that is unsafe to emit. It occupies one
// cell, so a sanitized string's width is stable under substitution.
const Replacement = '�'

const (
	// BidiMarker replaces a Unicode bidirectional control. The control is a
	// display-spoofing vector: it reorders the text that follows it, so a
	// filename can render as something other than what it opens.
	BidiMarker = '░'
	// ZeroWidthMarker replaces a zero-width or invisible format character,
	// which would otherwise let content hide between rendered runes.
	ZeroWidthMarker = '·'
	// EscapeMarker replaces a consumed terminal escape sequence. A sequence
	// removed without a trace makes the sanitized text a claim about content
	// the workspace does not hold: a status of ESC [ 2 J r u n n i n g renders
	// as "running", and an operator told that status is outside the vocabulary
	// is looking at one that is inside it. The marker keeps the rendering
	// honest about what was there.
	EscapeMarker = '⎋'
)

// Sanitize returns s with every terminal control sequence and every unsafe rune
// replaced by a visible stand-in. Nothing is dropped: a rune in, a rune out for
// every sequence, so the result says what the input held.
//
// Replaced by [EscapeMarker]: CSI, OSC, DCS, APC, PM and SOS sequences, and
// two-byte escapes. Replaced: C0 and C1 controls other than tab, bidirectional
// formatting controls, zero-width format characters, and invalid UTF-8.
// Newlines are replaced rather than preserved, so one input line is always one
// output line.
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		c := s[i]

		if c == 0x1B { // ESC
			i = skipEscape(s, i)
			b.WriteRune(EscapeMarker)
			continue
		}
		if c == '\t' {
			b.WriteString(strings.Repeat(" ", TabWidth))
			i++
			continue
		}
		if c < 0x20 || c == 0x7F { // C0 and DEL
			b.WriteRune(Replacement)
			i++
			continue
		}
		if c < utf8.RuneSelf {
			b.WriteByte(c)
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(Replacement)
			i++
			continue
		}
		b.WriteRune(safeRune(r))
		i += size
	}
	return b.String()
}

// safeRune returns what may be drawn in place of r.
//
// The unsafe set is named by Unicode category rather than enumerated. An
// enumeration is a list of the characters somebody thought of, and the ones
// nobody thought of are exactly the ones an attacker reaches for — U+0600
// ARABIC NUMBER SIGN joins the digits after it, U+061C reorders, and neither
// appears on a list written from the bidi controls alone. Category membership
// is the property that matters: a format character is one the renderer acts on
// rather than draws.
func safeRune(r rune) rune {
	switch {
	case r >= 0x80 && r <= 0x9F: // C1 controls
		return Replacement
	case isBidiControl(r):
		return BidiMarker
	case absorbs(r):
		return ZeroWidthMarker
	case unicode.In(r, unicode.Cf), isVariationSelector(r), unicode.Is(unicode.Cs, r):
		// Cf is every format character. A variation selector is a mark rather
		// than a format character and changes how the rune before it renders,
		// so it is neutralized too. An unpaired surrogate is not text.
		return ZeroWidthMarker
	default:
		return r
	}
}

// isBidiControl reports the format characters that reorder the text around
// them. They are drawn with their own marker because a reordered filename is a
// different threat from a hidden one, and an operator reading the marker should
// be able to tell which they are looking at.
func isBidiControl(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, // LRE RLE PDF LRO RLO
		r >= 0x2066 && r <= 0x2069, // LRI RLI FSI PDI
		r == 0x200E, r == 0x200F,   // LRM RLM
		r == 0x061C: // Arabic letter mark
		return true
	default:
		return false
	}
}

// firstAbsorbing and lastAbsorbing bound the runes that join what follows them
// into their own grapheme cluster. Outside the bound nothing does, so the ASCII
// and Latin text that makes up most of a workspace answers [absorbs] with a
// comparison rather than a lookup. TestNoRuneOutsideTheAbsorbingBoundAbsorbs
// holds the bound to the whole code space.
const (
	firstAbsorbing = 0x0D4E
	lastAbsorbing  = 0x11FFF
)

// absorbing is every rune within the bound that takes the character behind it
// into its own cluster.
//
// The set is measured rather than listed. A list is the runes somebody thought
// of, and Unicode adds more; measuring asks the same question the renderer will
// ask. It is measured once, because asking per rune costs more than the render
// boundary can spend.
var absorbing = sync.OnceValue(func() map[rune]struct{} {
	out := make(map[rune]struct{})
	for r := rune(firstAbsorbing); r <= lastAbsorbing; r++ {
		if joinsWhatFollows(r) {
			out[r] = struct{}{}
		}
	}
	return out
})

// joinsWhatFollows reports whether a space written behind r occupies no cell of
// its own, which is what it means for r to hold the cluster open.
func joinsWhatFollows(r rune) bool {
	s := string(r)
	return Width(s) > 0 && Width(s+" ") == Width(s)
}

// absorbs reports a rune that takes the character behind it into its own
// grapheme cluster, so that character occupies no cell of its own.
//
// A pane fitted to its rectangle ends where the rectangle does. When its last
// cluster is left open it takes the first cell of whatever is drawn beside it,
// and two panes of ten cells each render as nineteen — the neighbour shifts a
// column and the frame's last column collapses. Padding cannot close such a
// cluster: whatever is appended to close it is what gets absorbed. Removing the
// rune at this boundary is what makes width additive across concatenation,
// which is the property the render boundary composes panes on.
func absorbs(r rune) bool {
	if r < firstAbsorbing || r > lastAbsorbing {
		return false
	}
	_, ok := absorbing()[r]
	return ok
}

// isVariationSelector reports a mark that changes the presentation of the rune
// before it without occupying a cell of its own.
func isVariationSelector(r rune) bool {
	return (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

// skipEscape returns the index just past the escape sequence beginning at i.
//
// The three shapes are those of ECMA-48: a control sequence, a string-terminated
// sequence, and a two-byte escape. Each is consumed by its own grammar, because
// a sequence consumed by the wrong one leaves its remainder in the output.
func skipEscape(s string, i int) int {
	i++ // consume ESC
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipControlSequence(s, i+1)
	case ']', 'P', '_', '^', 'X':
		return skipStringSequence(s, i+1)
	default:
		return i + 1
	}
}

// skipControlSequence consumes a CSI: parameter bytes, then intermediate bytes,
// then one final byte.
func skipControlSequence(s string, i int) int {
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3F {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2F {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7E {
		i++
	}
	return i
}

// skipStringSequence consumes an OSC, DCS, APC, PM or SOS, which run until a
// string terminator: either BEL or ESC backslash.
func skipStringSequence(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1B && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		}
		i++
	}
	return i
}
