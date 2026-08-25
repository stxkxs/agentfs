package pane

import (
	"strconv"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Budgets renders the response-time deadlines agentfs holds itself to and what
// a session spent against them.
//
// The rows come from [metrics.Registry.Budgets] and nothing else, so a budget
// reaches the screen by being registered: there is no list of names here to
// fall out of step with the one the registry holds.
//
// The observations exist only while frames are being produced, which is why the
// record is read here rather than by a command that prints a report and exits.
// A budget with no observations says so, because a deadline nothing exercised
// and a deadline nothing breached are different answers.
type Budgets struct {
	scroller
}

// The columns, left to right. The marker holds the state glyph with a space on
// either side; the rest hold the budget's name and the numbers, each number
// right-aligned so a column of durations reads down the page.
const (
	budgetMarkerWidth = 3
	budgetNameWidth   = 18
	budgetValueWidth  = 9
)

// budgetAbsent stands where a budget with no observations would carry a number,
// so an unexercised path reads as unmeasured rather than as fast.
const budgetAbsent = "-"

// Update applies a navigation action over the budget rows.
func (b *Budgets) Update(a keys.Action, stats []metrics.BudgetStats, height int) bool {
	return b.apply(a, len(budgetLines(stats)), height)
}

// Badge summarizes the table for the box border: how many budgets are held, and
// how many of them the session has missed.
func (Budgets) Badge(stats []metrics.BudgetStats) string {
	breached := 0
	for _, s := range stats {
		if !s.Met() {
			breached++
		}
	}
	badge := strconv.Itoa(len(stats)) + " budgets"
	if breached > 0 {
		badge += " · " + strconv.Itoa(breached) + " breached"
	}
	return badge
}

// View renders the budget table into rect.
func (b Budgets) View(stats []metrics.BudgetStats, rect layout.Rect, p theme.Palette) []string {
	lines := budgetLines(stats)
	view := b
	start, end := view.window(len(lines), rect.H)

	out := make([]string, 0, rect.H)
	for i := start; i < end; i++ {
		out = append(out, textx.Fit(lines[i].render(p), rect.W))
	}
	return render.Rows(out, rect)
}

// budgetLine is one row of the table: a plain note, the column heading, or a
// budget's record.
type budgetLine struct {
	// note is a line of prose, rendered dim, when it is not empty.
	note string
	// header marks the column heading.
	header bool
	// stats is the record the row reports, when the row reports one.
	stats metrics.BudgetStats
}

func (l budgetLine) render(p theme.Palette) string {
	switch {
	case l.note != "":
		return strings.Repeat(" ", budgetMarkerWidth) + p.Dim().Render(l.note)
	case l.header:
		return budgetHeader(p)
	default:
		return budgetRow(l.stats, p)
	}
}

// budgetLines flattens the record into rows. It takes no palette, so the count
// a scroller moves over is the count the view draws.
func budgetLines(stats []metrics.BudgetStats) []budgetLine {
	if len(stats) == 0 {
		return []budgetLine{{note: "no budgets registered"}}
	}

	lines := make([]budgetLine, 0, len(stats)+2)
	lines = append(lines,
		budgetLine{note: "this session · percentiles over the last " +
			strconv.Itoa(metrics.ReservoirSize) + " observations"},
		budgetLine{header: true})
	for _, s := range stats {
		lines = append(lines, budgetLine{stats: s})
	}
	return lines
}

func budgetHeader(p theme.Palette) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", budgetMarkerWidth))
	b.WriteString(p.Title().Render(pad("budget", budgetNameWidth)))
	for _, name := range []string{"deadline", "count", "breached", "p50", "p90", "p99", "max"} {
		b.WriteString(p.Title().Render(rightAlign(name)))
	}
	return b.String()
}

func budgetRow(s metrics.BudgetStats, p theme.Palette) string {
	var b strings.Builder
	b.WriteString(budgetMarker(s, p))
	b.WriteString(p.Accent().Render(pad(textx.Sanitize(s.Name), budgetNameWidth)))
	b.WriteString(p.Dim().Render(rightAlign(budgetDuration(s.Deadline))))
	b.WriteString(p.Body().Render(rightAlign(strconv.FormatInt(s.Count, 10))))

	breached := rightAlign(strconv.FormatInt(s.Breached, 10))
	if s.Met() {
		b.WriteString(p.Body().Render(breached))
	} else {
		b.WriteString(p.Severity(theme.RoleWarning).Render(breached))
	}

	for _, d := range []time.Duration{s.P50, s.P90, s.P99, s.Max} {
		cell := budgetAbsent
		if s.Count > 0 {
			cell = budgetDuration(d)
		}
		b.WriteString(p.Body().Render(rightAlign(cell)))
	}
	return b.String()
}

// budgetMarker states the budget's standing in one cell: exercised and inside
// its deadline, exercised and past it, or not exercised at all.
func budgetMarker(s metrics.BudgetStats, p theme.Palette) string {
	switch {
	case s.Count == 0:
		return " " + p.Dim().Render(p.Glyphs().Unknown) + " "
	case s.Met():
		return " " + p.Status(theme.RoleDone).Render(p.Glyphs().Done) + " "
	default:
		return " " + p.Severity(theme.RoleWarning).Render(p.Glyphs().Warning) + " "
	}
}

// budgetDuration spells a duration in the unit its column is read in.
// Milliseconds carry the deadlines and nearly every observation, so a span
// below a second is spelled in them, with a fraction only where the whole part
// is too coarse to separate two spans.
func budgetDuration(d time.Duration) string {
	if d >= time.Second {
		return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + "s"
	}
	ms := float64(d) / float64(time.Millisecond)
	if ms >= 10 {
		return strconv.FormatFloat(ms, 'f', 0, 64) + "ms"
	}
	return strconv.FormatFloat(ms, 'f', 2, 64) + "ms"
}

// rightAlign pads s against the right of a value column, holding one cell back
// as the gap to the column after it. A value wider than the column is
// truncated, so one long number shifts nothing that follows.
func rightAlign(s string) string {
	if gap := budgetValueWidth - textx.Width(s) - 1; gap > 0 {
		return strings.Repeat(" ", gap) + s + " "
	}
	return textx.Truncate(s, budgetValueWidth-1) + " "
}
