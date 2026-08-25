package pane_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// cursorGlyph is the mark every pane draws on its selected row.
var cursorGlyph = theme.Plain().Glyphs().Cursor

// cursorRow returns the rendered row carrying the cursor.
func cursorRow(t *testing.T, lines []string) string {
	t.Helper()
	for _, line := range lines {
		if strings.HasPrefix(line, cursorGlyph) {
			return line
		}
	}
	t.Fatalf("no row is marked as selected:\n%s", strings.Join(lines, "\n"))
	return ""
}

func expanded(t *testing.T, fsys fsx.FS, limits index.Limits, dirs ...string) *index.Index {
	t.Helper()
	ix := index.New(fsx.New("ws", fsys), limits)
	for _, d := range dirs {
		ix.Expand(d)
	}
	ix.Drain()
	return ix
}

// A pane with no rows has nothing under its cursor, so a workspace that is
// empty or unreadable does not hand the application a node that is not there.
func TestTreeSelectsNothingWhenItHasNoRows(t *testing.T) {
	t.Parallel()
	ix := index.New(fsx.New("ws", fstest.MapFS{}), index.Limits{})

	var tree pane.Tree
	if node, ok := tree.Selected(ix); ok {
		t.Fatalf("Selected() = %v, true on an empty index, want no selection", node)
	}
	for _, a := range []keys.Action{keys.ActionDown, keys.ActionExpand, keys.ActionCollapse} {
		if tree.Update(a, ix, 10) {
			t.Errorf("%v reported a change on an empty index", a)
		}
	}
}

// Collapsing a file closes the directory holding it and moves there, so one
// press walks back up rather than making the reader find the parent row first.
func TestTreeCollapsingALeafClosesAndSelectsItsParent(t *testing.T) {
	t.Parallel()
	// Rows below the parent keep the cursor from landing on it by clamping
	// alone, so the assertion is about the move rather than about the ceiling.
	ix := expanded(t, fstest.MapFS{
		"a/x.json":   {Data: []byte(`{}`)},
		"b/one.json": {Data: []byte(`{}`)}, "b/two.json": {Data: []byte(`{}`)},
		"c/z.json": {Data: []byte(`{}`)},
	}, index.Limits{}, "b")

	var tree pane.Tree
	for range 2 {
		tree.Update(keys.ActionDown, ix, 10)
	}
	if node, _ := tree.Selected(ix); node.Path != "b/one.json" {
		t.Fatalf("the cursor is on %q, want it on the first member of b", node.Path)
	}

	if !tree.Update(keys.ActionCollapse, ix, 10) {
		t.Fatal("collapsing a leaf reported no change")
	}
	node, ok := tree.Selected(ix)
	if !ok || node.Path != "b" {
		t.Fatalf("the cursor is on %v, want it on the parent directory", node)
	}
	if node.Expanded() {
		t.Error("the parent directory was selected but left open")
	}
}

// A file at the workspace root has no directory to walk up to, so collapsing it
// leaves the reader where they are rather than moving to the root row.
func TestTreeCollapsingATopLevelLeafHoldsTheSelection(t *testing.T) {
	t.Parallel()
	ix := index.New(fsx.New("ws", fstest.MapFS{"x.json": {Data: []byte(`{}`)}}), index.Limits{})

	var tree pane.Tree
	tree.Update(keys.ActionCollapse, ix, 10)
	node, ok := tree.Selected(ix)
	if !ok || node.Path != "x.json" {
		t.Fatalf("Selected() = %v, %t, want the file still selected", node, ok)
	}
}

// Opening a file is the application's business: the tree reports the selection
// and leaves loading it to the pane that renders files.
func TestTreeLeavesOpeningAFileToTheApplication(t *testing.T) {
	t.Parallel()
	ix := index.New(fsx.New("ws", fstest.MapFS{"x.json": {Data: []byte(`{}`)}}), index.Limits{})

	var tree pane.Tree
	if tree.Update(keys.ActionOpen, ix, 10) {
		t.Error("the tree claimed to have handled opening a file")
	}
}

