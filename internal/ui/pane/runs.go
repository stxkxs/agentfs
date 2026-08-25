package pane

import (
	"strconv"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// Runs renders recorded executions.
//
// A run whose identity was declared and one whose identity was guessed from the
// shape of a directory name are rendered differently, because an operator
// acting on a run needs to know which of the two they are looking at.
type Runs struct {
	scroller
}

// Selected returns the run under the cursor.
func (rn Runs) Selected(runs []workspace.Run) (workspace.Run, bool) {
	if rn.cursor < 0 || rn.cursor >= len(runs) {
		return workspace.Run{}, false
	}
	return runs[rn.cursor], true
}

// Update applies a navigation action.
func (rn *Runs) Update(a keys.Action, runs []workspace.Run, height int) bool {
	return rn.apply(a, len(runs), height)
}

// Badge summarizes the list.
func (rn Runs) Badge(runs []workspace.Run) string {
	declared := 0
	for _, r := range runs {
		if r.Declared {
			declared++
		}
	}
	if len(runs) == 0 {
		return "none"
	}
	return strconv.Itoa(len(runs)) + " runs · " + strconv.Itoa(declared) + " declared"
}

// View renders the run list into r.
func (rn Runs) View(runs []workspace.Run, r layout.Rect, p theme.Palette, now time.Time) []string {
	if len(runs) == 0 {
		return render.Rows([]string{textx.Fit(p.Dim().Render("  no runs recorded"), r.W)}, r)
	}

	view := rn
	start, end := view.window(len(runs), r.H)

	out := make([]string, 0, r.H)
	for i := start; i < end; i++ {
		run := runs[i]
		g := p.Glyphs()

		var b strings.Builder
		b.WriteString(p.Status(statusRole(run.Status)).Render(statusGlyph(g, statusRole(run.Status))))
		b.WriteByte(' ')
		b.WriteString(p.Dim().Render(runStamp(run.StartedAt, now)))
		b.WriteByte(' ')
		b.WriteString(p.Title().Render(textx.Sanitize(run.ID)))
		if !run.Declared {
			// An identity read off a directory name is a guess, and saying so
			// is cheaper than an operator discovering it later.
			b.WriteString(p.Dim().Render(" (inferred)"))
		}
		b.WriteByte(' ')
		b.WriteString(p.Dim().Render(strconv.Itoa(run.Files) + " files"))
		out = append(out, render.Row(p, i == rn.cursor, b.String(), r.W))
	}
	return render.Rows(out, r)
}

// runStamp renders a run's start relative to now for anything inside a day, and
// as a date beyond it.
func runStamp(at, now time.Time) string {
	if at.IsZero() {
		return "unknown  "
	}
	if d := now.Sub(at); d >= 0 && d < 24*time.Hour {
		return at.Format("15:04:05")
	}
	return at.Format("01-02 15:04")
}
