package pane_test

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

var clock = time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)

// rects covers the shapes a pane is asked to fill, from generous to absurd.
var rects = []layout.Rect{
	{W: 40, H: 10}, {W: 28, H: 6}, {W: 200, H: 40}, {W: 8, H: 3}, {W: 1, H: 1}, {W: 3, H: 1},
	{W: 40, H: 0},
}

// hostile is content a workspace agentfs does not control can contain.
var hostile = []string{
	strings.Repeat("x", 4000),
	"日本語" + strings.Repeat("あ", 400),
	"👩‍👩‍👧‍👦",
	"\x1b]52;c;aGk=\x07",
	"\u202ereversed",
}

func assertFits(t *testing.T, name string, lines []string, r layout.Rect) {
	t.Helper()
	if len(lines) != r.H {
		t.Errorf("%s produced %d lines for a %dx%d rect", name, len(lines), r.W, r.H)
		return
	}
	for i, line := range lines {
		if w := textx.Width(line); w != r.W {
			t.Errorf("%s line %d is %d cells, want %d: %q", name, i, w, r.W, textx.Abbrev(line))
		}
	}
}

func hostileIndex(t *testing.T) *index.Index {
	t.Helper()
	fsys := fstest.MapFS{}
	for i, name := range hostile {
		fsys[textx.Sanitize(name)+"-"+string(rune('a'+i))+"/state.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	fsys["plain/logs/run.log"] = &fstest.MapFile{Data: []byte("x")}
	ix := index.New(fsx.New("ws", fsys), index.Limits{})
	for _, row := range ix.Rows() {
		ix.Expand(row.Node.Path)
	}
	ix.Drain()
	return ix
}

func TestTreeFitsEveryRect(t *testing.T) {
	t.Parallel()
	ix := hostileIndex(t)
	var tree pane.Tree
	for _, r := range rects {
		assertFits(t, "tree", tree.View(ix, r, theme.Plain(), func(*index.Node) bool { return true }), r)
	}
}

// A collapsed directory that has been read says how much is inside it, so the
// reader can decide whether opening it is worth a keystroke.
func TestTreeDisclosureCarriesItsCount(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/one.json": {Data: []byte(`{}`)}, "a/two.json": {Data: []byte(`{}`)},
	}
	ix := index.New(fsx.New("ws", fsys), index.Limits{})
	ix.Expand("a")
	ix.Drain()
	ix.Collapse("a")

	var tree pane.Tree
	out := strings.Join(tree.View(ix, layout.Rect{W: 40, H: 6}, theme.Plain(), noRecent), "\n")
	if !strings.Contains(out, "(2)") {
		t.Fatalf("a read but closed directory does not say how much it holds:\n%s", out)
	}
}

// Recency must survive a palette that draws no colour, which is the palette
// every golden frame renders under.
func TestTreeRecencyIsVisibleWithoutColour(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(`{}`)}}
	ix := index.New(fsx.New("ws", fsys), index.Limits{})
	ix.Expand("a")
	ix.Drain()

	var tree pane.Tree
	r := layout.Rect{W: 40, H: 6}
	plain := strings.Join(tree.View(ix, r, theme.Plain(), noRecent), "\n")
	marked := strings.Join(tree.View(ix, r, theme.Plain(), func(*index.Node) bool { return true }), "\n")

	if plain == marked {
		t.Fatal("a changed row renders identically to an unchanged one under the plain palette")
	}
	if strings.ContainsRune(marked, 0x1B) {
		t.Fatal("the plain palette emitted an escape sequence")
	}
}

func TestTreeNavigationMovesAndExpands(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a/x.json": {Data: []byte(`{}`)}, "b/y.json": {Data: []byte(`{}`)}}
	ix := index.New(fsx.New("ws", fsys), index.Limits{})

	var tree pane.Tree
	first, _ := tree.Selected(ix)
	tree.Update(keys.ActionDown, ix, 10)
	second, _ := tree.Selected(ix)
	if first == second {
		t.Fatal("moving down did not change the selection")
	}

	tree.Update(keys.ActionUp, ix, 10)
	tree.Update(keys.ActionExpand, ix, 10)
	ix.Drain()
	if _, ok := ix.Lookup("a/x.json"); !ok {
		t.Fatal("expanding did not read the directory")
	}
}

