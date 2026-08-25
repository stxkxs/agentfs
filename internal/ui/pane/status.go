package pane

import (
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
)

// Condition is one thing limiting what agentfs can see, with a rank that orders
// it against the others.
type Condition struct {
	// Rank orders conditions; a lower rank is more serious and is dropped last
	// when the line cannot hold them all.
	Rank int
	// Text names the condition.
	Text string
	// Severe marks a condition that means the view is not current, rather than
	// merely incomplete.
	Severe bool
}

// Status renders the line that answers "am I seeing everything".
//
// Several ceilings can be reached at once, and a fixed-height line on a narrow
// terminal cannot render them all. Conditions are therefore ranked, the most
// serious survive, and what did not fit is counted rather than dropped in
// silence — because a status line that quietly omits a degradation is worse
// than one that admits it ran out of room.
type Status struct{}

// Conditions collects everything limiting the view, most serious first.
func Conditions(ws watch.Stats, ix index.Stats, invalidDocs int) []Condition {
	var out []Condition

	if ws.RootLost {
		out = append(out, Condition{Rank: 0, Text: "workspace root unreadable — retrying", Severe: true})
	}
	if ws.Dropped > 0 {
		out = append(out, Condition{Rank: 1,
			Text: strconv.FormatUint(ws.Dropped, 10) + " changes dropped, resynchronized", Severe: true})
	}
	if ws.WatchesRefused > 0 {
		out = append(out, Condition{Rank: 2,
			Text: strconv.FormatUint(ws.WatchesRefused, 10) + " directories swept, not watched"})
	}
	if ix.NodeCeilingHit {
		out = append(out, Condition{Rank: 3, Text: "tree at its node ceiling", Severe: true})
	}
	if ix.TruncatedDirs > 0 {
		out = append(out, Condition{Rank: 4,
			Text: strconv.Itoa(ix.TruncatedDirs) + " directories capped"})
	}
	if ix.DepthTruncated > 0 {
		out = append(out, Condition{Rank: 5,
			Text: strconv.Itoa(ix.DepthTruncated) + " subtrees below the depth ceiling"})
	}
	if ix.UnreadableDirs > 0 {
		out = append(out, Condition{Rank: 6,
			Text: strconv.Itoa(ix.UnreadableDirs) + " directories unreadable"})
	}
	if invalidDocs > 0 {
		out = append(out, Condition{Rank: 7,
			Text: strconv.Itoa(invalidDocs) + " state documents invalid"})
	}
	if ws.Errors > 0 {
		out = append(out, Condition{Rank: 8,
			Text: strconv.FormatUint(ws.Errors, 10) + " source errors"})
	}
	return out
}

// View renders the status line into r.
func (Status) View(mode watch.Stats, conditions []Condition, r layout.Rect, p theme.Palette) []string {
	prefix := p.Dim().Render("  " + modeSummary(mode) + " ")
	if len(conditions) == 0 {
		line := prefix + p.Status(theme.RoleDone).Render(p.Glyphs().Done+" complete view")
		return render.Rows([]string{textx.Fit(line, r.W)}, r)
	}

	sep := p.Dim().Render(" " + p.Glyphs().VerticalBar + " ")
	var b strings.Builder
	b.WriteString(prefix)
	used := textx.Width(prefix)
	shown := 0

	for i, c := range conditions {
		text := c.Text
		style := p.Severity(theme.RoleWarning)
		glyph := p.Glyphs().Warning
		if c.Severe {
			style = p.Severity(theme.RoleSevere)
			glyph = p.Glyphs().Severe
		}
		cell := style.Render(glyph + " " + text)

		need := textx.Width(cell)
		if shown > 0 {
			need += textx.Width(sep)
		}
		reserve := 0
		if remaining := len(conditions) - i - 1; remaining > 0 {
			reserve = textx.Width(p.Dim().Render(" +" + strconv.Itoa(remaining) + " more"))
		}
		if used+need+reserve > r.W {
			break
		}
		if shown > 0 {
			b.WriteString(sep)
		}
		b.WriteString(cell)
		used += need
		shown++
	}

	if shown < len(conditions) {
		b.WriteString(p.Dim().Render(" +" + strconv.Itoa(len(conditions)-shown) + " more"))
	}
	return render.Rows([]string{textx.Fit(b.String(), r.W)}, r)
}

// modeSummary states how the workspace is being observed, so an operator reads
// it rather than assuming kernel events are arriving.
func modeSummary(s watch.Stats) string {
	parts := []string{s.Filesystem.Type, s.Mode.String()}
	if s.Watches > 0 {
		parts = append(parts, strconv.Itoa(s.Watches)+" watches")
	}
	if s.Tracked > 0 {
		parts = append(parts, "sweep "+strconv.Itoa(s.Tracked))
	}
	if s.SweepCycle > 0 {
		parts = append(parts, s.SweepCycle.Round(100_000_000).String()+" cycle")
	}
	return strings.Join(parts, " · ")
}
