package app_test

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
)

// wide is a terminal roomy enough that an assertion about what a pane says is
// about the wording rather than about what survived truncation.
var wide = layout.Rect{W: 200, H: 40}

// cursorGlyph marks the selected row of every list pane.
var cursorGlyph = theme.Plain().Glyphs().Cursor

const (
	longLog  = "agent-solver/logs/run.log"
	shortLog = "agent-solver/logs/tail.log"
)

// sessionFixture is a workspace carrying the conditions one session moves
// through: an agent with two recorded runs, one declaring its identity and one
// leaving it to be inferred from the directory name; a log long enough to
// scroll and to hold several search matches; a log short enough that an
// appended line lands inside the viewport; and a second agent whose directory
// stays closed, so a change naming it is a change the open tree has no reason
// to reread.
func sessionFixture() fstest.MapFS {
	return fstest.MapFS{
		"agent-solver/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"running","task":"solve","step":1,"steps_total":4}`)},
		longLog:  {Data: []byte(longLogBody())},
		shortLog: {Data: []byte("2026-04-08 12:59:58 INFO  [solver] opened\n")},
		"agent-solver/runs/run-001/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"done","run_id":"eval-alpha"}`)},
		"agent-solver/runs/run-001/trace.jsonl": {Data: []byte(`{"step":1}` + "\n")},
		"agent-solver/runs/run-002/output.txt":  {Data: []byte("partial\n")},
		"agent-tracer/state.json":               {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)},
	}
}

// longLogBody is forty numbered lines, three of which carry the word the search
// tests query for.
func longLogBody() string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		word := "step"
		if i%13 == 0 {
			word = "marker"
		}
		fmt.Fprintf(&b, "2026-04-08 12:59:59 INFO  [solver] %s line-%03d\n", word, i)
	}
	return b.String()
}

// walkTo moves the tree cursor onto a path, opening the directories above it.
func walkTo(h *harness, target string) {
	h.t.Helper()
	for range 200 {
		at := h.selectedPath()
		if at == target {
			return
		}
		node, ok := h.model.Index().Lookup(at)
		if ok && node.IsDir && !node.Expanded() && strings.HasPrefix(target, node.Path+"/") {
			h.key("enter")
			continue
		}
		h.key("j")
	}
	h.t.Fatalf("the cursor never reached %q:\n%s", target, h.frame())
}

// marked reports whether the frame row carrying text also carries the selection
// marker, which is how a list says which row a key would act on.
func marked(h *harness, text string) bool {
	h.t.Helper()
	for _, line := range h.lines() {
		if strings.Contains(line, text) {
			return strings.Contains(line, cursorGlyph)
		}
	}
	h.t.Fatalf("no row carries %q:\n%s", text, h.frame())
	return false
}

func mustContain(h *harness, what, want string) {
	h.t.Helper()
	if !strings.Contains(h.frame(), want) {
		h.t.Errorf("%s: the frame does not carry %q:\n%s", what, want, h.frame())
	}
}

// An operator reading a run list has to know which identities the agent stated
// and which agentfs guessed from a directory name, because the two are worth
// different amounts of trust.
func TestRunsModeDistinguishesADeclaredRunFromAnInferredOne(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("R")

		frame := h.frame()
		if !strings.Contains(frame, "Runs · agent-solver") {
			t.Fatalf("the left pane did not become the run history:\n%s", frame)
		}
		mustContain(h, "run history", "2 runs · 1 declared")

		declared, inferred := "", ""
		for _, line := range h.lines() {
			if strings.Contains(line, "eval-alpha") {
				declared = line
			}
			if strings.Contains(line, "run-002") {
				inferred = line
			}
		}
		if declared == "" || inferred == "" {
			t.Fatalf("the run list does not hold both runs:\n%s", frame)
		}
		if strings.Contains(declared, "inferred") {
			t.Errorf("a run that declared its identity is marked as guessed: %q", declared)
		}
		if !strings.Contains(inferred, "(inferred)") {
			t.Errorf("a run whose identity came from its directory name does not say so: %q", inferred)
		}
	})
}

// Both ways out of the run history return the operator to the tree, so a mode
// entered by one key is not a mode only that key can leave.
func TestRunsModeIsLeftByEscapeAndByItsOwnKey(t *testing.T) {
	t.Parallel()
	for _, leave := range []string{"esc", "R"} {
		t.Run(leave, func(t *testing.T) {
			t.Parallel()
			run(t, sessionFixture(), wide, func(h *harness) {
				h.key("R")
				if !strings.Contains(h.frame(), "Runs · agent-solver") {
					t.Fatalf("R did not enter the run history:\n%s", h.frame())
				}

				h.key(leave)
				frame := h.frame()
				if strings.Contains(frame, "Runs · agent-solver") {
					t.Fatalf("%s did not leave the run history:\n%s", leave, frame)
				}
				if !strings.Contains(frame, "Files") {
					t.Fatalf("%s left the run history without restoring the tree:\n%s", leave, frame)
				}
			})
		})
	}
}

