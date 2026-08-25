package layout_test

import (
	"testing"

	"github.com/stxkxs/agentfs/internal/ui/layout"
)

// The sweep spans every terminal size a reasonable display offers, from a
// single cell to a wall-sized one, in every mode.
const (
	sweepWidth  = 400
	sweepHeight = 200
)

// The rules the package documents, restated here in literal numbers so a change
// to a proportion has to be made on both sides.
const (
	leftPercent      = 30
	leftFloor        = 28
	leftCeiling      = 60
	previewPercent   = 60
	rowsAboveContent = 2 // the title and the agent bar
	chromeRows       = 3 // those two and the status line
)

type namedRect struct {
	name string
	r    layout.Rect
}

// The overlay modes and the pane each one fills, restated here so a mode that
// gains a pane has to be declared on both sides.
var overlays = map[layout.Mode]string{
	layout.ModeHelp:    "help",
	layout.ModeBudgets: "budgets",
}

// overlayMode reports whether the mode gives the content region to one pane.
func overlayMode(m layout.Mode) bool { _, ok := overlays[m]; return ok }

// overlayName is the pane an overlay mode fills.
func overlayName(m layout.Mode) string { return overlays[m] }

// overlayPane returns the mode that fills the named pane, and whether the pane
// is an overlay at all.
func overlayPane(name string) (layout.Mode, bool) {
	for mode, pane := range overlays {
		if pane == name {
			return mode, true
		}
	}
	return layout.ModeBrowse, false
}

func fields(f *layout.Frame) []namedRect {
	return []namedRect{
		{"title", f.Title},
		{"agentbar", f.AgentBar},
		{"left", f.Left},
		{"preview", f.Preview},
		{"feed", f.Feed},
		{"help", f.Help},
		{"budgets", f.Budgets},
		{"status", f.Status},
	}
}

// checkFrame asserts every invariant that holds for any terminal and any mode.
func checkFrame(t *testing.T, term layout.Rect, mode layout.Mode, f *layout.Frame) {
	t.Helper()

	wantTerm := layout.Rect{X: term.X, Y: term.Y, W: max(term.W, 0), H: max(term.H, 0)}
	if f.Term != wantTerm {
		t.Fatalf("Compute(%+v,%v).Term = %+v, want %+v", term, mode, f.Term, wantTerm)
	}

	if want := f.Term.W < layout.MinWidth || f.Term.H < layout.MinHeight; f.TooSmall != want {
		t.Fatalf("Compute(%+v,%v).TooSmall = %v, want %v", term, mode, f.TooSmall, want)
	}

	for _, nr := range fields(f) {
		if nr.r.W < 0 || nr.r.H < 0 {
			t.Fatalf("Compute(%+v,%v) gave %s negative extent %+v", term, mode, nr.name, nr.r)
		}
		if nr.r.W > f.Term.W || nr.r.H > f.Term.H {
			t.Fatalf("Compute(%+v,%v) gave %s %+v, larger than the terminal", term, mode, nr.name, nr.r)
		}
		if !f.Term.Contains(nr.r) {
			t.Fatalf("Compute(%+v,%v) placed %s %+v outside %+v", term, mode, nr.name, nr.r, f.Term)
		}
	}

	panes := f.Panes()
	for i, a := range panes {
		if a.Empty() {
			t.Fatalf("Compute(%+v,%v).Panes() returned the empty rect %+v", term, mode, a)
		}
		for _, b := range panes[i+1:] {
			if a.Overlaps(b) {
				t.Fatalf("Compute(%+v,%v) overlapping panes %+v and %+v", term, mode, a, b)
			}
		}
	}

	if f.TooSmall {
		checkCollapsed(t, term, mode, f)
		return
	}

	// The mode decides which of the content panes exist, and nothing else. An
	// overlay mode carries its own pane and none of the browse panes; a browse
	// mode carries the browse panes and no overlay.
	for _, nr := range []namedRect{{"left", f.Left}, {"preview", f.Preview}, {"feed", f.Feed}} {
		if nr.r.Empty() != overlayMode(mode) {
			t.Fatalf("Compute(%+v,%v) gave %s %+v", term, mode, nr.name, nr.r)
		}
	}
	for _, nr := range []namedRect{{"help", f.Help}, {"budgets", f.Budgets}} {
		owner, _ := overlayPane(nr.name)
		if nr.r.Empty() != (mode != owner) {
			t.Fatalf("Compute(%+v,%v) gave %s %+v", term, mode, nr.name, nr.r)
		}
	}

	checkProportions(t, term, mode, f)

	// No overlaps plus an area equal to the terminal's is an exact tiling: no
	// cell is claimed twice and none is left for nothing to paint.
	area := 0
	for _, p := range panes {
		area += p.W * p.H
	}
	if want := f.Term.W * f.Term.H; area != want {
		t.Fatalf("Compute(%+v,%v) panes cover %d cells of %d", term, mode, area, want)
	}
}

