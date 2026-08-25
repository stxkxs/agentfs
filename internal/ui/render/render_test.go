package render_test

import (
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Adversarial content a pane can be handed from a workspace it does not control.
var hostile = []string{
	strings.Repeat("x", 4000),
	"日本語のテキストが延々と続く行" + strings.Repeat("あ", 500),
	"👩‍👩‍👧‍👦 family emoji sequence",
	"\x1b]52;c;aGk=\x07escape attempt",
	"\u202ereversed",
	strings.Repeat("agent-researcher/", 200) + "state.json",
	"",
	"\t\ttabs",
}

func TestCanvasProducesExactlyTheTerminalShape(t *testing.T) {
	t.Parallel()
	p := theme.Plain()

	for _, term := range []layout.Rect{
		{W: 80, H: 24}, {W: 60, H: 16}, {W: 400, H: 200}, {W: 61, H: 17}, {W: 200, H: 50},
	} {
		frame := layout.Compute(term, layout.ModeBrowse)
		c := render.NewCanvas(term)
		for _, r := range frame.Panes() {
			c.Place(r, render.Box{Rect: r, Title: "pane"}.Render(p, hostile))
		}

		out := c.String()
		lines := strings.Split(out, "\n")
		if len(lines) != term.H {
			t.Fatalf("%dx%d produced %d lines, want %d", term.W, term.H, len(lines), term.H)
		}
		for i, line := range lines {
			if w := textx.Width(line); w != term.W {
				t.Errorf("%dx%d line %d is %d cells, want %d", term.W, term.H, i, w, term.W)
			}
		}
	}
}

func TestBoxReturnsItsExactRectangle(t *testing.T) {
	t.Parallel()
	p := theme.Plain()
	for _, r := range []layout.Rect{
		{W: 40, H: 10}, {W: 3, H: 3}, {W: 2, H: 2}, {W: 1, H: 1}, {W: 0, H: 0}, {W: 120, H: 40},
	} {
		lines := render.Box{Rect: r, Title: "Files", Badge: "12 items"}.Render(p, hostile)
		if r.W <= 0 || r.H <= 0 {
			if len(lines) != 0 {
				t.Errorf("an empty rect produced %d lines", len(lines))
			}
			continue
		}
		if len(lines) != r.H {
			t.Errorf("%dx%d box produced %d lines, want %d", r.W, r.H, len(lines), r.H)
		}
		for i, line := range lines {
			if w := textx.Width(line); w != r.W {
				t.Errorf("%dx%d box line %d is %d cells, want %d", r.W, r.H, i, w, r.W)
			}
		}
	}
}

func TestBoxTitleSurvivesWhenTheBadgeCannot(t *testing.T) {
	t.Parallel()
	p := theme.Plain()
	lines := render.Box{Rect: layout.Rect{W: 14, H: 4}, Title: "Preview", Badge: "1234 lines"}.Render(p, nil)
	if !strings.Contains(lines[0], "Preview") {
		t.Fatalf("the title was dropped in favour of the badge: %q", lines[0])
	}
	if strings.Contains(lines[0], "1234 lines") {
		t.Fatalf("a badge that does not fit was drawn anyway: %q", lines[0])
	}
}

func TestBoxBelowBorderSizeDegradesToPlainContent(t *testing.T) {
	t.Parallel()
	lines := render.Box{Rect: layout.Rect{W: 8, H: 2}, Title: "t"}.Render(theme.Plain(), []string{"body"})
	if len(lines) != 2 {
		t.Fatalf("produced %d lines, want 2", len(lines))
	}
	if strings.ContainsAny(lines[0], "╭╮╰╯") {
		t.Fatalf("a box too small for a border drew border fragments: %q", lines[0])
	}
	if !strings.Contains(lines[0], "body") {
		t.Fatalf("content was lost: %q", lines[0])
	}
}

func TestBoxInnerExcludesTheBorder(t *testing.T) {
	t.Parallel()
	b := render.Box{Rect: layout.Rect{X: 5, Y: 2, W: 20, H: 10}}
	inner := b.Inner()
	want := layout.Rect{X: 6, Y: 3, W: 18, H: 8}
	if inner != want {
		t.Fatalf("Inner = %+v, want %+v", inner, want)
	}
	if !b.Rect.Contains(inner) {
		t.Error("the content rect is not inside the box")
	}
}

func TestPlaceCorrectsAPaneThatReturnsTheWrongShape(t *testing.T) {
	t.Parallel()
	term := layout.Rect{W: 20, H: 4}
	c := render.NewCanvas(term)
	c.Place(layout.Rect{X: 0, Y: 0, W: 10, H: 2}, []string{"far too long for ten cells", "ok"})
	c.Place(layout.Rect{X: 10, Y: 0, W: 10, H: 2}, nil)

	for i, line := range strings.Split(c.String(), "\n") {
		if w := textx.Width(line); w != term.W {
			t.Errorf("line %d is %d cells, want %d: %q", i, w, term.W, line)
		}
	}
}

func TestRowAlwaysFitsItsWidth(t *testing.T) {
	t.Parallel()
	p := theme.Plain()
	for _, body := range hostile {
		for _, w := range []int{1, 2, 10, 80} {
			if got := textx.Width(render.Row(p, true, textx.Sanitize(body), w)); got != w {
				t.Errorf("Row(%q, %d) is %d cells", textx.Abbrev(body), w, got)
			}
		}
	}
}

// One idiom draws every cursor, so the marker cannot drift between panes.
func TestCursorIsOneCellAndOnlyWhenSelected(t *testing.T) {
	t.Parallel()
	p := theme.Plain()
	if got := textx.Width(render.Cursor(p, true)); got != 1 {
		t.Errorf("a selected cursor is %d cells, want 1", got)
	}
	if got := render.Cursor(p, false); got != " " {
		t.Errorf("an unselected cursor renders %q, want a space", got)
	}
}

func TestTooSmallRendersOneExplicitLine(t *testing.T) {
	t.Parallel()
	r := layout.Rect{W: 30, H: 3}
	lines := render.TooSmall(theme.Plain(), r)
	if len(lines) != r.H {
		t.Fatalf("produced %d lines, want %d", len(lines), r.H)
	}
	if !strings.Contains(lines[0], "too small") {
		t.Fatalf("the message does not name the condition: %q", lines[0])
	}
	for i, line := range lines {
		if w := textx.Width(line); w != r.W {
			t.Errorf("line %d is %d cells, want %d", i, w, r.W)
		}
	}
}

// Under the plain palette a frame carries no escape sequence, which is what
// makes a golden file readable and diffable.
//
// The content is sanitized first, as a pane sanitizes workspace bytes before it
// styles them. Sanitizing inside the box instead would strip the palette's own
// styling along with the workspace's.
func TestPlainPaletteEmitsNoEscapes(t *testing.T) {
	t.Parallel()
	safe := make([]string, len(hostile))
	for i, line := range hostile {
		safe[i] = textx.Sanitize(line)
	}

	term := layout.Rect{W: 80, H: 24}
	frame := layout.Compute(term, layout.ModeBrowse)
	c := render.NewCanvas(term)
	for _, r := range frame.Panes() {
		c.Place(r, render.Box{Rect: r, Title: "pane", Focused: true}.Render(theme.Plain(), safe))
	}
	if strings.ContainsRune(c.String(), 0x1B) {
		t.Fatal("the plain palette produced an escape sequence")
	}
}

// A styled palette must still fit its rectangle: styling contributes no width,
// and the fit is computed on cells rather than bytes.
func TestStyledFrameStillFitsItsRectangle(t *testing.T) {
	t.Parallel()
	safe := make([]string, len(hostile))
	for i, line := range hostile {
		safe[i] = textx.Sanitize(line)
	}

	term := layout.Rect{W: 100, H: 30}
	frame := layout.Compute(term, layout.ModeBrowse)
	c := render.NewCanvas(term)
	for _, r := range frame.Panes() {
		c.Place(r, render.Box{Rect: r, Title: "pane", Focused: true}.Render(theme.Dark(), safe))
	}
	for i, line := range strings.Split(c.String(), "\n") {
		if w := textx.Width(line); w != term.W {
			t.Errorf("styled line %d is %d cells, want %d", i, w, term.W)
		}
	}
}

func FuzzCanvasFit(f *testing.F) {
	f.Add("hello", 20, 5)
	f.Add("\x1b[31mred\x1b[0m", 3, 1)
	f.Add("日本語", 2, 2)
	// Grapheme_Cluster_Break=Prepend, where the first space of the pad Place
	// adds joins the cluster before it and occupies no cell. The class is held
	// as a seed because a seed corpus is what runs under `task test`.
	f.Add("\u0d4e", 20, 1)
	f.Add("\U000111c2", 20, 1)
	f.Add("\U000113d1", 20, 1)

	f.Fuzz(func(t *testing.T, content string, w, h int) {
		if w < 0 || w > 400 || h < 0 || h > 200 {
			t.Skip()
		}
		term := layout.Rect{W: w, H: h}
		c := render.NewCanvas(term)
		c.Place(layout.Rect{W: w, H: h}, []string{textx.Sanitize(content)})

		out := c.String()
		if h == 0 {
			return
		}
		for i, line := range strings.Split(out, "\n") {
			if got := textx.Width(line); got != w {
				t.Fatalf("line %d is %d cells, want %d", i, got, w)
			}
		}
	})
}

// A Grapheme_Cluster_Break=Prepend character joins the character after it into
// its own cluster, so the first space a pane's line is padded with occupies no
// cell. Canvas.row advances the column by each segment's declared width, so a
// line one cell short of that width draws the pane beside it a column early and
// leaves the frame's last column unwritten.
// A pane whose content already fills its rectangle takes no fill, so a cluster
// left open at its end has nothing to close it and takes a cell from the pane
// beside it. The absorbing rune is removed before the text is fitted, which is
// what holds the frame's columns where the layout put them.
func TestCanvasHoldsItsWidthWhenAFullPaneEndsInAnAbsorbingRune(t *testing.T) {
	t.Parallel()
	term := layout.Rect{W: 20, H: 1}
	c := render.NewCanvas(term)
	c.Place(layout.Rect{X: 0, Y: 0, W: 10, H: 1}, []string{textx.Sanitize("abcdefghi\u0d4e")})
	c.Place(layout.Rect{X: 10, Y: 0, W: 10, H: 1}, []string{"RIGHT"})

	line := c.String()
	if w := textx.Width(line); w != term.W {
		t.Fatalf("line is %d cells, want %d: %q", w, term.W, line)
	}
	i := strings.Index(line, "RIGHT")
	if i < 0 {
		t.Fatalf("the right pane was not drawn: %q", line)
	}
	if col := textx.Width(line[:i]); col != 10 {
		t.Fatalf("the right pane starts at column %d, want 10: %q", col, line)
	}
}

// A bordered box draws its right border behind the fitted content, so an open
// cluster at the content's end swallows the border rather than a neighbour.
func TestBoxKeepsItsRightBorderWhenContentEndsInAnAbsorbingRune(t *testing.T) {
	t.Parallel()
	for _, w := range []int{20, 40, 100} {
		box := render.Box{Rect: layout.Rect{W: w, H: 3}}
		content := textx.Sanitize(strings.Repeat("y", box.Inner().W-1) + "\u0d4e")
		for i, line := range box.Render(theme.Plain(), []string{content}) {
			if got := textx.Width(line); got != w {
				t.Errorf("width %d: line %d is %d cells: %q", w, i, got, line)
			}
		}
	}
}

func TestCanvasHoldsItsWidthWhenAClusterAbsorbsThePad(t *testing.T) {
	t.Parallel()
	term := layout.Rect{W: 20, H: 1}
	c := render.NewCanvas(term)
	c.Place(layout.Rect{X: 0, Y: 0, W: 10, H: 1}, []string{"ൎ"})
	c.Place(layout.Rect{X: 10, Y: 0, W: 10, H: 1}, []string{"RIGHT"})

	line := c.String()
	if w := textx.Width(line); w != term.W {
		t.Fatalf("line is %d cells, want %d: %q", w, term.W, line)
	}
	i := strings.Index(line, "RIGHT")
	if i < 0 {
		t.Fatalf("the right pane was not drawn: %q", line)
	}
	if col := textx.Width(line[:i]); col != 10 {
		t.Fatalf("the right pane starts at column %d, want 10: %q", col, line)
	}
}