// Opening a run is how an operator gets from "this run happened" to the files
// it wrote, so it has to land in the tree with that run's directory open.
func TestOpeningARunBrowsesItsDirectory(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, "agent-solver/runs")
		h.key("enter")

		if strings.Contains(h.frame(), "trace.jsonl") {
			t.Fatal("the run's own directory is open before it was asked for")
		}

		h.key("R")
		if !marked(h, "eval-alpha") {
			t.Fatalf("the run history did not open on its first run:\n%s", h.frame())
		}
		h.key("enter")

		frame := h.frame()
		if strings.Contains(frame, "Runs · agent-solver") {
			t.Fatalf("opening a run stayed in the run history:\n%s", frame)
		}
		if !strings.Contains(frame, "trace.jsonl") {
			t.Fatalf("opening a run did not open its directory in the tree:\n%s", frame)
		}
	})
}

// The run history replaces the tree in the focus ring rather than adding to it:
// a Tab that reached a pane the mode does not draw would send keys nowhere.
func TestFocusInRunsModeCyclesTheRunListRatherThanTheTree(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("R")
		if !marked(h, "eval-alpha") {
			t.Fatalf("the run history did not open on its first run:\n%s", h.frame())
		}

		h.key("j")
		if !marked(h, "run-002") {
			t.Fatalf("the focused run list ignored a movement key:\n%s", h.frame())
		}

		h.key("tab")
		h.key("k")
		if !marked(h, "run-002") {
			t.Error("a key moved the run list while another pane held focus")
		}

		h.key("tab")
		h.key("tab")
		h.key("k")
		if !marked(h, "eval-alpha") {
			t.Errorf("three tab presses did not return focus to the run list:\n%s", h.frame())
		}
	})
}

// Reload exists for the workspace that changed without the observer hearing
// about it. It has to reread the filesystem and leave the operator where they
// were: a reload that closed every directory would cost more to recover from
// than it saved.
func TestReloadRereadsTheWorkspaceAndKeepsOpenDirectories(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("enter")
		mustContain(h, "an opened workspace", "state.json")

		h.fs["agent-solver/notes.md"] = &fstest.MapFile{Data: []byte("written elsewhere\n")}
		h.key("r")

		frame := h.frame()
		if !strings.Contains(frame, "notes.md") {
			t.Errorf("reload did not reread the workspace:\n%s", frame)
		}
		if !strings.Contains(frame, "state.json") {
			t.Errorf("reload closed a directory the operator had open:\n%s", frame)
		}
	})
}

// A batch that reports a resynchronization is not an account of what changed,
// so applying it would leave the tree disagreeing with the filesystem. It has
// to be taken as an instruction to rebuild.
func TestAResyncBatchRebuildsRatherThanApplying(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("enter")
		mustContain(h, "an opened workspace", "state.json")

		h.fs["agent-solver/out-of-band.md"] = &fstest.MapFile{Data: []byte("written elsewhere\n")}

		h.emit(watch.Change{Path: "agent-tracer/state.json", Op: watch.OpModify})
		if strings.Contains(h.frame(), "out-of-band.md") {
			t.Fatal("a batch naming a closed directory rebuilt an open one")
		}

		h.send(app.BatchMsgForTest(watch.Batch{Resync: true, At: clock}))
		if !strings.Contains(h.frame(), "out-of-band.md") {
			t.Fatalf("a resynchronized batch did not rebuild from the filesystem:\n%s", h.frame())
		}
	})
}

// While the root is unreadable the last frame is the only account of the
// workspace an operator has. Blanking it would discard that account and say
// nothing about why.
func TestRootLossIsReportedWithoutBlankingTheFrame(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, shortLog)
		mustContain(h, "a selected log", "opened")

		h.send(app.BatchMsgForTest(watch.Batch{
			RootLost: true, At: clock, Stats: watch.Stats{RootLost: true},
		}))

		frame := h.frame()
		if !strings.Contains(frame, "root unreadable") {
			t.Errorf("a lost root was not reported on the status line:\n%s", frame)
		}
		if !strings.Contains(frame, "agent-solver") {
			t.Errorf("the tree was blanked while the root was unreadable:\n%s", frame)
		}
		if !strings.Contains(frame, "opened") {
			t.Errorf("the open file was dropped while the root was unreadable:\n%s", frame)
		}
	})
}