// The badge names every ceiling the index reached, so a reader can tell whether
// the tree is showing everything before deciding to trust it.
func TestTreeBadgeNamesEveryCeilingItReached(t *testing.T) {
	t.Parallel()
	twoFiles := fstest.MapFS{"a/one.json": {Data: []byte(`{}`)}, "a/two.json": {Data: []byte(`{}`)}}

	t.Run("complete", func(t *testing.T) {
		t.Parallel()
		ix := expanded(t, twoFiles, index.Limits{}, "a")
		if got := (pane.Tree{}).Badge(ix); got != "3 rows" {
			t.Errorf("Badge() = %q, want %q", got, "3 rows")
		}
	})

	t.Run("directory capped", func(t *testing.T) {
		t.Parallel()
		ix := expanded(t, twoFiles, index.Limits{MaxEntriesPerDir: 1}, "a")
		if got := (pane.Tree{}).Badge(ix); !strings.Contains(got, "1 capped") {
			t.Errorf("Badge() = %q, want it to name the capped directory", got)
		}
	})

	t.Run("directory unreadable", func(t *testing.T) {
		t.Parallel()
		faulty := fsx.NewFaulty(twoFiles, fsx.Fault{
			Path: "a", Op: fsx.OpReadDir, Err: errors.New("permission denied"),
		})
		ix := expanded(t, faulty, index.Limits{}, "a")
		if got := (pane.Tree{}).Badge(ix); !strings.Contains(got, "1 unreadable") {
			t.Errorf("Badge() = %q, want it to name the unreadable directory", got)
		}
	})

	t.Run("node ceiling", func(t *testing.T) {
		t.Parallel()
		ix := index.New(fsx.New("ws", fstest.MapFS{
			"a/one.json": {Data: []byte(`{}`)}, "b/two.json": {Data: []byte(`{}`)},
		}), index.Limits{MaxNodes: 1})
		if got := (pane.Tree{}).Badge(ix); !strings.Contains(got, "at node ceiling") {
			t.Errorf("Badge() = %q, want it to name the node ceiling", got)
		}
	})
}

// A row a reader cannot open carries the reason on the row itself, because the
// alternative is a directory that looks empty when it is only unreadable.
func TestTreeMarksLinksAndUnreadableRows(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/one.json": {Data: []byte(`{}`)},
		"link":       {Mode: fs.ModeSymlink, Data: []byte("a/one.json")},
	}
	faulty := fsx.NewFaulty(fsys, fsx.Fault{Path: "a", Op: fsx.OpReadDir, Err: errors.New("permission denied")})
	ix := expanded(t, faulty, index.Limits{}, "a")

	out := strings.Join((pane.Tree{}).View(ix, layout.Rect{W: 60, H: 6}, theme.Plain(), noRecent), "\n")
	if !strings.Contains(out, "link@") {
		t.Errorf("a symbolic link is not marked as one:\n%s", out)
	}
	if !strings.Contains(out, "unreadable") {
		t.Errorf("a directory that could not be read is not marked as one:\n%s", out)
	}
}

// The feed reads newest first, and a batch that arrives after another sits
// above it, so the order on screen is the order things happened.
func TestFeedHoldsEveryBatchNewestFirst(t *testing.T) {
	t.Parallel()
	f := pane.NewFeed(0)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "first", Op: watch.OpCreate}, {Path: "second", Op: watch.OpCreate},
	}})
	f.Push(watch.Batch{Changes: []watch.Change{
		{Path: "third", Op: watch.OpCreate}, {Path: "fourth", Op: watch.OpCreate},
	}})

	if f.Len() != 4 {
		t.Fatalf("a feed given no capacity holds %d of the 4 entries pushed", f.Len())
	}
	out := f.View(layout.Rect{W: 60, H: 4}, theme.Plain())
	for i, want := range []string{"fourth", "third", "second", "first"} {
		if !strings.Contains(out[i], want) {
			t.Errorf("row %d is %q, want it to hold %q", i, out[i], want)
		}
	}
}

