// Package pane holds the components the frame is assembled from.
//
// A pane is a plain value with a cursor and a viewport. It is not a Bubble Tea
// model: only the application at the root of the program is, which is what
// keeps a pane exercisable by calling its methods rather than by driving a
// terminal.
//
// A pane sanitizes workspace bytes with [textx.Sanitize] before it styles them,
// because [render] cannot: by the time a line reaches the render boundary it
// carries the palette's own escape sequences, and stripping those would remove
// the styling along with the threat.
package pane

import (
	"github.com/stxkxs/agentfs/internal/ui/keys"
)

// ID identifies a pane for focus and for key scoping.
type ID int

// The panes, in focus-ring order.
const (
	// IDTree is the workspace tree.
	IDTree ID = iota
	// IDPreview is the file preview.
	IDPreview
	// IDFeed is the activity feed.
	IDFeed
	// IDRuns is the run history, which replaces the tree in runs mode.
	IDRuns
)

// String returns the lowercase name of the pane.
func (id ID) String() string {
	switch id {
	case IDTree:
		return "tree"
	case IDPreview:
		return "preview"
	case IDFeed:
		return "feed"
	case IDRuns:
		return "runs"
	default:
		return "unknown"
	}
}

// Scope returns the key scope a pane resolves presses in, so a pane cannot be
// focusable without its keys being declared.
func (id ID) Scope() keys.Scope {
	switch id {
	case IDTree:
		return keys.ScopeTree
	case IDPreview:
		return keys.ScopePreview
	case IDFeed:
		return keys.ScopeFeed
	case IDRuns:
		return keys.ScopeRuns
	default:
		return keys.ScopeGlobal
	}
}

// FocusRing is the order Tab moves focus through in browse mode.
func FocusRing() []ID { return []ID{IDTree, IDPreview, IDFeed} }

// RunsFocusRing is the order Tab moves focus through in runs mode.
func RunsFocusRing() []ID { return []ID{IDRuns, IDPreview, IDFeed} }

// scroller is the cursor and viewport arithmetic every list pane shares.
//
// One implementation means a list cannot scroll differently from its
// neighbours, and the off-by-one that lets a cursor sit one row outside its
// viewport is fixed once rather than per pane.
type scroller struct {
	cursor int
	offset int
}

// clamp keeps the cursor inside the list and the viewport around the cursor.
func (s *scroller) clamp(count, height int) {
	if count <= 0 {
		s.cursor, s.offset = 0, 0
		return
	}
	s.cursor = min(max(s.cursor, 0), count-1)
	if height <= 0 {
		s.offset = 0
		return
	}
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+height {
		s.offset = s.cursor - height + 1
	}
	s.offset = min(max(s.offset, 0), max(count-height, 0))
}

// apply moves the cursor for a navigation action, reporting whether it moved.
func (s *scroller) apply(a keys.Action, count, height int) bool {
	before := s.cursor
	switch a {
	case keys.ActionUp:
		s.cursor--
	case keys.ActionDown:
		s.cursor++
	case keys.ActionPageUp:
		s.cursor -= max(height/2, 1)
	case keys.ActionPageDown:
		s.cursor += max(height/2, 1)
	case keys.ActionTop:
		s.cursor = 0
	case keys.ActionBottom:
		s.cursor = count - 1
	default:
		return false
	}
	s.clamp(count, height)
	return s.cursor != before
}

// window returns the half-open range of rows the viewport shows.
func (s *scroller) window(count, height int) (start, end int) {
	s.clamp(count, height)
	if height <= 0 {
		return 0, 0
	}
	return s.offset, min(s.offset+height, count)
}

// Cursor returns the selected row.
func (s scroller) Cursor() int { return s.cursor }