// checkCollapsed asserts the too-small contract: one line, and nothing else.
func checkCollapsed(t *testing.T, term layout.Rect, mode layout.Mode, f *layout.Frame) {
	t.Helper()
	for _, nr := range fields(f) {
		if nr.name == "status" {
			continue
		}
		if !nr.r.Empty() {
			t.Fatalf("Compute(%+v,%v) is too small yet carries %s %+v", term, mode, nr.name, nr.r)
		}
	}
	want := layout.Rect{X: term.X, Y: term.Y, W: f.Term.W, H: min(f.Term.H, 1)}
	if f.Status != want {
		t.Fatalf("Compute(%+v,%v).Status = %+v, want the single line %+v", term, mode, f.Status, want)
	}
}

// checkProportions asserts where every boundary of a frame that carries the
// layout falls: three chrome rows around a content region, the left column on
// leftPercent of the width held between leftFloor and leftCeiling, and the
// preview on previewPercent of the content region with the feed taking the
// rest. Asserting it here rather than in a test of its own puts it on every
// size the sweep and the fuzz reach, so a split that is right at one size and
// wrong at another cannot pass.
func checkProportions(t *testing.T, term layout.Rect, mode layout.Mode, f *layout.Frame) {
	t.Helper()

	contentY := f.Term.Y + rowsAboveContent
	contentH := f.Term.H - chromeRows
	want := map[string]layout.Rect{
		"title":    {X: f.Term.X, Y: f.Term.Y, W: f.Term.W, H: 1},
		"agentbar": {X: f.Term.X, Y: f.Term.Y + 1, W: f.Term.W, H: 1},
		"status":   {X: f.Term.X, Y: f.Term.Y + f.Term.H - 1, W: f.Term.W, H: 1},
	}

	if overlayMode(mode) {
		want[overlayName(mode)] = layout.Rect{X: f.Term.X, Y: contentY, W: f.Term.W, H: contentH}
	} else {
		leftW := min(max(f.Term.W*leftPercent/100, leftFloor), leftCeiling)
		rightW := f.Term.W - leftW
		previewH := contentH * previewPercent / 100
		want["left"] = layout.Rect{X: f.Term.X, Y: contentY, W: leftW, H: contentH}
		want["preview"] = layout.Rect{X: f.Term.X + leftW, Y: contentY, W: rightW, H: previewH}
		want["feed"] = layout.Rect{X: f.Term.X + leftW, Y: contentY + previewH, W: rightW, H: contentH - previewH}
	}

	for _, nr := range fields(f) {
		w, ruled := want[nr.name]
		if !ruled {
			continue
		}
		if nr.r != w {
			t.Fatalf("Compute(%+v,%v) %s = %+v, want %+v", term, mode, nr.name, nr.r, w)
		}
		if nr.r.Empty() {
			t.Fatalf("Compute(%+v,%v) carries the layout yet %s %+v holds no cells", term, mode, nr.name, nr.r)
		}
	}
}

func TestComputeHoldsItsInvariantsAtEverySize(t *testing.T) {
	t.Parallel()
	for _, mode := range layout.Modes() {
		for w := 1; w <= sweepWidth; w++ {
			for h := 1; h <= sweepHeight; h++ {
				term := layout.Rect{W: w, H: h}
				f := layout.Compute(term, mode)
				checkFrame(t, term, mode, &f)
				if again := layout.Compute(term, mode); again != f {
					t.Fatalf("Compute(%+v,%v) returned %+v then %+v", term, mode, f, again)
				}
			}
		}
	}
}

func TestTooSmallTracksTheMinimumExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		w, h int
		want bool
	}{
		{layout.MinWidth, layout.MinHeight, false},
		{layout.MinWidth - 1, layout.MinHeight, true},
		{layout.MinWidth, layout.MinHeight - 1, true},
		{layout.MinWidth - 1, layout.MinHeight - 1, true},
		{layout.MinWidth + 1, layout.MinHeight + 1, false},
		{1, 1, true},
		{0, 0, true},
		{-5, -5, true},
		{sweepWidth, layout.MinHeight - 1, true},
		{layout.MinWidth - 1, sweepHeight, true},
	}
	for _, mode := range layout.Modes() {
		for _, tc := range cases {
			term := layout.Rect{W: tc.w, H: tc.h}
			f := layout.Compute(term, mode)
			if f.TooSmall != tc.want {
				t.Errorf("Compute(%dx%d,%v).TooSmall = %v, want %v", tc.w, tc.h, mode, f.TooSmall, tc.want)
			}
			checkFrame(t, term, mode, &f)
		}
	}
}

