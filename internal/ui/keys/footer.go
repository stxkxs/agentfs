package keys

import (
	"strings"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

const (
	// hintSeparator divides one footer hint from the next.
	hintSeparator = " · "
	// keySeparator divides the spellings within one hint.
	keySeparator = "/"
)

// Footer renders the hint line for scope s, occupying exactly width cells.
//
// Hints appear in the order [Registry.ForScope] returns them: the scope's own
// bindings in table order, then the global fallbacks in table order. Table
// order is priority order, so when the hints do not all fit, the lowest
// priority ones are dropped whole and the line ends at a hint boundary rather
// than mid-word. A width too narrow for even the leading hint clips that hint,
// which keeps the key an operator needs most on the line instead of blanking
// it. A width of zero or less yields the empty string, since a line of no
// cells is not a line.
func (r *Registry) Footer(s Scope, width int) string {
	if width <= 0 {
		return ""
	}
	bindings := r.ForScope(s)
	hints := make([]string, 0, len(bindings))
	for _, b := range bindings {
		hints = append(hints, hint(b))
	}

	sep := textx.Width(hintSeparator)
	var line strings.Builder
	used := 0
	for i, h := range hints {
		cost := textx.Width(h)
		if i > 0 {
			cost += sep
		}
		if used+cost > width {
			break
		}
		if i > 0 {
			line.WriteString(hintSeparator)
		}
		line.WriteString(h)
		used += cost
	}
	if line.Len() == 0 && len(hints) > 0 {
		return fill(hints[0], width)
	}
	return fill(line.String(), width)
}

// fill returns s occupying exactly width cells, for a width of at least one.
//
// The footer covers a rectangle the layout has already fixed, so the line
// carries no slack in either direction. Clipping a grapheme cluster at the
// boundary can leave the line over that width, and padding text that ends in a
// mark that joins what follows it loses the first space to that mark's cluster
// and leaves the line under it. So the measured line is cut back to the width
// and then filled to it; the space a mark takes closes its cluster, so a
// second round of padding lands.
func fill(s string, width int) string {
	out := textx.Fit(s, width)
	for textx.Width(out) > width {
		_, size := utf8.DecodeLastRuneInString(out)
		out = out[:len(out)-size]
	}
	for range 2 {
		gap := width - textx.Width(out)
		if gap <= 0 {
			break
		}
		out += strings.Repeat(" ", gap)
	}
	return out
}

// hint renders one binding as it appears in the footer: the spellings, then
// what pressing one of them does. Spellings are sanitized at the point they are
// rendered rather than at the point they are stored, because [Registry.Resolve]
// matches them against what the terminal reports.
func hint(b Binding) string {
	return textx.Sanitize(strings.Join(b.Keys, keySeparator)) + " " + b.Help
}