// A reader who scrolled away keeps the entry they were reading, because a list
// that slides under them as entries arrive cannot be read at all.
func TestFeedHoldsTheReadersPlaceWhenEntriesArriveBeneathIt(t *testing.T) {
	t.Parallel()
	r := layout.Rect{W: 60, H: 8}
	f := pane.NewFeed(100)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "oldest", Op: watch.OpCreate},
		{Path: "middle", Op: watch.OpCreate},
		{Path: "newest", Op: watch.OpCreate},
	}})

	f.Update(keys.ActionDown, r.H)
	f.Update(keys.ActionDown, r.H)
	if got := cursorRow(t, f.View(r, theme.Plain())); !strings.Contains(got, "oldest") {
		t.Fatalf("the cursor is on %q, want it on the oldest entry", got)
	}

	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "later-a", Op: watch.OpModify}, {Path: "later-b", Op: watch.OpModify},
	}})
	if got := cursorRow(t, f.View(r, theme.Plain())); !strings.Contains(got, "oldest") {
		t.Errorf("the cursor moved to %q when entries arrived above it", got)
	}
	if f.Cursor() != 4 {
		t.Errorf("Cursor() = %d, want row 4 after two entries were pushed above it", f.Cursor())
	}
}

// Following again is how a reader who scrolled away gets back to the newest
// entry in one press, from wherever in the list they stopped.
func TestFeedFollowKeyReturnsToTheNewestEntry(t *testing.T) {
	t.Parallel()
	f := pane.NewFeed(100)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "a", Op: watch.OpCreate}, {Path: "b", Op: watch.OpCreate},
	}})

	if f.Update(keys.ActionFollow, 5) {
		t.Error("following reported a change on a feed that was already following")
	}
	f.Update(keys.ActionBottom, 5)
	if f.Following() {
		t.Fatal("scrolling to the end left the feed following")
	}
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "c", Op: watch.OpCreate}, {Path: "d", Op: watch.OpCreate},
	}})
	if got := f.Badge(); !strings.Contains(got, "2 newer") {
		t.Fatalf("Badge() = %q, want it to count the two entries that arrived", got)
	}

	if !f.Update(keys.ActionFollow, 5) {
		t.Fatal("following an unfollowed feed reported no change")
	}
	if !f.Following() {
		t.Error("the follow key did not resume following")
	}
	if f.Cursor() != 0 {
		t.Errorf("Cursor() = %d after following, want the newest entry", f.Cursor())
	}

	// The count is of entries the reader has not seen, so resuming has to drop
	// what they caught up on rather than carrying it into the next scroll away.
	f.Update(keys.ActionBottom, 5)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: "e", Op: watch.OpCreate}}})
	if got := f.Badge(); !strings.Contains(got, "1 newer") {
		t.Errorf("Badge() = %q, want it to count only the entry that arrived since", got)
	}
}

// The kind of change survives a monochrome terminal, so an operator reading the
// feed over a plain connection can still tell a removal from a creation.
func TestFeedDrawsEachKindOfChangeDistinctly(t *testing.T) {
	t.Parallel()
	ops := []watch.Op{watch.OpCreate, watch.OpModify, watch.OpRemove, watch.OpRename, watch.Op(99)}

	seen := map[string]watch.Op{}
	for _, op := range ops {
		f := pane.NewFeed(10)
		f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: "p", Op: op}}})
		row := f.View(layout.Rect{W: 40, H: 1}, theme.Plain())[0]
		if prev, dup := seen[row]; dup {
			t.Errorf("%v and %v render identically: %q", prev, op, row)
		}
		seen[row] = op
	}
}

func TestRunsSelectionFollowsTheCursor(t *testing.T) {
	t.Parallel()
	runs := []workspace.Run{
		{ID: "first", Agent: "a", Dir: "a/runs/first", Declared: true, StartedAt: clock},
		{ID: "second", Agent: "a", Dir: "a/runs/second", StartedAt: clock},
	}

	var rn pane.Runs
	if _, ok := rn.Selected(nil); ok {
		t.Error("Selected() reported a run on an empty list")
	}
	got, ok := rn.Selected(runs)
	if !ok || got.ID != "first" {
		t.Fatalf("Selected() = %v, %t, want the first run", got, ok)
	}

	if !rn.Update(keys.ActionDown, runs, 10) {
		t.Fatal("moving down reported no change")
	}
	if got, _ = rn.Selected(runs); got.ID != "second" {
		t.Errorf("Selected() = %q, want the second run", got.ID)
	}
	if rn.Update(keys.ActionDown, runs, 10) {
		t.Error("moving down past the last run reported a change")
	}
}

