// Package layout divides the terminal into panes.
//
// Geometry computed in two places drifts: a renderer that decides where a pane
// begins and a scroller that decides how many rows it holds disagree the first
// time either one changes, and the symptom is a pane that paints over its
// neighbour. Dividing the screen is this package's job alone. A caller passes a
// terminal size in and hands each pane the rectangle it gets back; a pane
// receives its rectangle and derives no boundary of its own, so a boundary
// moves in one place.
//
// [Compute] is a pure function of a terminal size and a [Mode]. It consults no
// content, holds no state, and imports nothing outside the standard library, so
// the frame is fixed before anything is rendered into it and stays fixed while
// the content beneath it changes.
package layout

// MinWidth and MinHeight are the smallest terminal that carries the layout.
// MinWidth holds the left column at its floor of minLeftWidth beside a right
// column of minRightWidth; MinHeight holds the three chrome rows above a
// content region the preview and the feed can each take a useful share of.
// Below either dimension [Compute] refuses the terminal as a whole.
const (
	MinWidth  = 60
	MinHeight = 16
)

// The chrome rows, top to bottom: the title, the agent bar, and — anchored to
// the bottom edge — the status line. Everything between them is the content
// region.
const (
	titleHeight    = 1
	agentBarHeight = 1
	statusHeight   = 1
	chromeHeight   = titleHeight + agentBarHeight + statusHeight
)

// The left column takes leftPercent of the terminal width, floored at
// minLeftWidth so a tree of nested paths stays readable on a narrow terminal
// and capped at maxLeftWidth so it does not swallow a wide one. The right
// column takes the remainder and never drops below minRightWidth, which is what
// MinWidth reserves for it.
const (
	leftPercent   = 30
	minLeftWidth  = 28
	maxLeftWidth  = 60
	minRightWidth = MinWidth - minLeftWidth
)

// The preview takes previewPercent of the content region's height and the feed
// takes the rest, so a file stays legible while the event stream keeps enough
// rows to show a burst.
const previewPercent = 60

// Frame is the resolved geometry of one terminal size. Every non-empty rect is
// absolute, so a pane renders at its own coordinates without consulting its
// neighbours. A frame that carries the layout tiles the terminal exactly: no
// cell is claimed twice and none is left unclaimed. A frame with TooSmall set
// tiles nothing, because it carries one line and leaves the rest blank.
type Frame struct {
	// Term is the terminal the frame divides, its size clamped to
	// non-negative. Every other rect lies inside it.
	Term Rect
	// Title is the top row, carrying the workspace heading.
	Title Rect
	// AgentBar is the row beneath the title, carrying per-agent status.
	AgentBar Rect
	// Left lists the workspace tree in [ModeBrowse] and an agent's runs in
	// [ModeRuns]. It is empty in an overlay mode.
	Left Rect
	// Preview holds the selected file. It is empty in an overlay mode.
	Preview Rect
	// Feed holds the event stream. It is empty in an overlay mode.
	Feed Rect
	// Status is the degradation line: what agentfs could not read and why. It
	// is the bottom row of a frame that carries the layout. When TooSmall is
	// set it moves to the first row and is the only pane in the frame, so the
	// message lands on a terminal too short to hold the layout.
	Status Rect
	// Help is the whole content region in [ModeHelp] and empty otherwise.
	Help Rect
	// Budgets is the whole content region in [ModeBudgets] and empty
	// otherwise.
	Budgets Rect
	// TooSmall reports that the terminal is narrower than [MinWidth] or
	// shorter than [MinHeight]. The frame then carries [Frame.Status] alone,
	// and the caller writes one line naming the size it needs.
	TooSmall bool
}

// Panes returns every non-empty pane in the frame, in reading order: title,
// agent bar, left, preview, feed, the overlays, status. [Frame.Term] is not
// among them; it is the space the panes divide.
func (f Frame) Panes() []Rect {
	all := [...]Rect{f.Title, f.AgentBar, f.Left, f.Preview, f.Feed, f.Help, f.Budgets, f.Status}
	out := make([]Rect, 0, len(all))
	for _, r := range all {
		if !r.Empty() {
			out = append(out, r)
		}
	}
	return out
}

// Compute derives the frame for a terminal and a mode. The same terminal and
// mode always produce an identical frame.
//
// Only term.W and term.H size the frame, and they are clamped to non-negative.
// Every non-empty pane is placed relative to term.X and term.Y, so the frame
// for an offset terminal is the frame for the origin translated by that offset;
// an empty pane has no position and stays at the zero rect. A coordinate is a
// terminal cell, and the arithmetic holds while an origin plus an extent fits
// in an int.
func Compute(term Rect, mode Mode) Frame {
	term = Rect{X: term.X, Y: term.Y, W: max(term.W, 0), H: max(term.H, 0)}
	f := Frame{Term: term, TooSmall: term.W < MinWidth || term.H < MinHeight}

	if f.TooSmall {
		// Below MinWidth by MinHeight there is no arrangement left to make:
		// panes clamped to a row or two carry nothing and read as a corrupted
		// screen rather than as a small one. The frame collapses to the
		// degradation line, which is the one pane whose job is to say why.
		f.Status = Rect{X: term.X, Y: term.Y, W: term.W, H: min(term.H, statusHeight)}
		return f
	}

	f.Title = Rect{X: term.X, Y: term.Y, W: term.W, H: titleHeight}
	f.AgentBar = Rect{X: term.X, Y: term.Y + titleHeight, W: term.W, H: agentBarHeight}
	f.Status = Rect{X: term.X, Y: term.Y + term.H - statusHeight, W: term.W, H: statusHeight}

	contentY := term.Y + titleHeight + agentBarHeight
	contentH := term.H - chromeHeight

	// An overlay mode gives the whole content region to one pane. The browse
	// panes are then empty rather than sized and hidden, so the frame carries
	// no rectangle for a pane that is not on screen.
	overlay := Rect{X: term.X, Y: contentY, W: term.W, H: contentH}
	switch mode {
	case ModeHelp:
		f.Help = overlay
		return f
	case ModeBudgets:
		f.Budgets = overlay
		return f
	case ModeBrowse, ModeRuns:
	}

	// leftPercent of the width, held between minLeftWidth and maxLeftWidth and
	// never past what the right column's floor leaves. Bounding by
	// minRightWidth here keeps the split whole for any MinWidth: the left
	// column yields rather than the right column going negative.
	leftW := term.W * leftPercent / 100
	leftW = min(max(leftW, minLeftWidth), maxLeftWidth, max(term.W-minRightWidth, 0))

	// previewPercent of the content region, each pane holding at least one row
	// while the region has rows to give.
	previewH := contentH * previewPercent / 100
	previewH = min(max(previewH, 1), max(contentH-1, 0))

	f.Left = Rect{X: term.X, Y: contentY, W: leftW, H: contentH}
	f.Preview = Rect{X: term.X + leftW, Y: contentY, W: term.W - leftW, H: previewH}
	f.Feed = Rect{X: term.X + leftW, Y: contentY + previewH, W: term.W - leftW, H: contentH - previewH}
	return f
}