func TestTooSmallLeavesOneLineToWriteOn(t *testing.T) {
	t.Parallel()
	f := layout.Compute(layout.Rect{W: 40, H: 10}, layout.ModeBrowse)
	panes := f.Panes()
	if len(panes) != 1 {
		t.Fatalf("Panes() = %+v, want the status line alone", panes)
	}
	if panes[0] != (layout.Rect{W: 40, H: 1}) {
		t.Fatalf("Panes()[0] = %+v, want the top row across the terminal", panes[0])
	}
}

// A terminal with no cells has nowhere to put even the degradation line.
func TestAnEmptyTerminalCarriesNoPanes(t *testing.T) {
	t.Parallel()
	for _, term := range []layout.Rect{{}, {W: 0, H: 40}, {W: 40, H: 0}, {W: -9, H: -9}} {
		for _, mode := range layout.Modes() {
			f := layout.Compute(term, mode)
			if panes := f.Panes(); len(panes) != 0 {
				t.Errorf("Compute(%+v,%v).Panes() = %+v, want none", term, mode, panes)
			}
			checkFrame(t, term, mode, &f)
		}
	}
}

func TestFrameGeometry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		term layout.Rect
		mode layout.Mode
		want layout.Frame
	}{
		{
			name: "the smallest terminal that carries the layout",
			term: layout.Rect{W: 60, H: 16},
			mode: layout.ModeBrowse,
			want: layout.Frame{
				Term:     layout.Rect{W: 60, H: 16},
				Title:    layout.Rect{W: 60, H: 1},
				AgentBar: layout.Rect{Y: 1, W: 60, H: 1},
				Left:     layout.Rect{Y: 2, W: 28, H: 13},
				Preview:  layout.Rect{X: 28, Y: 2, W: 32, H: 7},
				Feed:     layout.Rect{X: 28, Y: 9, W: 32, H: 6},
				Status:   layout.Rect{Y: 15, W: 60, H: 1},
			},
		},
		{
			name: "a common terminal",
			term: layout.Rect{W: 80, H: 24},
			mode: layout.ModeBrowse,
			want: layout.Frame{
				Term:     layout.Rect{W: 80, H: 24},
				Title:    layout.Rect{W: 80, H: 1},
				AgentBar: layout.Rect{Y: 1, W: 80, H: 1},
				Left:     layout.Rect{Y: 2, W: 28, H: 21},
				Preview:  layout.Rect{X: 28, Y: 2, W: 52, H: 12},
				Feed:     layout.Rect{X: 28, Y: 14, W: 52, H: 9},
				Status:   layout.Rect{Y: 23, W: 80, H: 1},
			},
		},
		{
			name: "the left column on its percentage",
			term: layout.Rect{W: 100, H: 30},
			mode: layout.ModeRuns,
			want: layout.Frame{
				Term:     layout.Rect{W: 100, H: 30},
				Title:    layout.Rect{W: 100, H: 1},
				AgentBar: layout.Rect{Y: 1, W: 100, H: 1},
				Left:     layout.Rect{Y: 2, W: 30, H: 27},
				Preview:  layout.Rect{X: 30, Y: 2, W: 70, H: 16},
				Feed:     layout.Rect{X: 30, Y: 18, W: 70, H: 11},
				Status:   layout.Rect{Y: 29, W: 100, H: 1},
			},
		},
		{
			name: "the left column at its ceiling",
			term: layout.Rect{W: 400, H: 200},
			mode: layout.ModeBrowse,
			want: layout.Frame{
				Term:     layout.Rect{W: 400, H: 200},
				Title:    layout.Rect{W: 400, H: 1},
				AgentBar: layout.Rect{Y: 1, W: 400, H: 1},
				Left:     layout.Rect{Y: 2, W: 60, H: 197},
				Preview:  layout.Rect{X: 60, Y: 2, W: 340, H: 118},
				Feed:     layout.Rect{X: 60, Y: 120, W: 340, H: 79},
				Status:   layout.Rect{Y: 199, W: 400, H: 1},
			},
		},
		{
			name: "help takes the content region whole",
			term: layout.Rect{W: 80, H: 24},
			mode: layout.ModeHelp,
			want: layout.Frame{
				Term:     layout.Rect{W: 80, H: 24},
				Title:    layout.Rect{W: 80, H: 1},
				AgentBar: layout.Rect{Y: 1, W: 80, H: 1},
				Help:     layout.Rect{Y: 2, W: 80, H: 21},
				Status:   layout.Rect{Y: 23, W: 80, H: 1},
			},
		},
		{
			name: "a terminal below the minimum",
			term: layout.Rect{W: 59, H: 15},
			mode: layout.ModeBrowse,
			want: layout.Frame{
				Term:     layout.Rect{W: 59, H: 15},
				Status:   layout.Rect{W: 59, H: 1},
				TooSmall: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := layout.Compute(tc.term, tc.mode); got != tc.want {
				t.Errorf("Compute(%+v,%v) =\n\t%+v\nwant\n\t%+v", tc.term, tc.mode, got, tc.want)
			}
		})
	}
}

