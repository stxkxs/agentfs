package app_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/watch"
)

// bindingLog is the file the preview keys are exercised over.
const bindingLog = "agent-a/logs/run.log"

// paneScope pairs a key scope with the pane that advertises it.
//
// [keys.Registry.ForScope] answers for a focused pane: the scope's own
// bindings and the global ones it falls back to. So the fixture focuses that
// pane and gives every one of those bindings something to act on — a list with
// rows above and below the cursor, a file long enough to scroll, matches to
// move between. A pane holding an empty list ignores a key for want of content
// rather than for want of a binding, and a fixture that left one empty would
// pass this test while proving nothing.
type paneScope struct {
	scope keys.Scope
	// focus leaves the pane owning scope in front and ready to be acted on.
	focus func(*harness)
}

// paneScopes are the scopes a pane owns. The global bindings are reached
// through all four, because that is where an operator meets them: a footer
// under a pane, offering a key the pane has to answer.
func paneScopes() []paneScope {
	return []paneScope{
		{keys.ScopeTree, focusTree},
		{keys.ScopePreview, focusPreview},
		{keys.ScopeFeed, focusFeed},
		{keys.ScopeRuns, focusRuns},
	}
}

// TestEveryAdvertisedBindingDoesSomething presses every key a pane's footer
// offers, in that pane, and requires the press to reach the frame or the model.
//
// Resolving a press is not answering it. A pane that resolves a key and drops
// it advertises work it does not do — in the footer, in the `?` overlay and in
// the generated reference at once, all three drawn from the binding it
// resolved. Pressing the key in the pane is what tells the two apart.
func TestEveryAdvertisedBindingDoesSomething(t *testing.T) {
	t.Parallel()

	for _, ps := range paneScopes() {
		advertised := keys.Default().ForScope(ps.scope)
		if len(advertised) == 0 {
			t.Fatalf("%v advertises nothing, so its pane went unchecked", ps.scope)
		}
		for _, b := range advertised {
			for _, key := range b.Keys {
				t.Run(ps.scope.String()+" "+key, func(t *testing.T) {
					t.Parallel()
					run(t, bindingFixture(), wide, func(h *harness) {
						focusScope(h, ps)
						assertPressActs(h, key, b)
					})
				})
			}
		}
	}
}

// TestAnUnansweredPressChangesNothing is the control for
// [TestEveryAdvertisedBindingDoesSomething]. A press the table does not claim
// must leave the model exactly as it was, or a binding could pass for landing
// on the strength of something the fixture does on its own.
func TestAnUnansweredPressChangesNothing(t *testing.T) {
	t.Parallel()

	const unbound = "z"
	if _, claimed := keys.Default().Resolve(unbound, keys.ScopeGlobal); claimed {
		t.Fatalf("%q is bound, so it is no control", unbound)
	}
	for _, ps := range paneScopes() {
		t.Run(ps.scope.String(), func(t *testing.T) {
			t.Parallel()
			run(t, bindingFixture(), wide, func(h *harness) {
				focusScope(h, ps)
				before := observe(h)
				h.key(unbound)
				if after := observe(h); after != before {
					h.t.Fatalf("%q is claimed by no binding and changed the model:\n%s", unbound, before.frame)
				}
			})
		})
	}
}

// focusScope runs a fixture's focus step and requires it to have landed, so a
// subtest named for one scope cannot quietly press its keys against another
// pane's table and pass on what that pane answered.
func focusScope(h *harness, ps paneScope) {
	h.t.Helper()
	ps.focus(h)
	if got := h.model.ScopeForTest(); got != ps.scope {
		h.t.Fatalf("the fixture left presses resolving in %v, so nothing here is about %v:\n%s",
			got, ps.scope, h.frame())
	}
}

// TestSubmittingAQueryPutsTheHitItReportsOnScreen covers the one advertised
// binding [TestEveryAdvertisedBindingDoesSomething] cannot reach: no pane owns
// [keys.ScopeSearch], and a press there closes the prompt, so requiring the
// frame to change would pass whether or not the key did what it says.
//
// The prompt binds enter to the next match. Locating the hits and moving to one
// are separate: the badge names a hit as soon as the query is submitted, and it
// takes an explicit move to bring that line into the viewport. The file is
// several screens long and the first hit is below the fold, so a submission
// that only counted the hits leaves the reader looking at the head of the file
// while the badge tells them they are at the first one.
func TestSubmittingAQueryPutsTheHitItReportsOnScreen(t *testing.T) {
	t.Parallel()

	run(t, bindingFixture(), wide, func(h *harness) {
		walkTo(h, bindingLog)
		h.key("tab")
		h.key("/")
		h.typeText("marker")
		h.key("enter")

		frame := h.frame()
		if !strings.Contains(frame, `1/5 for "marker"`) {
			t.Fatalf("the badge does not report the first of five hits:\n%s", frame)
		}
		if !strings.Contains(frame, "marker line-037") {
			t.Errorf("the badge reports a hit the frame is not showing:\n%s", frame)
		}
	})
}

