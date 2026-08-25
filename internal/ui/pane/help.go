package pane

import (
	"strings"

	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Help renders the full binding table.
//
// It is drawn from the same registry that resolves a press and generates the
// key reference, so a binding cannot be live without appearing here and cannot
// appear here without being live.
type Help struct {
	scroller
}

// Update applies a navigation action over the help rows.
func (h *Help) Update(a keys.Action, r *keys.Registry, height int) bool {
	return h.apply(a, len(helpRows(r, 0)), height)
}

// View renders the help overlay into rect.
func (h Help) View(r *keys.Registry, rect layout.Rect, p theme.Palette) []string {
	rows := helpRows(r, max(rect.W-4, 20))
	view := h
	start, end := view.window(len(rows), rect.H)

	out := make([]string, 0, rect.H)
	for i := start; i < end; i++ {
		out = append(out, textx.Fit(rows[i].render(p), rect.W))
	}
	return render.Rows(out, rect)
}

// helpRow is either a section heading or a binding.
type helpRow struct {
	heading string
	keys    string
	help    string
}

func (r helpRow) render(p theme.Palette) string {
	if r.heading != "" {
		return "  " + p.Title().Render(r.heading)
	}
	return "    " + p.Accent().Render(pad(r.keys, 18)) + p.Body().Render(r.help)
}

// helpRows flattens the registry into displayable rows.
func helpRows(r *keys.Registry, _ int) []helpRow {
	var rows []helpRow
	for _, section := range r.Help() {
		if len(section.Bindings) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, helpRow{})
		}
		rows = append(rows, helpRow{heading: sectionTitle(section.Scope)})
		for _, b := range section.Bindings {
			rows = append(rows, helpRow{keys: strings.Join(b.Keys, " / "), help: b.Help})
		}
	}
	return rows
}

func sectionTitle(s keys.Scope) string {
	switch s {
	case keys.ScopeGlobal:
		return "Everywhere"
	case keys.ScopeTree:
		return "Files"
	case keys.ScopePreview:
		return "Preview"
	case keys.ScopeFeed:
		return "Activity"
	case keys.ScopeRuns:
		return "Runs"
	case keys.ScopeSearch:
		return "Search"
	default:
		return s.String()
	}
}

func pad(s string, w int) string {
	if gap := w - textx.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s + " "
}
