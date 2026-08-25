package textx

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Ellipsis marks text removed to make it fit.
const Ellipsis = "…"

// Width returns the display width of s in terminal cells. ANSI styling
// contributes no width, and a double-width rune contributes two cells.
func Width(s string) int { return ansi.StringWidth(s) }

// Truncate returns s clipped to at most width cells, marking any elision with
// [Ellipsis]. Styling is preserved and never split mid-sequence, and a
// double-width rune is dropped rather than halved.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return s
	}
	if width == 1 {
		return Ellipsis
	}
	return ansi.Truncate(s, width, Ellipsis)
}

// Pad returns s padded on the right to at least width cells.
//
// The fill is measured where it lands rather than derived from one subtraction
// of the unpadded width, because display width is not additive across
// concatenation. A grapheme cluster left open at the end of s takes the first
// space into itself, so a fill counted in advance lands a cell short. Only the
// first space can be taken that way: a space behind a space begins a cluster of
// its own.
//
// Text that already fills the width takes no fill, so a cluster left open at
// its end stays open and takes a cell from whatever is concatenated behind it.
// [Sanitize] removes the runes that hold a cluster open, which is what makes
// the widths of fitted segments add up; text that has not passed through it
// carries no such guarantee.
func Pad(s string, width int) string {
	gap := width - Width(s)
	if gap <= 0 {
		return s
	}
	padded := s + strings.Repeat(" ", gap)
	if short := width - Width(padded); short > 0 {
		padded += strings.Repeat(" ", short)
	}
	return padded
}

// Fit returns s occupying exactly width cells: clipped when wider, padded when
// narrower. Every pane's output passes through Fit at the render boundary, so a
// pane cannot overflow its rectangle by forgetting to clamp.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return Pad(Truncate(s, width), width)
}

// Elide shortens a slash-separated path to at most width cells by replacing
// interior segments with [Ellipsis], keeping the first segment and as much of
// the tail as fits. The base name is what identifies a file, so it survives in
// preference to the path that leads to it.
func Elide(p string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(p) <= width {
		return p
	}
	segs := strings.Split(p, "/")
	if len(segs) <= 2 {
		return Truncate(p, width)
	}
	first := segs[0]
	for i := 1; i < len(segs)-1; i++ {
		candidate := first + "/" + Ellipsis + "/" + strings.Join(segs[i+1:], "/")
		if Width(candidate) <= width {
			return candidate
		}
	}
	base := segs[len(segs)-1]
	if candidate := Ellipsis + "/" + base; Width(candidate) <= width {
		return candidate
	}
	return Truncate(base, width)
}

// Abbrev shortens s for use in a test failure or a log line, where the whole
// value would bury the message that names the problem.
func Abbrev(s string) string { return Truncate(s, 40) }
