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
	"github.com/stxkxs/agentfs/internal/watch"
)

// Entry is one line of the activity feed.
type Entry struct {
	// At is when the change was observed.
	At time.Time
	// Change is what was observed.
	Change watch.Change
}

// Feed is the scrollable record of observed change.
//
// It follows the newest entry until the reader scrolls, and then holds position
// and counts what arrived, because a list that jumps under a reader trying to
// read it is unusable — and a list that silently stops updating is worse.
type Feed struct {
	scroller
	entries []Entry
	max     int
	follow  bool
	unread  int
	dropped int
}

// DefaultCapacity is the number of entries a feed holds. The ring is bounded so
// a long session costs constant memory, and what it discarded is reported
// rather than forgotten.
const DefaultCapacity = 2000

// NewFeed returns a feed holding at most capacity entries.
func NewFeed(capacity int) Feed {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return Feed{max: capacity, follow: true}
}

// Len returns the number of entries held.
func (f Feed) Len() int { return len(f.entries) }

// Selected returns the entry under the cursor. A caller acts on the change it
// names: the feed records what happened, and opening one is the application's
// business.
func (f Feed) Selected() (Entry, bool) {
	if f.cursor < 0 || f.cursor >= len(f.entries) {
		return Entry{}, false
	}
	return f.entries[f.cursor], true
}

// Following reports whether the feed is pinned to the newest entry.
func (f Feed) Following() bool { return f.follow }

// Push records a batch. Entries are held newest first.
func (f *Feed) Push(b watch.Batch) {
	if f.max == 0 {
		f.max = DefaultCapacity
	}
	at := b.At
	if at.IsZero() {
		at = time.Now()
	}

	fresh := make([]Entry, 0, len(b.Changes))
	for _, c := range b.Changes {
		when := c.At
		if when.IsZero() {
			when = at
		}
		fresh = append(fresh, Entry{At: when, Change: c})
	}
	// Newest first within the batch, matching the order of the feed itself.
	for i, j := 0, len(fresh)-1; i < j; i, j = i+1, j-1 {
		fresh[i], fresh[j] = fresh[j], fresh[i]
	}

	f.entries = append(fresh, f.entries...)
	if over := len(f.entries) - f.max; over > 0 {
		f.entries = f.entries[:f.max]
		f.dropped += over
	}

	if f.follow {
		f.cursor, f.offset = 0, 0
		return
	}
	// Holding position means the cursor must move with the rows that were
	// pushed beneath it, or the reader's place slides.
	f.cursor += len(fresh)
	f.unread += len(fresh)
	f.clamp(len(f.entries), 0)
}

// Update applies a navigation action. Scrolling away from the newest entry
// stops following; returning to it resumes, as does [keys.ActionFollow] from
// wherever the reader stopped.
func (f *Feed) Update(a keys.Action, height int) bool {
	if a == keys.ActionFollow {
		moved := !f.follow || f.cursor != 0
		f.follow = true
		f.unread = 0
		f.cursor, f.offset = 0, 0
		return moved
	}

	if !f.apply(a, len(f.entries), height) {
		return false
	}
	if f.cursor == 0 {
		f.follow, f.unread = true, 0
	} else {
		f.follow = false
	}
	return true
}

// Badge names what the reader cannot see from where they are.
func (f Feed) Badge() string {
	var parts []string
	if f.follow {
		parts = append(parts, "following")
	} else if f.unread > 0 {
		parts = append(parts, "↓ "+strconv.Itoa(f.unread)+" newer")
	}
	parts = append(parts, strconv.Itoa(len(f.entries))+" entries")
	if f.dropped > 0 {
		parts = append(parts, strconv.Itoa(f.dropped)+" discarded")
	}
	return strings.Join(parts, " · ")
}

// View renders the feed into r.
func (f Feed) View(r layout.Rect, p theme.Palette) []string {
	if len(f.entries) == 0 {
		return render.Rows([]string{textx.Fit(p.Dim().Render("  waiting for changes"), r.W)}, r)
	}

	view := f
	start, end := view.window(len(f.entries), r.H)

	out := make([]string, 0, r.H)
	for i := start; i < end; i++ {
		e := f.entries[i]
		var b strings.Builder
		b.WriteString(p.Dim().Render(e.At.Format("15:04:05")))
		b.WriteByte(' ')
		b.WriteString(changeMarker(e.Change.Op, p))
		b.WriteByte(' ')
		b.WriteString(p.Body().Render(textx.Elide(textx.Sanitize(e.Change.Path), max(r.W-16, 8))))
		out = append(out, render.Row(p, i == f.cursor, b.String(), r.W))
	}
	return render.Rows(out, r)
}

// changeMarker renders an operation as a glyph and a colour, so the kind of
// change survives a monochrome terminal.
func changeMarker(op watch.Op, p theme.Palette) string {
	g := p.Glyphs()
	switch op {
	case watch.OpCreate:
		return p.Status(theme.RoleRunning).Render(g.Created)
	case watch.OpModify:
		return p.Status(theme.RoleIdle).Render(g.Modified)
	case watch.OpRemove:
		return p.Status(theme.RoleError).Render(g.Removed)
	case watch.OpRename:
		return p.Accent().Render(g.Renamed)
	default:
		return p.Dim().Render(g.Unknown)
	}
}