// Browse and runs differ in what the left pane lists, not in where it sits, so
// switching between them must not move a boundary.
func TestBrowseAndRunsShareOneGeometry(t *testing.T) {
	t.Parallel()
	for w := 1; w <= sweepWidth; w++ {
		for h := 1; h <= sweepHeight; h++ {
			term := layout.Rect{W: w, H: h}
			if browse, runs := layout.Compute(term, layout.ModeBrowse), layout.Compute(term, layout.ModeRuns); browse != runs {
				t.Fatalf("Compute(%+v) browse %+v, runs %+v", term, browse, runs)
			}
		}
	}
}

// The widths where each rule in the left column's clamp decides it. The sweep
// holds the rule at every width; this names the widths worth reading.
func TestTheLeftColumnIsThirtyPercentBetweenTwentyEightAndSixty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		w, want int
		rule    string
	}{
		{layout.MinWidth, 28, "the floor, since 30% of 60 columns is 18"},
		{93, 28, "the floor, since 30% of 93 columns is 27"},
		{100, 30, "the percentage"},
		{150, 45, "the percentage"},
		{200, 60, "the percentage, arriving at the ceiling"},
		{sweepWidth, 60, "the ceiling, since 30% of 400 columns is 120"},
	}
	for _, tc := range cases {
		f := layout.Compute(layout.Rect{W: tc.w, H: layout.MinHeight}, layout.ModeBrowse)
		if f.Left.W != tc.want {
			t.Errorf("width %d: left column %d, want %d from %s", tc.w, f.Left.W, tc.want, tc.rule)
		}
		if right := tc.w - tc.want; f.Preview.W != right || f.Feed.W != right {
			t.Errorf("width %d: preview %d and feed %d, want %d each", tc.w, f.Preview.W, f.Feed.W, right)
		}
	}
}

// The heights worth reading in the preview and feed split, including the one
// where the percentage divides the content region evenly.
func TestThePreviewIsSixtyPercentOfTheContentRegion(t *testing.T) {
	t.Parallel()
	cases := []struct{ h, preview, feed int }{
		{layout.MinHeight, 7, 6},
		{24, 12, 9},
		{53, 30, 20},
		{sweepHeight, 118, 79},
	}
	for _, tc := range cases {
		f := layout.Compute(layout.Rect{W: layout.MinWidth, H: tc.h}, layout.ModeBrowse)
		if f.Preview.H != tc.preview || f.Feed.H != tc.feed {
			t.Errorf("height %d: preview %d and feed %d rows, want %d and %d",
				tc.h, f.Preview.H, f.Feed.H, tc.preview, tc.feed)
		}
		if content := f.Preview.H + f.Feed.H; content != tc.h-chromeRows {
			t.Errorf("height %d: preview and feed cover %d rows, want %d", tc.h, content, tc.h-chromeRows)
		}
	}
}

// MinWidth and MinHeight are the size a caller names in the line it renders for
// a too-small terminal, so the boundary is pinned to the size itself: 60 by 16
// carries the layout and one cell less in either direction does not.
func TestTheSmallestTerminalIsSixtyBySixteen(t *testing.T) {
	t.Parallel()
	f := layout.Compute(layout.Rect{W: 60, H: 16}, layout.ModeBrowse)
	if f.TooSmall {
		t.Fatalf("Compute(60x16) refused the terminal")
	}
	for _, nr := range fields(&f) {
		if _, overlay := overlayPane(nr.name); overlay {
			continue
		}
		if nr.r.Empty() {
			t.Errorf("Compute(60x16) gave %s %+v, which holds no cells", nr.name, nr.r)
		}
	}
	for _, term := range []layout.Rect{{W: 59, H: 16}, {W: 60, H: 15}} {
		if got := layout.Compute(term, layout.ModeBrowse); !got.TooSmall {
			t.Errorf("Compute(%+v) carried the layout", term)
		}
	}
}