// Nothing was observed during the outage, so recovery is a rebuild and the
// status line has to stop reporting a condition that ended.
func TestRootRecoveryClearsTheReportAndPicksUpWhatWasMissed(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("enter")
		h.send(app.BatchMsgForTest(watch.Batch{
			RootLost: true, At: clock, Stats: watch.Stats{RootLost: true},
		}))
		mustContain(h, "a lost root", "root unreadable")

		h.fs["agent-solver/during-outage.md"] = &fstest.MapFile{Data: []byte("missed\n")}
		h.send(app.BatchMsgForTest(watch.Batch{RootRecovered: true, Resync: true, At: clock}))

		frame := h.frame()
		if strings.Contains(frame, "root unreadable") {
			t.Errorf("a recovered root is still reported as lost:\n%s", frame)
		}
		if !strings.Contains(frame, "during-outage.md") {
			t.Errorf("recovery did not pick up what changed during the outage:\n%s", frame)
		}
	})
}

// Both ends of a log stay one key away. The line an operator wants is as often
// the first as the last, and a viewport that could only walk there a row at a
// time would make the far end of a long file unreachable in practice.
func TestPreviewReachesEitherEndOfALog(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, longLog)
		mustContain(h, "a selected log", "line-001")

		h.key("tab")
		h.key("G")
		frame := h.frame()
		if !strings.Contains(frame, "line-040") {
			t.Errorf("G did not reach the end of the log:\n%s", frame)
		}
		if strings.Contains(frame, "line-001") {
			t.Errorf("G left the start of the log on screen:\n%s", frame)
		}

		h.key("g")
		if !strings.Contains(h.frame(), "line-001") {
			t.Errorf("g did not return to the start of the log:\n%s", h.frame())
		}
	})
}

// A change naming the file on display has to reload it. An agent's log is
// written to while it is being read, and a preview that held the bytes from
// the first read would show an account of the run that the run has left behind.
func TestABatchTouchingTheOpenFileReloadsIt(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, shortLog)
		mustContain(h, "a selected log", "opened")

		h.fs[shortLog] = &fstest.MapFile{Data: []byte(
			"2026-04-08 12:59:58 INFO  [solver] opened\n2026-04-08 13:00:01 WARN  [solver] appended\n")}
		h.emit(watch.Change{Path: shortLog, Op: watch.OpModify})

		if !strings.Contains(h.frame(), "appended") {
			t.Fatalf("a change to the open file did not reload it:\n%s", h.frame())
		}
	})
}

// A change to a file nobody has open must not cost a read of the preview, so
// the reload is driven by the batch naming that file rather than by any batch
// arriving.
func TestABatchElsewhereLeavesTheOpenFileAlone(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, shortLog)
		mustContain(h, "a selected log", "opened")

		h.fs[shortLog] = &fstest.MapFile{Data: []byte(
			"2026-04-08 12:59:58 INFO  [solver] opened\n2026-04-08 13:00:01 WARN  [solver] appended\n")}
		h.emit(watch.Change{Path: "agent-tracer/state.json", Op: watch.OpModify})

		if strings.Contains(h.frame(), "appended") {
			t.Fatalf("a change naming another file reloaded the open one:\n%s", h.frame())
		}
	})
}

// Cycling matches is how a reader gets through a query's hits, and the badge is
// the only thing telling them where in the hits they are.
func TestSearchCyclesMatchesAndReportsThePosition(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, longLog)
		h.key("tab")

		h.key("/")
		h.typeText("marker")
		h.key("enter")
		mustContain(h, "a submitted query", `1/3 for "marker"`)
		mustContain(h, "a submitted query", "line-013")

		h.key("n")
		mustContain(h, "the next match", `2/3 for "marker"`)
		mustContain(h, "the next match", "line-026")

		h.key("N")
		mustContain(h, "the previous match", `1/3 for "marker"`)

		h.key("N")
		mustContain(h, "wrapping backwards", `3/3 for "marker"`)
		mustContain(h, "wrapping backwards", "line-039")
	})
}

// Submitting with the previous-match key asks for the hit before the first,
// which is the last one, while enter lands on the first hit rather than moving
// past it.
func TestSubmittingSearchBackwardsLandsOnTheLastMatch(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, longLog)
		h.key("tab")

		h.key("/")
		h.typeText("marker")
		h.key("ctrl+p")

		mustContain(h, "a query submitted backwards", `3/3 for "marker"`)
	})
}

