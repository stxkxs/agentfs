package app_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/watch"
)

var standard = layout.Rect{W: 100, H: 30}

// Every frame is exactly the terminal's shape, whatever the workspace holds.
func TestFrameFitsEveryTerminalSize(t *testing.T) {
	t.Parallel()
	sizes := []layout.Rect{
		{W: 60, H: 16}, {W: 61, H: 17}, {W: 80, H: 24}, {W: 100, H: 30},
		{W: 200, H: 60}, {W: 400, H: 200}, {W: 40, H: 10}, {W: 1, H: 1},
	}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			t.Parallel()
			run(t, workspaceFixture(), size, func(h *harness) {
				h.assertFits(size)
				h.key("j")
				h.key("enter")
				h.assertFits(size)
			})
		})
	}
}

// A workspace whose content is hostile must not break the frame.
func TestFrameSurvivesHostileContent(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a" + strings.Repeat("長", 200) + "/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"b/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"running","task":"` + strings.Repeat("x", 3000) + `"}`)},
		"b/logs/hostile.log": {Data: []byte(
			"\x1b]52;c;aGk=\x07clipboard\n\u202ereversed\n" + strings.Repeat("y", 4000) + "\n")},
	}
	run(t, fsys, standard, func(h *harness) {
		h.key("j")
		h.key("enter")
		h.key("j")
		h.key("enter")
		h.assertFits(standard)

		if strings.ContainsRune(h.frame(), 0x1B) {
			t.Fatal("workspace content reached the frame carrying an escape sequence")
		}
	})
}

// Geometry is computed on resize and nowhere else, so rendering is free of it.
func TestFrameIsStableAcrossRenders(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		first := h.frame()
		for range 5 {
			if got := h.frame(); got != first {
				t.Fatal("rendering the same state twice produced different frames")
			}
		}
	})
}

func TestTooSmallTerminalRendersOneMessage(t *testing.T) {
	t.Parallel()
	size := layout.Rect{W: 30, H: 8}
	run(t, workspaceFixture(), size, func(h *harness) {
		h.assertFits(size)
		if !strings.Contains(h.frame(), "too small") {
			t.Fatalf("a terminal below the minimum did not say so:\n%s", h.frame())
		}
	})
}

// Every tab stop must handle at least one key, or focus lands somewhere inert.
func TestEveryTabStopRespondsToNavigation(t *testing.T) {
	t.Parallel()
	// Each pane needs more content than fits, so a failure is about the
	// binding rather than about a list with nothing below the fold.
	long := strings.Repeat("2026-04-08 12:59:59 INFO  [a] working\n", 200)
	fsys := fstest.MapFS{
		"agent-a/state.json":   {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"agent-a/logs/run.log": {Data: []byte(long)},
	}

	// The tree scrolls.
	run(t, fsys, standard, func(h *harness) {
		h.key("enter")
		before := h.frame()
		h.key("j")
		if h.frame() == before {
			t.Errorf("the tree pane ignored j:\n%s", before)
		}
	})

	// The preview scrolls the file the tree selected.
	run(t, fsys, standard, func(h *harness) {
		h.key("enter") // open agent-a
		h.key("j")     // logs/
		h.key("enter") // open logs/
		h.key("j")     // run.log, which loads the preview
		if !strings.Contains(h.frame(), "working") {
			t.Fatalf("the preview did not load the selected file:\n%s", h.frame())
		}

		h.key("tab")
		before := h.frame()
		h.key("j")
		if h.frame() == before {
			t.Errorf("the preview pane ignored j:\n%s", before)
		}
	})

	// The feed scrolls what it recorded.
	run(t, fsys, standard, func(h *harness) {
		changes := make([]watch.Change, 0, 60)
		for i := range 60 {
			changes = append(changes, watch.Change{
				Path: fmt.Sprintf("agent-a/logs/f%02d.log", i), Op: watch.OpCreate,
			})
		}
		h.emit(changes...)

		h.key("tab")
		h.key("tab")
		before := h.frame()
		h.key("j")
		if h.frame() == before {
			t.Errorf("the feed pane ignored j:\n%s", before)
		}
	})
}

func TestTabCyclesBackToWhereItStarted(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		start := h.frame()
		for range 3 {
			h.key("tab")
		}
		if h.frame() != start {
			t.Fatal("three tab presses did not return focus to the first pane")
		}
	})
}

// Expanding costs one directory read and shows the members.
func TestExpandingADirectoryShowsItsMembers(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		if strings.Contains(h.frame(), "state.json") {
			t.Fatal("a closed directory's members are on screen")
		}
		h.key("enter")
		if !strings.Contains(h.frame(), "state.json") {
			t.Fatalf("expanding did not reveal the directory's members:\n%s", h.frame())
		}
	})
}

func TestSelectingAFileLoadsThePreview(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		selectResearcherState(h)

		if !strings.Contains(h.frame(), "retrieval") {
			t.Fatalf("selecting a state document did not preview it:\n%s", h.frame())
		}
	})
}