// The badge separates the runs whose identity was declared from those read off
// a directory name, because an operator acting on a run needs to know which.
func TestRunsBadgeCountsDeclaredIdentities(t *testing.T) {
	t.Parallel()
	if got := (pane.Runs{}).Badge(nil); got != "none" {
		t.Errorf("Badge() = %q for no runs, want %q", got, "none")
	}
	runs := []workspace.Run{
		{ID: "a", Declared: true, StartedAt: clock}, {ID: "b", StartedAt: clock}, {ID: "c", Declared: true},
	}
	if got := (pane.Runs{}).Badge(runs); got != "3 runs · 2 declared" {
		t.Errorf("Badge() = %q, want %q", got, "3 runs · 2 declared")
	}
}

// A run inside the day reads as a clock time and one beyond it carries its
// date, so two runs a week apart are never mistaken for the same afternoon.
func TestRunStampIsAClockTimeInsideADayAndADateBeyondIt(t *testing.T) {
	t.Parallel()
	runs := []workspace.Run{
		{ID: "today", StartedAt: clock.Add(-2 * time.Hour)},
		{ID: "last-week", StartedAt: clock.AddDate(0, 0, -7)},
		{ID: "undated"},
	}
	out := (pane.Runs{}).View(runs, layout.Rect{W: 60, H: 3}, theme.Plain(), clock)

	for i, want := range []string{"11:00:00", "04-01 13:00", "unknown"} {
		if !strings.Contains(out[i], want) {
			t.Errorf("row %d is %q, want it to hold %q", i, out[i], want)
		}
	}
}

// Every status the contract defines is drawn with its own mark, because the
// colour that distinguishes them is absent on a monochrome terminal.
func TestRunsDrawEveryStatusDistinctly(t *testing.T) {
	t.Parallel()
	statuses := []agentstate.Status{
		agentstate.StatusUnknown, agentstate.StatusRunning, agentstate.StatusIdle,
		agentstate.StatusBlocked, agentstate.StatusError, agentstate.StatusDone,
		agentstate.Status(99),
	}

	seen := map[string][]agentstate.Status{}
	for _, s := range statuses {
		runs := []workspace.Run{{ID: "r", Status: s, StartedAt: clock}}
		row := (pane.Runs{}).View(runs, layout.Rect{W: 40, H: 1}, theme.Plain(), clock)[0]
		seen[row] = append(seen[row], s)
	}
	// A status outside the vocabulary renders as the unknown one, which is the
	// only pair allowed to share a mark.
	if len(seen) != len(statuses)-1 {
		t.Errorf("%d statuses render as %d distinct rows, want each defined status to have its own: %v",
			len(statuses), len(seen), seen)
	}
}

// The overlay is drawn from the registry that resolves a press, so a binding
// cannot be live without appearing here.
func TestHelpNamesEveryScopeThatBindsSomething(t *testing.T) {
	t.Parallel()
	reg := keys.Default()
	out := strings.Join((pane.Help{}).View(reg, layout.Rect{W: 80, H: 200}, theme.Plain()), "\n")

	for _, section := range reg.Help() {
		if len(section.Bindings) == 0 {
			continue
		}
		for _, b := range section.Bindings {
			if !strings.Contains(out, b.Help) {
				t.Errorf("scope %v binds %q and the overlay does not show it", section.Scope, b.Help)
			}
		}
	}
}

// The overlay is longer than any terminal it is drawn into, so a reader reaches
// the last section by scrolling rather than by resizing.
func TestHelpScrollsToTheSectionsBelowTheFold(t *testing.T) {
	t.Parallel()
	reg := keys.Default()
	r := layout.Rect{W: 80, H: 6}

	var help pane.Help
	first := strings.Join(help.View(reg, r, theme.Plain()), "\n")
	if !help.Update(keys.ActionBottom, reg, r.H) {
		t.Fatal("scrolling to the end of the overlay reported no change")
	}
	last := strings.Join(help.View(reg, r, theme.Plain()), "\n")
	if first == last {
		t.Fatalf("the overlay shows the same rows at both ends:\n%s", last)
	}

	if !help.Update(keys.ActionTop, reg, r.H) {
		t.Fatal("scrolling back to the start reported no change")
	}
	if got := strings.Join(help.View(reg, r, theme.Plain()), "\n"); got != first {
		t.Errorf("returning to the top did not restore the first rows:\n%s", got)
	}
}