// Discarding a query has to take the highlight with it, or the reader is left
// with matches marked for a search they abandoned.
func TestDiscardingTheQueryClearsTheMatches(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, longLog)
		h.key("tab")

		h.key("/")
		h.typeText("marker")
		h.key("enter")
		mustContain(h, "a submitted query", `for "marker"`)

		h.key("/")
		h.key("esc")

		frame := h.frame()
		if strings.Contains(frame, `for "marker"`) {
			t.Errorf("discarding the query left its matches reported:\n%s", frame)
		}
		if strings.Contains(frame, "/marker") {
			t.Errorf("discarding the query left the prompt on screen:\n%s", frame)
		}
	})
}

// The prompt is a text field: it has to take back a character and take a space,
// or a query with either in it cannot be typed.
func TestSearchPromptEditsTheQuery(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("tab")
		h.key("/")
		h.key("backspace")
		h.typeText("marks")
		h.key("backspace")
		h.key("backspace")
		h.key("space")
		h.typeText("up")

		if !strings.Contains(h.frame(), "/mar up") {
			t.Fatalf("the prompt does not hold what was typed:\n%s", h.frame())
		}
	})
}

// A query that nothing matches is reported as such. Saying nothing would read
// as a query that was never submitted.
func TestSearchReportsAQueryNothingMatches(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		walkTo(h, longLog)
		h.key("tab")

		h.key("/")
		h.typeText("nowhere")
		h.key("enter")

		mustContain(h, "an unmatched query", `no match for "nowhere"`)
	})
}

// A terminal is resized while the session runs, in every mode, and the frame is
// exactly the terminal's shape after each one.
func TestResizingMidSessionRecomputesEveryMode(t *testing.T) {
	t.Parallel()
	sizes := []layout.Rect{{W: 200, H: 40}, {W: 60, H: 16}, {W: 132, H: 50}, {W: 40, H: 12}, {W: 90, H: 24}}

	for _, mode := range []string{"browse", "runs", "help"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			run(t, sessionFixture(), wide, func(h *harness) {
				walkTo(h, longLog)
				switch mode {
				case "runs":
					h.key("R")
				case "help":
					h.key("?")
				}
				for _, size := range sizes {
					h.send(tea.WindowSizeMsg{Width: size.W, Height: size.H})
					h.assertFits(size)
				}
			})
		})
	}
}

// The run list survives a resize with the mode it belongs to, so an operator
// who widened the terminal is not returned to the tree.
func TestResizingInRunsModeKeepsTheRunHistory(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("R")
		h.send(tea.WindowSizeMsg{Width: 240, Height: 60})
		h.assertFits(layout.Rect{W: 240, H: 60})

		mustContain(h, "a resized run history", "Runs · agent-solver")
		mustContain(h, "a resized run history", "eval-alpha")
	})
}

// The report surface and the frame are two readings of one scan. A report that
// named agents the screen does not, or omitted a finding the status line
// counts, would describe a workspace nobody can check against.
func TestReportSurfaceAgreesWithTheFrame(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"agent-good/state.json":   {Data: []byte(`{"schema":"agentfs/v1","status":"running","task":"index"}`)},
		"agent-broken/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
	}
	run(t, fsys, wide, func(h *harness) {
		frame := h.frame()

		agents := h.model.Agents()
		if len(agents) != 2 {
			t.Fatalf("the scan found %d agents, want 2", len(agents))
		}
		for _, a := range agents {
			if !strings.Contains(frame, a.Dir) {
				t.Errorf("the agent bar does not name %q:\n%s", a.Dir, frame)
			}
		}

		if len(h.model.Diagnostics()) == 0 {
			t.Errorf("a workspace the status line calls invalid produced no diagnostic:\n%s", frame)
		}
		if !strings.Contains(frame, "state documents invalid") {
			t.Errorf("an undecodable document was not reported on the status line:\n%s", frame)
		}

		if got, want := h.model.Stats(), h.obs.Stats(); got != want {
			t.Errorf("the model reports %+v, want the observer's %+v", got, want)
		}
		if h.model.Observer() != h.obs {
			t.Error("the model names a change source it was not given")
		}
		if _, ok := h.model.Index().Lookup("agent-good"); !ok {
			t.Error("the index the model exposes does not hold a directory the frame renders")
		}
		if got := h.model.SchemaVersion(); got != agentstate.SchemaVersion {
			t.Errorf("the model reads documents under %q, want %q", got, agentstate.SchemaVersion)
		}
	})
}