func TestAgentBarFitsEveryRect(t *testing.T) {
	t.Parallel()
	agents := []workspace.Agent{
		{Name: "researcher", Presence: workspace.PresenceDeclared,
			State: agentstate.State{Status: agentstate.StatusRunning, Task: strings.Repeat("long ", 200)}},
		{Name: "writer", Presence: workspace.PresenceStale, State: agentstate.State{Status: agentstate.StatusDone}},
		{Name: "broken", Presence: workspace.PresenceInvalid},
		{Name: "gone", Presence: workspace.PresenceUnreadable},
		{Name: "quiet", Presence: workspace.PresenceAbsent},
	}
	var bar pane.AgentBar
	for _, r := range rects {
		assertFits(t, "agent bar", bar.View(agents, r, theme.Plain()), r)
	}
}

// A bar that dropped agents says how many, rather than looking complete.
func TestAgentBarCountsWhatItCouldNotShow(t *testing.T) {
	t.Parallel()
	agents := make([]workspace.Agent, 8)
	for i := range agents {
		agents[i] = workspace.Agent{
			Name: "agent-" + string(rune('a'+i)), Presence: workspace.PresenceDeclared,
			State: agentstate.State{Status: agentstate.StatusRunning},
		}
	}
	out := strings.Join((pane.AgentBar{}).View(agents, layout.Rect{W: 60, H: 1}, theme.Plain()), "")
	if !strings.Contains(out, "more") {
		t.Fatalf("a bar that dropped agents did not say so: %q", out)
	}
}

// The five ways an agent's state can be known must be distinguishable on
// screen, or an operator cannot tell "idle" from "said nothing".
func TestPresenceStatesRenderDistinctly(t *testing.T) {
	t.Parallel()
	presences := []workspace.Presence{
		workspace.PresenceDeclared, workspace.PresenceStale, workspace.PresenceSettling,
		workspace.PresenceInvalid, workspace.PresenceUnreadable, workspace.PresenceAbsent,
	}
	seen := map[string]workspace.Presence{}
	for _, p := range presences {
		a := workspace.Agent{Name: "a", Presence: p, State: agentstate.State{Status: agentstate.StatusIdle}}
		out := strings.Join((pane.AgentBar{}).View([]workspace.Agent{a}, layout.Rect{W: 80, H: 1}, theme.Plain()), "")
		if prev, dup := seen[out]; dup {
			t.Errorf("%v and %v render identically: %q", prev, p, strings.TrimSpace(out))
		}
		seen[out] = p
	}
}

func TestFeedFollowsUntilTheReaderScrolls(t *testing.T) {
	t.Parallel()
	f := pane.NewFeed(100)
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{
		{Path: "a", Op: watch.OpCreate},
		{Path: "b", Op: watch.OpCreate},
		{Path: "c", Op: watch.OpCreate},
	}})
	if !f.Following() {
		t.Fatal("a fresh feed is not following")
	}

	if !f.Update(keys.ActionDown, 5) {
		t.Fatal("the feed ignored a scroll it could satisfy")
	}
	f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: "d", Op: watch.OpCreate}}})
	if f.Following() {
		t.Fatal("the feed resumed following after the reader scrolled")
	}
	if !strings.Contains(f.Badge(), "newer") {
		t.Fatalf("an unfollowed feed does not count what arrived: %q", f.Badge())
	}

	f.Update(keys.ActionFollow, 5)
	if !f.Following() {
		t.Fatal("following was not resumed")
	}
}

// A ring that discarded entries says so rather than presenting itself as the
// whole record.
func TestFeedReportsWhatItDiscarded(t *testing.T) {
	t.Parallel()
	f := pane.NewFeed(4)
	for i := range 10 {
		f.Push(watch.Batch{At: clock, Changes: []watch.Change{
			{Path: "f" + string(rune('a'+i)), Op: watch.OpCreate},
		}})
	}
	if f.Len() != 4 {
		t.Fatalf("the feed holds %d entries, want its capacity of 4", f.Len())
	}
	if !strings.Contains(f.Badge(), "discarded") {
		t.Fatalf("the feed does not report what it dropped: %q", f.Badge())
	}
}

func TestFeedFitsEveryRect(t *testing.T) {
	t.Parallel()
	f := pane.NewFeed(100)
	for _, name := range hostile {
		f.Push(watch.Batch{At: clock, Changes: []watch.Change{{Path: name, Op: watch.OpModify}}})
	}
	for _, r := range rects {
		assertFits(t, "feed", f.View(r, theme.Plain()), r)
	}
}