func TestSearchPromptCapturesText(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		selectResearcherState(h)
		h.key("tab") // focus the preview
		h.key("/")
		h.typeText("retrieval")

		if !strings.Contains(h.frame(), "/retrieval") {
			t.Fatalf("the prompt did not show what was typed:\n%s", h.frame())
		}
		h.key("enter")
		if strings.Contains(h.frame(), "/retrieval▏") {
			t.Fatal("the prompt stayed open after the query was submitted")
		}
	})
}

func TestSearchPromptTakesKeysThatAreOtherwiseBound(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.key("tab")
		h.key("/")
		h.typeText("q")

		if !strings.Contains(h.frame(), "/q") {
			t.Fatalf("a bound key was not taken as text by the prompt:\n%s", h.frame())
		}
	})
}

func TestHelpOverlayListsBindings(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.key("?")

		frame := h.frame()
		for _, want := range []string{"Keys", "quit", "reload the workspace"} {
			if !strings.Contains(frame, want) {
				t.Errorf("the help overlay does not mention %q:\n%s", want, frame)
			}
		}
		h.key("?")
		if strings.Contains(h.frame(), "? closes") {
			t.Fatal("the help overlay did not close")
		}
	})
}

// The status line answers "am I seeing everything" rather than leaving it to be
// inferred from an empty feed.
func TestStatusLineReportsACompleteView(t *testing.T) {
	t.Parallel()
	healthy := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
	}
	run(t, healthy, standard, func(h *harness) {
		if !strings.Contains(h.frame(), "complete view") {
			t.Fatalf("a healthy observer did not say the view is complete:\n%s", h.frame())
		}
	})
}

// A workspace holding a document that declares nothing usable is not a
// complete view of what the agents are doing, and the status line says so.
func TestStatusLineReportsAnInvalidDocument(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		if !strings.Contains(h.frame(), "state documents invalid") {
			t.Fatalf("an invalid document was not reported on the status line:\n%s", h.frame())
		}
	})
}

func TestStatusLineReportsDegradation(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.send(app.BatchWithStatsForTest(watch.Stats{Dropped: 12, Errors: 3, WatchesRefused: 40}))

		frame := h.frame()
		if !strings.Contains(frame, "12 changes dropped") {
			t.Errorf("dropped changes were not reported:\n%s", frame)
		}
		if strings.Contains(frame, "complete view") {
			t.Error("a degraded observer still claimed a complete view")
		}
	})
}

func TestRootLossIsReportedOnScreen(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.send(app.BatchWithStatsForTest(watch.Stats{RootLost: true}))

		if !strings.Contains(h.frame(), "root unreadable") {
			t.Fatalf("a lost root was not reported:\n%s", h.frame())
		}
	})
}

// The feed records what arrives and says how much it holds.
func TestFeedRecordsChanges(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		h.emit(
			watch.Change{Path: "agent-researcher/state.json", Op: watch.OpModify},
			watch.Change{Path: "agent-writer/artifacts/new.md", Op: watch.OpCreate},
		)

		frame := h.frame()
		if !strings.Contains(frame, "state.json") {
			t.Errorf("a change did not reach the feed:\n%s", frame)
		}
		if !strings.Contains(frame, "2 entries") {
			t.Errorf("the feed does not say how much it holds:\n%s", frame)
		}
	})
}

func TestQuitStopsTheProgram(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), standard, func(h *harness) {
		_, cmd := h.model.Update(keyPress("q"))
		if cmd == nil {
			t.Fatal("q produced no command")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("q did not ask the program to quit")
		}
	})
}

// The agent bar distinguishes what an agent declared from what it failed to.
func TestAgentBarDistinguishesPresence(t *testing.T) {
	t.Parallel()
	// Wide enough for every agent, so the assertion is about what the bar says
	// rather than about what fits.
	run(t, workspaceFixture(), layout.Rect{W: 220, H: 30}, func(h *harness) {
		frame := h.frame()

		for _, want := range []string{"agent-researcher", "running", "agent-writer", "done"} {
			if !strings.Contains(frame, want) {
				t.Errorf("the agent bar does not mention %q:\n%s", want, frame)
			}
		}
		if !strings.Contains(frame, "invalid state") {
			t.Errorf("an undecodable document was not distinguished from a declared one:\n%s", frame)
		}
	})
}

// selectResearcherState moves the cursor onto agent-researcher's state
// document, which is the file every preview assertion reads.
func selectResearcherState(h *harness) {
	h.t.Helper()
	for range 20 {
		if strings.Contains(h.frame(), "retrieval") {
			return
		}
		if node, ok := h.model.Index().Lookup(h.selectedPath()); ok && node.IsDir {
			h.key("enter")
			continue
		}
		h.key("j")
	}
	h.t.Fatalf("agent-researcher/state.json was never selected:\n%s", h.frame())
}