// A source's first delivery says the source is watching; it is not an account
// of what changed. Recording it would open a session with an activity feed full
// of files nobody touched.
func TestASeededBatchIsNotRecordedAsActivity(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.send(app.BatchMsgForTest(watch.Batch{
			Seeded: true, At: clock,
			Changes: []watch.Change{{Path: "agent-solver/state.json", Op: watch.OpCreate, At: clock}},
		}))

		frame := h.frame()
		if !strings.Contains(frame, "0 entries") {
			t.Errorf("a seeded batch was recorded as activity:\n%s", frame)
		}
	})
}

// A key no binding claims is a key that does nothing. Falling through to the
// focused pane would give every unbound press whatever the pane's default is.
func TestAnUnboundKeyChangesNothing(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		before := h.frame()
		for _, press := range []string{"z", "Q", "@"} {
			h.key(press)
			if h.frame() != before {
				t.Fatalf("%q is bound to nothing yet changed the frame:\n%s", press, h.frame())
			}
		}
	})
}

// One key closes every overlay, so an operator who wants out of the help
// overlay does not have to remember which key opened it.
func TestEscapeClosesTheHelpOverlay(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), wide, func(h *harness) {
		h.key("?")
		mustContain(h, "the help overlay", "? closes")

		h.key("esc")
		frame := h.frame()
		if strings.Contains(frame, "? closes") {
			t.Errorf("escape did not close the help overlay:\n%s", frame)
		}
		if !strings.Contains(frame, "agent-solver") {
			t.Errorf("closing the help overlay did not restore the tree:\n%s", frame)
		}
	})
}

// The help overlay scrolls the key list rather than the pane underneath it: a
// movement key there must reach what is on screen.
func TestTheHelpOverlayTakesMovementKeys(t *testing.T) {
	t.Parallel()
	run(t, sessionFixture(), layout.Rect{W: 100, H: 20}, func(h *harness) {
		h.key("?")
		before := h.frame()

		h.key("G")
		if h.frame() == before {
			t.Fatalf("the help overlay ignored a movement key:\n%s", before)
		}
	})
}

// A workspace with no agent has no run history to show, and asking for one
// leaves the operator in the tree rather than in an empty mode they have to
// find their way out of.
func TestRunHistoryHoldsInTheTreeWhenThereIsNoAgent(t *testing.T) {
	t.Parallel()
	run(t, fstest.MapFS{}, wide, func(h *harness) {
		h.key("R")

		frame := h.frame()
		if strings.Contains(frame, "Runs · ") {
			t.Fatalf("an empty workspace entered the run history:\n%s", frame)
		}
		if !strings.Contains(frame, "empty workspace") {
			t.Fatalf("the tree does not say the workspace is empty:\n%s", frame)
		}
	})
}

// A caller assembling a report supplies no change source. The workspace is
// still read and still rendered, rather than the session waiting on a channel
// that will never carry a batch.
func TestAModelWithoutAnObserverBrowsesTheWorkspace(t *testing.T) {
	t.Parallel()
	runUnobserved(t, sessionFixture(), standard, func(h *harness) {
		if h.model.Observer() != nil {
			t.Error("a model built without a change source reports one")
		}
		h.assertFits(standard)

		h.key("enter")
		mustContain(h, "a workspace read without a change source", "state.json")
	})
}

// A workspace nobody is writing produces no batch. Staleness is computed by the
// scan, so without a rescan on the tick an agent that stopped without declaring
// a terminal status reads as running until an operator presses reload.
func TestAQuietWorkspaceStillGoesStale(t *testing.T) {
	// The document is fresh when the model first scans it, so the frame cannot
	// report staleness until the clock has moved AND something rescanned. A
	// fixture that is already stale at the first scan asserts nothing about the
	// tick.
	fsys := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"running","heartbeat_seconds":60,` +
				`"updated_at":"2026-04-08T13:00:00Z"}`)},
	}
	run(t, fsys, standard, func(h *harness) {
		stale := h.model.Palette().Glyphs().Stale

		if strings.Contains(h.frame(), stale) {
			t.Fatalf("a fresh document was reported stale before the clock moved:\n%s", h.frame())
		}

		// Nothing writes to the workspace; only the clock moves.
		h.advance(5 * time.Minute)

		if strings.Contains(h.frame(), stale) {
			t.Fatalf("the frame changed without anything rescanning:\n%s", h.frame())
		}

		h.tick()

		if !strings.Contains(h.frame(), stale) {
			t.Fatalf("a lapsed heartbeat on a quiet workspace was not reported:\n%s", h.frame())
		}
	})
}
