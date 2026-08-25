package pane

import (
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Tree renders the workspace index.
type Tree struct {
	scroller
}

// Selected returns the node under the cursor.
func (t Tree) Selected(ix *index.Index) (*index.Node, bool) {
	rows := ix.Rows()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return nil, false
	}
	return rows[t.cursor].Node, true
}

// Update applies a navigation or disclosure action, reporting whether the
// selection changed. Expanding reads at most one directory.
func (t *Tree) Update(a keys.Action, ix *index.Index, height int) bool {
	rows := ix.Rows()
	if t.apply(a, len(rows), height) {
		return true
	}

	node, ok := t.Selected(ix)
	if !ok {
		return false
	}
	switch a {
	case keys.ActionExpand, keys.ActionOpen:
		if !node.IsDir {
			// Opening a file is the application's business: the tree reports
			// the selection and the preview loads it.
			return false
		}
		ix.Expand(node.Path)
		t.clamp(len(ix.Rows()), height)
		return true

	case keys.ActionCollapse:
		if node.IsDir && node.Expanded() {
			ix.Collapse(node.Path)
		} else if parent := parentOf(node.Path); parent != "." {
			// Collapsing a leaf closes its parent and moves to it, so one key
			// walks back up rather than requiring the reader to find the
			// parent row first.
			ix.Collapse(parent)
			t.Select(ix, parent, height)
		}
		t.clamp(len(ix.Rows()), height)
		return true

	default:
		return false
	}
}

// Select moves the cursor onto p and reports whether the tree is showing it. A
// path below a closed directory is not a row, so the cursor stays where it is
// until the caller opens the directories above it.
func (t *Tree) Select(ix *index.Index, p string, height int) bool {
	for i, row := range ix.Rows() {
		if row.Node.Path == p {
			t.cursor = i
			t.clamp(len(ix.Rows()), height)
			return true
		}
	}
	return false
}

func parentOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

// Badge summarizes what the pane holds, so a reader can tell whether the tree
// is showing everything before deciding to trust it.
func (t Tree) Badge(ix *index.Index) string {
	st := ix.Stats()
	parts := []string{strconv.Itoa(st.Rows) + " rows"}
	if st.TruncatedDirs > 0 {
		parts = append(parts, strconv.Itoa(st.TruncatedDirs)+" capped")
	}
	if st.UnreadableDirs > 0 {
		parts = append(parts, strconv.Itoa(st.UnreadableDirs)+" unreadable")
	}
	if st.NodeCeilingHit {
		parts = append(parts, "at node ceiling")
	}
	return strings.Join(parts, " · ")
}

// View renders the tree into r.
func (t Tree) View(ix *index.Index, r layout.Rect, p theme.Palette, recent func(*index.Node) bool) []string {
	rows := ix.Rows()
	start, end := t.window(len(rows), r.H)

	out := make([]string, 0, r.H)
	if len(rows) == 0 {
		out = append(out, textx.Fit(p.Dim().Render("  empty workspace"), r.W))
		return render.Rows(out, r)
	}

	g := p.Glyphs()
	for i := start; i < end; i++ {
		row := rows[i]
		n := row.Node

		var b strings.Builder
		b.WriteString(strings.Repeat("  ", row.Depth))
		b.WriteString(t.disclosure(n, g))
		b.WriteByte(' ')
		b.WriteString(t.label(n, p, g, recent(n)))
		out = append(out, render.Row(p, i == t.cursor, b.String(), r.W))
	}
	return render.Rows(out, r)
}

// disclosure returns the marker for a node's children, which names whether
// there is anything behind it.
func (Tree) disclosure(n *index.Node, g theme.Glyphs) string {
	if !n.IsDir {
		return g.Leaf
	}
	if n.Expanded() {
		return g.Expanded
	}
	return g.Collapsed
}

// label renders a node's name with the notes that let a reader decide whether
// to open it: how many members a closed directory holds, whether its content
// was capped, and whether it changed.
func (Tree) label(n *index.Node, p theme.Palette, g theme.Glyphs, recent bool) string {
	name := textx.Sanitize(n.Name)

	var styled string
	switch {
	case n.IsLink:
		styled = p.Dim().Render(name + "@")
	case n.IsDir:
		styled = p.Directory().Render(name + "/")
	case recent:
		styled = p.Recent().Render(name)
	default:
		styled = p.Body().Render(name)
	}

	var notes []string
	if recent {
		// Recency is carried by a glyph as well as a colour, because the
		// colour is absent under the plain palette and on a monochrome
		// terminal.
		notes = append(notes, g.Recent)
	}
	if n.IsDir && !n.Expanded() {
		if count, known := n.ChildCount(); known {
			notes = append(notes, strconv.Itoa(count))
		}
	}
	if n.Truncated {
		notes = append(notes, g.Truncated+" capped")
	}
	if n.Unreadable != nil {
		notes = append(notes, "unreadable")
	}
	if len(notes) == 0 {
		return styled
	}
	return styled + " " + p.Dim().Render("("+strings.Join(notes, " ")+")")
}
