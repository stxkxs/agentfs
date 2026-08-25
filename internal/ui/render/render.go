// Package render is the boundary every pane's output crosses on its way to the
// screen.
//
// A pane returns lines; [Canvas.Place] clips and pads them to the rectangle the
// layout gave it. Enforcing the fit here rather than in each pane means a pane
// cannot overflow its box by forgetting to clamp, which is the failure that
// makes one long log line destroy a whole frame.
//
// Panes hand this package styled text, so it must not sanitize: doing so would
// strip the palette's own escape sequences along with the workspace's. A pane
// runs [textx.Sanitize] over workspace bytes before it styles them, and the
// pane tests assert that no raw escape survives that path.
package render

import (
	"strings"

	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Canvas composes rendered panes into one frame.
//
// Panes tile the terminal without overlapping, so the frame is assembled row by
// row from the segments that occupy each row. Nothing is overwritten, which
// makes "no cell is claimed twice" a property of the assembly rather than a
// convention the panes have to observe.
type Canvas struct {
	w, h int
	rows [][]segment
}

type segment struct {
	x, w int
	text string
}

// NewCanvas returns a canvas the size of term.
func NewCanvas(term layout.Rect) *Canvas {
	w, h := max(term.W, 0), max(term.H, 0)
	return &Canvas{w: w, h: h, rows: make([][]segment, h)}
}

// Place writes lines into r. Each line is clipped or padded to exactly r.W
// cells, and the block is clipped or padded to exactly r.H lines, so a pane
// that returns the wrong shape is corrected rather than allowed to shift its
// neighbours.
func (c *Canvas) Place(r layout.Rect, lines []string) {
	if r.Empty() {
		return
	}
	for i := range r.H {
		y := r.Y + i
		if y < 0 || y >= c.h {
			continue
		}
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		c.rows[y] = insert(c.rows[y], segment{x: r.X, w: r.W, text: textx.Fit(line, r.W)})
	}
}

// insert keeps a row's segments ordered by column, so assembly is a
// concatenation rather than a sort.
func insert(row []segment, s segment) []segment {
	for i, existing := range row {
		if s.x < existing.x {
			return append(row[:i], append([]segment{s}, row[i:]...)...)
		}
	}
	return append(row, s)
}

// String assembles the frame. Every line is exactly the canvas width, and there
// are exactly as many lines as its height.
func (c *Canvas) String() string {
	var b strings.Builder
	for y := range c.h {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(c.row(y))
	}
	return b.String()
}

func (c *Canvas) row(y int) string {
	var b strings.Builder
	col := 0
	for _, s := range c.rows[y] {
		if s.x > col {
			b.WriteString(strings.Repeat(" ", s.x-col))
			col = s.x
		}
		if s.x < col {
			// A later segment starting inside an earlier one would mean the
			// layout handed two panes the same cell.
			continue
		}
		b.WriteString(s.text)
		col += s.w
	}
	if col < c.w {
		b.WriteString(strings.Repeat(" ", c.w-col))
	}
	return b.String()
}

// Box draws a bordered pane.
type Box struct {
	// Rect is where the box is drawn, borders included.
	Rect layout.Rect
	// Title names the pane. It is drawn into the top border.
	Title string
	// Badge is drawn at the right of the top border: a count, a mode, or a
	// condition. It carries the information scent a reader needs to decide
	// whether to look inside, so a pane with hidden content says how much.
	Badge string
	// Focused draws the border in the focus colour.
	Focused bool
}

// Inner returns the rectangle available to content, which is the box less its
// borders.
func (b Box) Inner() layout.Rect {
	if b.Rect.W < 3 || b.Rect.H < 3 {
		return layout.Rect{X: b.Rect.X, Y: b.Rect.Y, W: max(b.Rect.W, 0), H: max(b.Rect.H, 0)}
	}
	return layout.Rect{X: b.Rect.X + 1, Y: b.Rect.Y + 1, W: b.Rect.W - 2, H: b.Rect.H - 2}
}

// Render returns exactly Rect.H lines of exactly Rect.W cells, with content
// drawn inside the border.
func (b Box) Render(p theme.Palette, content []string) []string {
	if b.Rect.W < 3 || b.Rect.H < 3 {
		// A box too small to carry a border carries content alone, so a narrow
		// terminal degrades to plain text rather than to border fragments.
		return fitBlock(content, b.Rect.W, b.Rect.H)
	}

	border := p.Border(b.Focused)
	inner := b.Rect.W - 2
	out := make([]string, 0, b.Rect.H)
	out = append(out, border.Render("╭")+b.topBorder(p, inner)+border.Render("╮"))

	body := fitBlock(content, inner, b.Rect.H-2)
	for _, line := range body {
		out = append(out, border.Render("│")+line+border.Render("│"))
	}
	out = append(out, border.Render("╰")+border.Render(strings.Repeat("─", inner))+border.Render("╯"))
	return out
}

// topBorder draws the title at the left and the badge at the right, filling the
// span between them with the border rule.
func (b Box) topBorder(p theme.Palette, width int) string {
	border := p.Border(b.Focused)
	title := ""
	if b.Title != "" {
		title = " " + textx.Sanitize(b.Title) + " "
	}
	badge := ""
	if b.Badge != "" {
		badge = " " + textx.Sanitize(b.Badge) + " "
	}

	// The badge yields to the title: a pane that cannot show both shows what it
	// is before it shows how much it holds.
	if textx.Width(title)+textx.Width(badge)+2 > width {
		badge = ""
	}
	if textx.Width(title)+2 > width {
		title = textx.Truncate(title, max(width-2, 0))
	}

	fill := width - textx.Width(title) - textx.Width(badge) - 1
	if fill < 0 {
		fill = 0
	}
	var sb strings.Builder
	sb.WriteString(border.Render("─"))
	sb.WriteString(p.Title().Render(title))
	sb.WriteString(border.Render(strings.Repeat("─", fill)))
	sb.WriteString(p.Dim().Render(badge))
	return textx.Fit(sb.String(), width)
}

// fitBlock returns exactly h lines of exactly w cells.
func fitBlock(lines []string, w, h int) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	out := make([]string, h)
	for i := range h {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		out[i] = textx.Fit(line, w)
	}
	return out
}

// Rows returns exactly h lines of exactly w cells, for a pane that draws
// without a border.
func Rows(lines []string, r layout.Rect) []string { return fitBlock(lines, r.W, r.H) }

// Cursor marks the selected row.
//
// One idiom draws every cursor in agentfs. A pane that drew its own would
// produce a marker that drifts from the others in width or colour, and a
// reader would have to learn each pane's separately.
func Cursor(p theme.Palette, selected bool) string {
	if selected {
		return p.Cursor().Render(p.Glyphs().Cursor)
	}
	return " "
}

// Row composes a cursor marker and a body into one line of exactly width cells.
func Row(p theme.Palette, selected bool, body string, width int) string {
	if width <= 0 {
		return ""
	}
	marker := Cursor(p, selected)
	return textx.Fit(marker+" "+body, width)
}

// TooSmall renders the message a terminal below the layout's minimum receives.
// One explicit line is more use than panes clamped into fragments.
func TooSmall(p theme.Palette, r layout.Rect) []string {
	msg := "terminal too small — agentfs needs " +
		itoa(layout.MinWidth) + "×" + itoa(layout.MinHeight)
	lines := make([]string, max(r.H, 0))
	for i := range lines {
		lines[i] = textx.Fit("", r.W)
	}
	if len(lines) > 0 {
		lines[0] = textx.Fit(p.Dim().Render(msg), r.W)
	}
	return lines
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