// assertPressActs requires one press to change what a reader of the model can
// see. Quitting is the exception: it leaves the frame as it was by design, and
// says it landed by returning the command that ends the program.
func assertPressActs(h *harness, key string, b keys.Binding) {
	h.t.Helper()

	if b.Action == keys.ActionQuit {
		_, cmd := h.model.Update(keyPress(key))
		if cmd == nil {
			h.t.Fatalf("%q is advertised as %q and asked the program for nothing", key, b.Help)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			h.t.Fatalf("%q is advertised as %q and did not ask the program to quit", key, b.Help)
		}
		return
	}

	before := observe(h)
	h.key(key)
	if after := observe(h); after == before {
		h.t.Fatalf("%q is advertised as %q and changed nothing:\n%s", key, b.Help, before.frame)
	}
}

// modelState is what a press can change that a reader of the model can see: the
// frame it renders, the row the tree acts on, and what the index holds.
//
// Reloading rereads the workspace without moving anything on screen, so the
// index's read count is what shows that press landed.
type modelState struct {
	frame    string
	selected string
	index    index.Stats
}

func observe(h *harness) modelState {
	h.t.Helper()
	return modelState{
		frame:    h.frame(),
		selected: h.selectedPath(),
		index:    h.model.Index().Stats(),
	}
}

// focusTree leaves the tree focused with the cursor on a closed directory
// inside an open one, so expanding, collapsing and opening each have a
// directory to act on and there are rows on both sides to move to. The preview
// carries a submitted query, which is what discarding the search acts on.
func focusTree(h *harness) {
	h.t.Helper()
	searchPreview(h, "marker")
	h.key("shift+tab")
	walkTo(h, "agent-a/memory")
}

// focusPreview leaves the preview focused over a long file, held between its
// ends so scrolling can move either way, with matches ahead of and behind the
// viewport.
func focusPreview(h *harness) {
	h.t.Helper()
	searchPreview(h, "marker")
	h.key("G")
	h.key("ctrl+u")
}

// focusFeed leaves the feed focused over entries naming files the workspace
// holds, scrolled off the newest one so that following the tail again is a
// move and opening an entry has a file to show.
func focusFeed(h *harness) {
	h.t.Helper()
	searchPreview(h, "marker")
	h.emit(feedChanges()...)
	h.key("tab")
	h.key("j")
}

// focusRuns leaves the run history focused with the cursor on the middle of
// three runs, so moving in either direction lands on one.
func focusRuns(h *harness) {
	h.t.Helper()
	searchPreview(h, "marker")
	h.key("R")
	h.key("j")
}

// searchPreview opens the long log in the preview and submits a query over it,
// leaving the preview focused with matches to cycle and a highlight to discard.
func searchPreview(h *harness, query string) {
	h.t.Helper()
	walkTo(h, bindingLog)
	h.key("tab")
	h.key("/")
	h.typeText(query)
	h.key("enter")
}

// feedChanges name files the workspace holds, so the entry under the cursor is
// one the tree can walk to and the preview can load.
func feedChanges() []watch.Change {
	paths := []string{
		"agent-a/memory/context.json",
		"agent-b/state.json",
		"agent-c/state.json",
		"agent-a/state.json",
	}
	out := make([]watch.Change, 0, 40)
	for i := range cap(out) {
		out = append(out, watch.Change{Path: paths[i%len(paths)], Op: watch.OpModify})
	}
	return out
}

// bindingFixture is a workspace carrying what every pane's keys need: a tree
// deep enough to open and close, a log long enough to scroll with matches
// several screens apart, three recorded runs, and agent directories that stay
// closed until something opens them.
func bindingFixture() fstest.MapFS {
	fsys := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"running","task":"solve","step":1,"steps_total":4}`)},
		bindingLog:                    {Data: []byte(bindingLogBody())},
		"agent-a/memory/context.json": {Data: []byte(`{"facts":["a","b"]}`)},
		"agent-b/state.json":          {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)},
		"agent-c/state.json":          {Data: []byte(`{"schema":"agentfs/v1","status":"done"}`)},
	}
	for i := 1; i <= 3; i++ {
		fsys[fmt.Sprintf("agent-a/runs/run-%03d/state.json", i)] = &fstest.MapFile{Data: []byte(
			fmt.Sprintf(`{"schema":"agentfs/v1","status":"done","run_id":"eval-%03d"}`, i))}
	}
	return fsys
}

// bindingLogBody is two hundred numbered lines, every thirty-seventh carrying
// the word the query looks for, so a match is always more than a viewport away
// from the one before it.
func bindingLogBody() string {
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		word := "step"
		if i%37 == 0 {
			word = "marker"
		}
		fmt.Fprintf(&b, "2026-04-08 12:59:59 INFO  [solver] %s line-%03d\n", word, i)
	}
	return b.String()
}
