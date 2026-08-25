package pane

import (
	"cmp"

	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// gutterWidth is the width of the line-number column, including its trailing
// space.
const gutterWidth = 6

// Preview renders one file.
//
// It scrolls rather than selecting: a file has no row a reader acts on, so a
// key that moved an invisible cursor inside the visible window would change
// nothing on screen and read as a dropped key press. The cursor exists only to
// carry the current search match.
type Preview struct {
	scroller
	path    string
	query   string
	matches []int
	match   int
}

// Path returns the workspace-relative path of the file on display.
func (v Preview) Path() string { return v.path }

// Query returns the active search text.
func (v Preview) Query() string { return v.query }

// SetPath records which file the pane is showing and resets its position.
func (v *Preview) SetPath(p string) {
	if v.path == p {
		return
	}
	v.path, v.cursor, v.offset = p, 0, 0
	v.clearMatches()
}

// Search records a query and locates every line that matches it.
func (v *Preview) Search(w *fileview.Window, query string) {
	v.query = query
	v.matches, v.match = nil, 0
	if query == "" || w == nil {
		return
	}
	for i, line := range w.Lines() {
		if textx.Contains(line.Text, query) {
			v.matches = append(v.matches, i)
		}
	}
}

func (v *Preview) clearMatches() {
	v.query, v.matches, v.match = "", nil, 0
}

// Matches returns the current match position and the number of matches, for the
// pane's badge.
func (v Preview) Matches() (at, total int) {
	if len(v.matches) == 0 {
		return 0, 0
	}
	return v.match + 1, len(v.matches)
}

// ShowMatch brings the match [Preview.Matches] names into the viewport,
// reporting whether there was one to show.
//
// [Preview.Search] locates the hits and leaves the position on the first, which
// the badge reports. Nothing about locating a hit puts it on screen, so a
// caller that has just run a query calls this or the badge names a line the
// frame is not showing.
func (v *Preview) ShowMatch(w *fileview.Window, height int) bool {
	if len(v.matches) == 0 {
		return false
	}
	v.cursor = v.matches[v.match]
	v.centre(lineCount(w), height)
	return true
}

// Update applies a navigation or search action.
func (v *Preview) Update(a keys.Action, w *fileview.Window, height int) bool {
	count := lineCount(w)

	switch a {
	case keys.ActionNextMatch, keys.ActionPrevMatch:
		if len(v.matches) == 0 {
			return false
		}
		if a == keys.ActionNextMatch {
			v.match = (v.match + 1) % len(v.matches)
		} else {
			v.match = (v.match - 1 + len(v.matches)) % len(v.matches)
		}
		v.cursor = v.matches[v.match]
		v.centre(count, height)
		return true
	case keys.ActionClearSearch:
		if v.query == "" {
			return false
		}
		v.clearMatches()
		return true
	default:
		return v.scroll(a, count, height)
	}
}

// lineCount is how many lines a window holds, counting an absent window as
// none: a pane with no file loaded is a pane with nothing to move within.
func lineCount(w *fileview.Window) int {
	if w == nil {
		return 0
	}
	return len(w.Lines())
}

// scroll moves the viewport. The last line can be brought to the top, so the
// end of a file is reachable however long its last line is.
func (v *Preview) scroll(a keys.Action, count, height int) bool {
	before := v.offset
	switch a {
	case keys.ActionUp:
		v.offset--
	case keys.ActionDown:
		v.offset++
	case keys.ActionPageUp:
		v.offset -= max(height/2, 1)
	case keys.ActionPageDown:
		v.offset += max(height/2, 1)
	case keys.ActionTop:
		v.offset = 0
	case keys.ActionBottom:
		v.offset = count
	default:
		return false
	}
	v.offset = min(max(v.offset, 0), max(count-height, 0))
	v.cursor = v.offset
	return v.offset != before
}

// centre puts the cursor in the middle of the viewport, so a match arrives with
// the lines around it rather than at an edge.
func (v *Preview) centre(count, height int) {
	v.offset = v.cursor - height/2
	v.clamp(count, height)
}

// Badge names what the pane is showing and what it is not.
func (v Preview) Badge(w *fileview.Window) string {
	if w == nil || v.path == "" {
		return ""
	}
	parts := []string{strconv.Itoa(len(w.Lines())) + " lines"}
	if head, tail := w.Truncated(); head || tail {
		switch {
		case head && tail:
			parts = append(parts, "middle of "+humanBytes(w.Size()))
		case head:
			parts = append(parts, "tail of "+humanBytes(w.Size()))
		default:
			parts = append(parts, "head of "+humanBytes(w.Size()))
		}
	}
	if at, total := v.Matches(); total > 0 {
		parts = append(parts, strconv.Itoa(at)+"/"+strconv.Itoa(total)+" for "+strconv.Quote(v.query))
	} else if v.query != "" {
		parts = append(parts, "no match for "+strconv.Quote(v.query))
	}
	return strings.Join(parts, " · ")
}

// View renders the file into r.
func (v Preview) View(w *fileview.Window, r layout.Rect, p theme.Palette) []string {
	switch {
	case v.path == "":
		return render.Rows([]string{textx.Fit(p.Dim().Render("  select a file"), r.W)}, r)
	case w == nil:
		return render.Rows([]string{textx.Fit(p.Dim().Render("  loading"), r.W)}, r)
	case w.Err() != nil:
		msg := p.Severity(theme.RoleSevere).Render("  " + textx.Sanitize(w.Err().Error()))
		return render.Rows([]string{textx.Fit(msg, r.W)}, r)
	}

	lines := w.Lines()
	if len(lines) == 0 {
		return render.Rows([]string{textx.Fit(p.Dim().Render("  empty file"), r.W)}, r)
	}

	start := min(max(v.offset, 0), max(len(lines)-1, 0))
	end := min(start+r.H, len(lines))
	body := max(r.W-gutterWidth, 1)

	out := make([]string, 0, r.H)
	for i := start; i < end; i++ {
		gutter := p.Dim().Render(padLeft(strconv.Itoa(i+1), gutterWidth-1) + " ")
		styled := v.styleLine(lines[i], i, p)
		out = append(out, textx.Fit(gutter+textx.Truncate(styled, body), r.W))
	}
	return render.Rows(out, r)
}

// styleLine applies syntax roles and search matches to one line. A match is
// drawn over the syntax rather than instead of it, so highlighting a match does
// not blank the structure a reader is using to navigate.
func (v Preview) styleLine(line fileview.Line, index int, p theme.Palette) string {
	var hits []textx.Span
	if v.query != "" {
		hits = textx.FindAll(line.Text, v.query)
	}
	if len(line.Spans) == 0 && len(hits) == 0 {
		return p.Body().Render(line.Text)
	}

	marks := make([]mark, 0, len(line.Spans)+len(hits))
	for _, s := range line.Spans {
		style := roleStyle(p, s.Role)
		marks = clipSpan(marks, s, hits, renderer(&style))
	}
	if len(hits) > 0 {
		hit := p.Match(len(v.matches) > 0 && v.matches[v.match] == index)
		for _, h := range hits {
			marks = append(marks, mark{h.Start, h.End, renderer(&hit)})
		}
	}
	slices.SortStableFunc(marks, func(a, b mark) int { return cmp.Compare(a.start, b.start) })

	var b strings.Builder
	at := 0
	for _, m := range marks {
		start, end := max(m.start, at), min(m.end, len(line.Text))
		if start >= end {
			continue
		}
		if start > at {
			b.WriteString(p.Body().Render(line.Text[at:start]))
		}
		b.WriteString(m.style(line.Text[start:end]))
		at = end
	}
	if at < len(line.Text) {
		b.WriteString(p.Body().Render(line.Text[at:]))
	}
	return b.String()
}

// mark is a byte range of a display line and the style that draws it.
type mark struct {
	start, end int
	style      func(string) string
}

// clipSpan appends the parts of a syntactic span that no match covers.
//
// A match that lands in the middle of a token has to be visible, and the token
// has to keep its colour on either side of the hit, or the reader loses the
// structure that tells them where in the document the hit is.
func clipSpan(marks []mark, s fileview.Span, hits []textx.Span, style func(string) string) []mark {
	at := s.Start
	for _, h := range hits {
		if h.End <= at || h.Start >= s.End {
			continue
		}
		if h.Start > at {
			marks = append(marks, mark{at, h.Start, style})
		}
		at = h.End
	}
	if at < s.End {
		marks = append(marks, mark{at, s.End, style})
	}
	return marks
}

// renderer adapts a style to a one-argument renderer, so the mark table holds
// one function shape whatever produced the style.
func renderer(st *lipgloss.Style) func(string) string {
	return func(s string) string { return st.Render(s) }
}

// roleStyle maps a syntactic role onto the palette.
func roleStyle(p theme.Palette, r fileview.Role) lipgloss.Style {
	switch r {
	case fileview.RoleKey:
		return p.JSON(theme.RoleKey)
	case fileview.RoleString:
		return p.JSON(theme.RoleString)
	case fileview.RoleNumber:
		return p.JSON(theme.RoleNumber)
	case fileview.RoleBool:
		return p.JSON(theme.RoleBool)
	case fileview.RoleNull:
		return p.JSON(theme.RoleNull)
	case fileview.RolePunct:
		return p.JSON(theme.RolePunct)
	case fileview.RoleTrace:
		return p.LogLevel(theme.RoleTrace)
	case fileview.RoleDebug:
		return p.LogLevel(theme.RoleDebug)
	case fileview.RoleInfo:
		return p.LogLevel(theme.RoleInfoLevel)
	case fileview.RoleWarn:
		return p.LogLevel(theme.RoleWarn)
	case fileview.RoleError:
		return p.LogLevel(theme.RoleErrorLevel)
	case fileview.RolePlain:
		return p.Body()
	default:
		return p.Body()
	}
}

func padLeft(s string, w int) string {
	if gap := w - len(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatInt(n/div, 10) + string("KMGT"[exp]) + "iB"
}