// Only the terminal's size shapes the frame; its origin translates the result.
// A caller nesting agentfs inside a larger surface relies on that.
func TestOriginTranslatesTheFrame(t *testing.T) {
	t.Parallel()
	offsets := []layout.Rect{{X: 1, Y: 1}, {X: 7, Y: 3}, {X: -4, Y: -9}, {X: 500, Y: 500}}
	sizes := []layout.Rect{{W: 1, H: 1}, {W: 60, H: 16}, {W: 80, H: 24}, {W: 400, H: 200}}
	for _, mode := range layout.Modes() {
		for _, size := range sizes {
			base := layout.Compute(size, mode)
			origin := fields(&base)
			for _, off := range offsets {
				term := layout.Rect{X: off.X, Y: off.Y, W: size.W, H: size.H}
				got := layout.Compute(term, mode)
				checkFrame(t, term, mode, &got)
				moved := fields(&got)
				for i, want := range origin {
					shifted := want.r
					if !shifted.Empty() {
						shifted.X += off.X
						shifted.Y += off.Y
					}
					if moved[i].r != shifted {
						t.Fatalf("Compute(%+v,%v) %s = %+v, want %+v translated to %+v",
							term, mode, want.name, moved[i].r, want.r, shifted)
					}
				}
			}
		}
	}
}

func TestPanesAreInReadingOrder(t *testing.T) {
	t.Parallel()
	f := layout.Compute(layout.Rect{W: 80, H: 24}, layout.ModeBrowse)
	want := []layout.Rect{f.Title, f.AgentBar, f.Left, f.Preview, f.Feed, f.Status}
	got := f.Panes()
	if len(got) != len(want) {
		t.Fatalf("Panes() = %+v, want %d panes", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Panes()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPanesOmitsTheEmptyPanes(t *testing.T) {
	t.Parallel()
	f := layout.Compute(layout.Rect{W: 80, H: 24}, layout.ModeHelp)
	for _, p := range f.Panes() {
		if p == f.Left || p == f.Preview || p == f.Feed {
			t.Fatalf("Panes() = %+v, which includes an empty content pane", f.Panes())
		}
	}
	if len(f.Panes()) != 4 {
		t.Fatalf("Panes() = %+v, want title, agent bar, help and status", f.Panes())
	}
}

// An unrecognised mode is arranged as browse rather than left without panes, so
// a caller adding to the vocabulary sees a frame before Compute names its mode.
func TestAnUnknownModeCarriesTheSplitArrangement(t *testing.T) {
	t.Parallel()
	term := layout.Rect{W: 80, H: 24}
	got := layout.Compute(term, layout.Mode(9))
	if want := layout.Compute(term, layout.ModeBrowse); got != want {
		t.Fatalf("Compute(%+v,Mode(9)) = %+v, want %+v", term, got, want)
	}
	checkFrame(t, term, layout.ModeBrowse, &got)
}

func FuzzCompute(f *testing.F) {
	f.Add(0, 0, 80, 24, int(layout.ModeBrowse))
	f.Add(0, 0, 60, 16, int(layout.ModeHelp))
	f.Add(-3, -9, 1, 1, int(layout.ModeRuns))
	f.Add(5, 7, -40, -40, 9)
	f.Add(1<<20, 1<<20, 1<<20, 1<<20, -1)

	f.Fuzz(func(t *testing.T, x, y, w, h, m int) {
		// A coordinate is a terminal cell, so hold the inputs where the sum of
		// an origin and an extent stays a number a terminal could name.
		const bound = 1 << 20
		clamp := func(v int) int { return min(max(v, -bound), bound) }
		term := layout.Rect{X: clamp(x), Y: clamp(y), W: clamp(w), H: clamp(h)}
		mode := layout.Mode(m)

		got := layout.Compute(term, mode)
		checkFrame(t, term, mode, &got)
		if again := layout.Compute(term, mode); again != got {
			t.Fatalf("Compute(%+v,%v) returned %+v then %+v", term, mode, got, again)
		}
	})
}