// A scope the default table does not define still earns a heading and a row, so
// a table assembled at run time cannot hide a binding from the reader.
func TestHelpNamesAScopeOutsideTheDefaultVocabulary(t *testing.T) {
	t.Parallel()
	reg := keys.New([]keys.Binding{
		{Keys: []string{"ctrl+alt+shift+f", "ctrl+alt+shift+g"}, Action: keys.ActionOpen,
			Scope: keys.Scope(42), Help: "do the thing"},
	})
	out := strings.Join((pane.Help{}).View(reg, layout.Rect{W: 80, H: 8}, theme.Plain()), "\n")

	if !strings.Contains(out, keys.Scope(42).String()) {
		t.Errorf("a scope outside the vocabulary is not named:\n%s", out)
	}
	if !strings.Contains(out, "do the thing") {
		t.Errorf("a binding in an undeclared scope is not shown:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+alt+shift+f / ctrl+alt+shift+g do the thing") {
		t.Errorf("a key spelling wider than the column runs into its help text:\n%s", out)
	}
}

// The pane lists what the registry holds and nothing else, so a budget reaches
// an operator by being registered rather than by being named here.
func TestBudgetsListEveryBudgetTheRegistryHolds(t *testing.T) {
	t.Parallel()
	registry := metrics.NewRegistry()
	metrics.DefaultBudgets(registry)
	held := registry.Budgets()
	if len(held) == 0 {
		t.Fatal("DefaultBudgets registered nothing, so the check covers nothing")
	}

	out := strings.Join((pane.Budgets{}).View(held, layout.Rect{W: 100, H: 40}, theme.Plain()), "\n")
	for _, b := range held {
		if !strings.Contains(out, b.Name) {
			t.Errorf("the registry holds %s and the pane does not list it:\n%s", b.Name, out)
		}
	}
}

// A deadline nothing exercised and a deadline nothing breached are different
// answers, so the pane marks them apart and withholds percentiles it has not
// measured.
func TestBudgetsSeparateAnUnexercisedBudgetFromABreachedOne(t *testing.T) {
	t.Parallel()
	p := theme.Plain()
	stats := []metrics.BudgetStats{
		{Name: "untouched", Deadline: 50 * time.Millisecond},
		{Name: "inside", Deadline: 50 * time.Millisecond, Count: 10,
			P50: time.Millisecond, P90: 2 * time.Millisecond, P99: 3 * time.Millisecond, Max: 4 * time.Millisecond},
		{Name: "past", Deadline: 50 * time.Millisecond, Count: 10, Breached: 3,
			P50: 20 * time.Millisecond, P90: 60 * time.Millisecond, P99: 80 * time.Millisecond, Max: 90 * time.Millisecond},
	}
	lines := (pane.Budgets{}).View(stats, layout.Rect{W: 100, H: 10}, p)

	rows := map[string]string{}
	for _, want := range []string{"untouched", "inside", "past"} {
		for _, line := range lines {
			if strings.Contains(line, want) {
				rows[want] = line
			}
		}
		if rows[want] == "" {
			t.Fatalf("the pane does not list %s:\n%s", want, strings.Join(lines, "\n"))
		}
	}

	if !strings.Contains(rows["untouched"], p.Glyphs().Unknown) {
		t.Errorf("a budget with no observation is not marked unexercised: %q", rows["untouched"])
	}
	if !strings.Contains(rows["inside"], p.Glyphs().Done) {
		t.Errorf("a budget nothing breached is not marked met: %q", rows["inside"])
	}
	if !strings.Contains(rows["past"], p.Glyphs().Warning) {
		t.Errorf("a breached budget is not marked: %q", rows["past"])
	}
	if !strings.Contains(rows["untouched"], "-") {
		t.Errorf("a budget with no observation reports percentiles it never measured: %q", rows["untouched"])
	}
	if strings.Contains(rows["inside"], "-") {
		t.Errorf("a budget with observations withholds a percentile it measured: %q", rows["inside"])
	}
	if !strings.Contains(rows["past"], "3") {
		t.Errorf("a breached budget does not report how many observations missed: %q", rows["past"])
	}
}

// The table is longer than a short terminal holds, so a reader reaches the last
// budget by scrolling rather than by resizing.
func TestBudgetsScrollToTheRowsBelowTheFold(t *testing.T) {
	t.Parallel()
	stats := budgetFixture()
	r := layout.Rect{W: 100, H: 3}

	var budgets pane.Budgets
	first := strings.Join(budgets.View(stats, r, theme.Plain()), "\n")
	if !budgets.Update(keys.ActionBottom, stats, r.H) {
		t.Fatal("scrolling to the end of the table reported no change")
	}
	last := strings.Join(budgets.View(stats, r, theme.Plain()), "\n")
	if first == last {
		t.Fatalf("the table shows the same rows at both ends:\n%s", last)
	}
	if !budgets.Update(keys.ActionTop, stats, r.H) {
		t.Fatal("scrolling back to the start reported no change")
	}
	if got := strings.Join(budgets.View(stats, r, theme.Plain()), "\n"); got != first {
		t.Errorf("returning to the top did not restore the first rows:\n%s", got)
	}
}

// The badge carries the scent a reader decides on before opening the pane: how
// many budgets are held, and whether any of them was missed.
func TestBudgetsBadgeCountsWhatIsHeldAndWhatWasMissed(t *testing.T) {
	t.Parallel()
	met := []metrics.BudgetStats{{Name: "a", Count: 1}, {Name: "b", Count: 1}}
	if got := (pane.Budgets{}).Badge(met); !strings.Contains(got, "2") || strings.Contains(got, "breached") {
		t.Errorf("Badge() = %q for two budgets nothing breached", got)
	}
	missed := []metrics.BudgetStats{
		{Name: "a", Count: 1}, {Name: "b", Count: 1}, {Name: "c", Count: 1, Breached: 1},
	}
	if got := (pane.Budgets{}).Badge(missed); !strings.Contains(got, "breached") {
		t.Errorf("Badge() = %q with a breached budget among them", got)
	}
}

// budgetFixture is a record wide enough to overrun every column: a name longer
// than the column holding it, counts in the billions, and spans on both sides
// of a second.
func budgetFixture() []metrics.BudgetStats {
	return []metrics.BudgetStats{
		{Name: "a" + strings.Repeat("長", 40), Deadline: time.Millisecond,
			Count: 9_999_999_999, Breached: 9_999_999_998,
			P50: time.Hour, P90: time.Hour, P99: time.Hour, Max: 400 * time.Hour},
		{Name: "sub-millisecond", Deadline: time.Second, Count: 3,
			P50: 100 * time.Nanosecond, P90: time.Microsecond, P99: 900 * time.Microsecond, Max: time.Millisecond},
		{Name: "unexercised", Deadline: 250 * time.Millisecond},
		{Name: "seconds", Deadline: 90 * time.Second, Count: 2,
			P50: 61 * time.Second, P90: 62 * time.Second, P99: 63 * time.Second, Max: 64 * time.Second},
	}
}

// A healthy observer says the view is complete rather than saying nothing,
// because silence is what a broken status line also produces.
func TestStatusCollectsNoConditionsForAHealthyObserver(t *testing.T) {
	t.Parallel()
	if got := pane.Conditions(watch.Stats{}, index.Stats{}, 0); len(got) != 0 {
		t.Errorf("Conditions() = %v, want none", got)
	}
}

// Conditions are ranked so that the ones a narrow terminal drops are the ones
// that matter least.
func TestStatusRanksTheMostSeriousConditionFirst(t *testing.T) {
	t.Parallel()
	got := pane.Conditions(
		watch.Stats{RootLost: true, Dropped: 5, WatchesRefused: 9, Errors: 2},
		index.Stats{NodeCeilingHit: true, TruncatedDirs: 3, DepthTruncated: 2, UnreadableDirs: 1},
		4,
	)

	want := []string{
		"workspace root unreadable — retrying",
		"5 changes dropped, resynchronized",
		"9 directories swept, not watched",
		"tree at its node ceiling",
		"3 directories capped",
		"2 subtrees below the depth ceiling",
		"1 directories unreadable",
		"4 state documents invalid",
		"2 source errors",
	}
	if len(got) != len(want) {
		t.Fatalf("Conditions() returned %d conditions, want %d: %v", len(got), len(want), got)
	}
	for i, c := range got {
		if c.Text != want[i] {
			t.Errorf("condition %d is %q, want %q", i, c.Text, want[i])
		}
		if i > 0 && got[i-1].Rank >= c.Rank {
			t.Errorf("condition %d ranks %d, not below the %d before it", i, c.Rank, got[i-1].Rank)
		}
	}
	for _, severe := range got[:2] {
		if !severe.Severe {
			t.Errorf("%q is not marked severe", severe.Text)
		}
	}
}

// The line states how the workspace is being observed, so an operator reads the
// mechanism rather than assuming kernel events are arriving.
func TestStatusNamesHowTheWorkspaceIsObserved(t *testing.T) {
	t.Parallel()
	stats := watch.Stats{
		Mode:       config.ModeHybrid,
		Filesystem: fsx.Filesystem{Type: "nfs"},
		Watches:    12,
		Tracked:    340,
		SweepCycle: 1500 * time.Millisecond,
	}
	line := (pane.Status{}).View(stats, nil, layout.Rect{W: 120, H: 1}, theme.Plain())[0]

	for _, want := range []string{"nfs", "hybrid", "12 watches", "sweep 340", "1.5s cycle"} {
		if !strings.Contains(line, want) {
			t.Errorf("the status line does not name %q: %q", want, line)
		}
	}
}

// A pane's name is what a diagnostic and a key scope are reported against, so
// every focusable pane has one and a value from nowhere is visible as such.
func TestPaneIDNamesItself(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id   pane.ID
		want string
	}{
		{pane.IDTree, "tree"},
		{pane.IDPreview, "preview"},
		{pane.IDFeed, "feed"},
		{pane.IDRuns, "runs"},
		{pane.ID(99), "unknown"},
	} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("ID(%d).String() = %q, want %q", tc.id, got, tc.want)
		}
	}
	if got := pane.ID(99).Scope(); got != keys.ScopeGlobal {
		t.Errorf("ID(99).Scope() = %v, want the global scope", got)
	}
}