// An identity read off a directory name is a guess, and the pane says so.
func TestRunsDistinguishDeclaredFromInferredIdentity(t *testing.T) {
	t.Parallel()
	runs := []workspace.Run{
		{ID: "eval-a", Agent: "a", Dir: "a/runs/run-001", Declared: true, StartedAt: clock},
		{ID: "run-002", Agent: "a", Dir: "a/runs/run-002", StartedAt: clock},
	}
	out := strings.Join((pane.Runs{}).View(runs, layout.Rect{W: 60, H: 4}, theme.Plain(), clock), "\n")

	if !strings.Contains(out, "eval-a") {
		t.Errorf("a declared identity was not shown:\n%s", out)
	}
	if !strings.Contains(out, "inferred") {
		t.Errorf("a guessed identity was not marked as one:\n%s", out)
	}
}

func TestRunsFitEveryRect(t *testing.T) {
	t.Parallel()
	runs := []workspace.Run{
		{ID: strings.Repeat("run-", 400), Agent: "a", Dir: "a/x", StartedAt: clock},
		{ID: "b", Agent: "a", Dir: "a/b", Declared: true, Status: agentstate.StatusError},
	}
	for _, r := range rects {
		assertFits(t, "runs", (pane.Runs{}).View(runs, r, theme.Plain(), clock), r)
	}
}

// A healthy observer says the view is complete; a degraded one names what it
// is missing, and says how much it could not fit.
func TestStatusLineRanksAndCountsConditions(t *testing.T) {
	t.Parallel()
	healthy := (pane.Status{}).View(watch.Stats{}, nil, layout.Rect{W: 80, H: 1}, theme.Plain())
	if !strings.Contains(healthy[0], "complete view") {
		t.Fatalf("a healthy observer did not say so: %q", healthy[0])
	}

	stats := watch.Stats{RootLost: true, Dropped: 5, Errors: 2, WatchesRefused: 9}
	conditions := pane.Conditions(stats, index.Stats{TruncatedDirs: 3, UnreadableDirs: 1}, 4)
	if len(conditions) < 5 {
		t.Fatalf("collected %d conditions, want every one that is live", len(conditions))
	}

	narrow := (pane.Status{}).View(stats, conditions, layout.Rect{W: 60, H: 1}, theme.Plain())
	if !strings.Contains(narrow[0], "root unreadable") {
		t.Errorf("the most serious condition was dropped: %q", narrow[0])
	}
	if !strings.Contains(narrow[0], "more") {
		t.Errorf("conditions that did not fit were dropped in silence: %q", narrow[0])
	}
}

func TestStatusLineFitsEveryRect(t *testing.T) {
	t.Parallel()
	stats := watch.Stats{RootLost: true, Dropped: 99999, Errors: 42, WatchesRefused: 8192}
	conditions := pane.Conditions(stats, index.Stats{TruncatedDirs: 7, DepthTruncated: 2, NodeCeilingHit: true}, 3)
	for _, r := range rects {
		assertFits(t, "status", (pane.Status{}).View(stats, conditions, r, theme.Plain()), r)
	}
}

func TestHelpFitsEveryRect(t *testing.T) {
	t.Parallel()
	var help pane.Help
	for _, r := range rects {
		assertFits(t, "help", help.View(keys.Default(), r, theme.Plain()), r)
	}
}

// Every pane is a tab stop with a key scope, so focus cannot land somewhere
// with no bindings.
// The pane is drawn over whatever the registry holds, including a record wide
// enough to overrun every column.
func TestBudgetsFitEveryRect(t *testing.T) {
	t.Parallel()
	var budgets pane.Budgets
	for _, r := range rects {
		assertFits(t, "budgets", budgets.View(budgetFixture(), r, theme.Plain()), r)
		assertFits(t, "budgets empty", budgets.View(nil, r, theme.Plain()), r)
	}
}

// Every pane is a tab stop with a key scope, so focus cannot land somewhere
// with no bindings.
func TestEveryFocusablePaneHasAScope(t *testing.T) {
	t.Parallel()
	for _, ring := range [][]pane.ID{pane.FocusRing(), pane.RunsFocusRing()} {
		for _, id := range ring {
			if got := keys.Default().ForScope(id.Scope()); len(got) == 0 {
				t.Errorf("pane %v is focusable and its scope %v binds nothing", id, id.Scope())
			}
		}
	}
}

func noRecent(*index.Node) bool { return false }