// An empty pane says what it is waiting for, because a blank rectangle reads
// as a pane that failed rather than one with nothing to report.
func TestEmptyPanesSayWhatTheyAreWaitingFor(t *testing.T) {
	t.Parallel()
	r := layout.Rect{W: 40, H: 4}
	p := theme.Plain()
	ix := index.New(fsx.New("ws", fstest.MapFS{}), index.Limits{})

	for _, tc := range []struct {
		name string
		out  []string
		want string
	}{
		{name: "tree", out: (pane.Tree{}).View(ix, r, p, noRecent), want: "empty workspace"},
		{name: "feed", out: pane.NewFeed(10).View(r, p), want: "waiting for changes"},
		{name: "runs", out: (pane.Runs{}).View(nil, r, p, clock), want: "no runs recorded"},
		{name: "agent bar", out: (pane.AgentBar{}).View(nil, r, p), want: "no agents detected"},
		{name: "budgets", out: (pane.Budgets{}).View(nil, r, p), want: "no budgets registered"},
	} {
		assertFits(t, tc.name, tc.out, r)
		if !strings.Contains(strings.Join(tc.out, "\n"), tc.want) {
			t.Errorf("the empty %s does not say so:\n%s", tc.name, strings.Join(tc.out, "\n"))
		}
	}
}

// A half screen is one press, so a reader crosses a long list without holding a
// key down.
func TestPagingMovesHalfTheVisibleRows(t *testing.T) {
	t.Parallel()
	const height = 10
	f := pane.NewFeed(100)
	changes := make([]watch.Change, 40)
	for i := range changes {
		changes[i] = watch.Change{Path: "entry-" + string(rune('a'+i%26)), Op: watch.OpCreate}
	}
	f.Push(watch.Batch{At: clock, Changes: changes})

	if !f.Update(keys.ActionPageDown, height) {
		t.Fatal("paging down reported no change")
	}
	if f.Cursor() != height/2 {
		t.Errorf("Cursor() = %d after paging down, want %d", f.Cursor(), height/2)
	}
	if !f.Update(keys.ActionPageUp, height) {
		t.Fatal("paging up reported no change")
	}
	if f.Cursor() != 0 {
		t.Errorf("Cursor() = %d after paging back, want 0", f.Cursor())
	}
	if !f.Following() {
		t.Error("returning to the newest entry did not resume following")
	}
}

// A pane ignores an action it has no answer for, so a key bound elsewhere does
// not move a cursor as a side effect.
func TestPanesIgnoreAnActionTheyDoNotBind(t *testing.T) {
	t.Parallel()
	ix := expanded(t, fstest.MapFS{"a/one.json": {Data: []byte(`{}`)}}, index.Limits{}, "a")
	f := pane.NewFeed(10)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: "a", Op: watch.OpCreate}}})

	var tree pane.Tree
	if tree.Update(keys.ActionToggleRuns, ix, 10) {
		t.Error("the tree claimed an action it does not bind")
	}
	if f.Update(keys.ActionToggleRuns, 10) {
		t.Error("the feed claimed an action it does not bind")
	}
}

// A zero-value feed still holds a bounded ring, so a caller that declared one
// rather than constructing it does not grow without limit.
func TestZeroValueFeedHoldsABoundedRing(t *testing.T) {
	t.Parallel()
	var f pane.Feed
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: "a", Op: watch.OpCreate}}})

	if f.Len() != 1 {
		t.Fatalf("Len() = %d, want the pushed entry held", f.Len())
	}
	if !strings.Contains(f.Badge(), "1 entries") {
		t.Errorf("Badge() = %q, want it to count the entry", f.Badge())
	}
}

// Collapsing an open directory closes it in place, which is what distinguishes
// it from collapsing a file inside it.
func TestTreeCollapsingAnOpenDirectoryClosesItInPlace(t *testing.T) {
	t.Parallel()
	ix := expanded(t, fstest.MapFS{
		"a/one.json": {Data: []byte(`{}`)}, "a/two.json": {Data: []byte(`{}`)},
	}, index.Limits{}, "a")

	var tree pane.Tree
	if !tree.Update(keys.ActionCollapse, ix, 10) {
		t.Fatal("collapsing an open directory reported no change")
	}
	node, ok := tree.Selected(ix)
	if !ok || node.Path != "a" {
		t.Fatalf("Selected() = %v, %t, want the directory still selected", node, ok)
	}
	if node.Expanded() {
		t.Error("the directory was left open")
	}
	if len(ix.Rows()) != 1 {
		t.Errorf("the tree shows %d rows, want the closed directory alone", len(ix.Rows()))
	}
}

// A directory held to the per-directory cap says so on its own row, so a reader
// scanning the tree does not read a prefix as the whole of it.
func TestTreeMarksACappedDirectoryOnItsRow(t *testing.T) {
	t.Parallel()
	ix := expanded(t, fstest.MapFS{
		"a/one.json": {Data: []byte(`{}`)}, "a/two.json": {Data: []byte(`{}`)},
	}, index.Limits{MaxEntriesPerDir: 1}, "a")

	out := strings.Join((pane.Tree{}).View(ix, layout.Rect{W: 60, H: 4}, theme.Plain(), noRecent), "\n")
	if !strings.Contains(out, "capped") {
		t.Errorf("a capped directory is not marked as one:\n%s", out)
	}
}

// The bar carries the task, the position within it and the problem an agent
// declared, because those are what an operator reads before intervening.
func TestAgentBarCarriesTheDetailAnAgentDeclared(t *testing.T) {
	t.Parallel()
	agent := workspace.Agent{
		Name: "researcher", Presence: workspace.PresenceDeclared,
		State: agentstate.State{
			Status: agentstate.StatusBlocked, Task: "collate the findings",
			Step: agentstate.OrdinalStep(3), StepsTotal: 7, Problem: "awaiting approval",
		},
	}
	out := strings.Join((pane.AgentBar{}).View([]workspace.Agent{agent},
		layout.Rect{W: 160, H: 1}, theme.Plain()), "")

	for _, want := range []string{"collate the findings", "step 3/7", "awaiting approval"} {
		if !strings.Contains(out, want) {
			t.Errorf("the bar does not carry %q: %q", want, strings.TrimSpace(out))
		}
	}
}

// A presence outside the vocabulary reads as unknown rather than as an agent
// with nothing to report.
func TestAgentBarNamesAPresenceOutsideTheVocabularyAsUnknown(t *testing.T) {
	t.Parallel()
	agent := workspace.Agent{Name: "a", Presence: workspace.Presence(99)}
	out := strings.Join((pane.AgentBar{}).View([]workspace.Agent{agent},
		layout.Rect{W: 60, H: 1}, theme.Plain()), "")

	if !strings.Contains(out, "unknown") {
		t.Errorf("a presence from nowhere is not named as unknown: %q", strings.TrimSpace(out))
	}
}
