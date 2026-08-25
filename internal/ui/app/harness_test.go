package app_test

import (
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
)

// clock is the instant every frame is rendered at, so a golden file records the
// layout rather than the moment it was written.
var clock = time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)

// harness drives a model the way the runtime does: it applies a message, runs
// the commands the model returned on their own goroutines, and applies their
// messages in turn until nothing is left to run.
//
// It runs inside a [testing/synctest] bubble. Two of the model's commands wait
// by design — one reads the observer's channel and one sleeps until the next
// tick — and in a bubble "everything that can run has run" is a condition
// [synctest.Wait] reports rather than a duration to guess at. That is what
// makes running commands to quiescence deterministic instead of a race against
// a sleep.
type harness struct {
	t       *testing.T
	model   *app.Model
	obs     *watch.Manual
	fs      fstest.MapFS
	metrics *metrics.Registry
	msgs    chan tea.Msg

	// now is the model's clock, movable so a test can separate what a scan
	// reports from what the passage of time reports. A fixed clock makes any
	// assertion about staleness true before the thing that produces it runs.
	nowMu sync.Mutex
	now   time.Time
}

// run executes fn inside a synctest bubble with a harness over fsys.
//
// Closing the observer at the end releases the command that is blocked reading
// it, so every goroutine the bubble started exits and the bubble closes.
func run(t *testing.T, fsys fstest.MapFS, size layout.Rect, fn func(*harness)) {
	t.Helper()
	runWithConfig(t, fsys, size, nil, fn)
}

// runWithConfig drives a model whose ceilings the test sets, for a path a
// default configuration reaches only with a workspace the size of the ceiling.
func runWithConfig(t *testing.T, fsys fstest.MapFS, size layout.Rect, tune func(*config.Config), fn func(*harness)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, fsys, size, watch.NewManual(), tune)
		defer h.close()
		fn(h)
	})
}

// runUnobserved drives a model built without a change source, which is the
// shape a caller assembling a report from one scan constructs.
func runUnobserved(t *testing.T, fsys fstest.MapFS, size layout.Rect, fn func(*harness)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, fsys, size, nil, nil)
		defer h.close()
		fn(h)
	})
}

func newHarness(t *testing.T, fsys fstest.MapFS, size layout.Rect, obs *watch.Manual, tune func(*config.Config)) *harness {
	t.Helper()

	cfg := config.Defaults()
	cfg.Root = "ws"
	if tune != nil {
		tune(&cfg)
	}

	// A nil observer is passed as a nil interface rather than as an interface
	// holding a nil pointer, so the model sees the absence the option
	// documents.
	var source watch.Observer
	if obs != nil {
		source = obs
	}

	registry := metrics.NewRegistry()
	metrics.DefaultBudgets(registry)

	h := &harness{
		t: t, obs: obs, fs: fsys, metrics: registry,
		msgs: make(chan tea.Msg, 256), now: clock,
	}
	h.model = app.New(app.Options{
		Root:     fsx.New("ws", fsys),
		Observer: source,
		Config:   cfg,
		Palette:  theme.Plain(),
		Metrics:  registry,
		Now:      h.clock,
	})

	h.dispatch(h.model.Init())
	h.send(tea.WindowSizeMsg{Width: size.W, Height: size.H})
	return h
}

// close lets every outstanding command finish, which is what the bubble
// requires before it can close.
//
// Closing the observer releases the command blocked reading it. Sleeping past
// the tick interval releases the one waiting on a timer; inside a bubble that
// sleep costs no real time.
func (h *harness) close() {
	if h.obs != nil {
		_ = h.obs.Close()
	}
	time.Sleep(app.RescanIntervalForTest * 2)
	synctest.Wait()
	for {
		select {
		case <-h.msgs:
		default:
			return
		}
	}
}

// send applies a message and runs everything it produced.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	next, cmd := h.model.Update(msg)
	model, ok := next.(*app.Model)
	if !ok {
		h.t.Fatalf("Update returned %T, want *app.Model", next)
	}
	h.model = model
	h.dispatch(cmd)
}

// dispatch runs a command tree to quiescence.
//
// A tick message is dropped rather than applied: it re-arms itself, so
// delivering it would keep the harness running forever without changing what
// the frame shows.
func (h *harness) dispatch(cmd tea.Cmd) {
	h.t.Helper()
	h.spawn(cmd)

	for range 1000 {
		synctest.Wait()
		select {
		case msg := <-h.msgs:
			if app.IsTickMsgForTest(msg) {
				continue
			}
			next, follow := h.model.Update(msg)
			model, ok := next.(*app.Model)
			if !ok {
				h.t.Fatalf("Update returned %T, want *app.Model", next)
			}
			h.model = model
			h.spawn(follow)
		default:
			return
		}
	}
	h.t.Fatal("the model did not settle after a thousand commands")
}

// spawn runs one command, or every command of a batch, on its own goroutine.
func (h *harness) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		switch m := msg.(type) {
		case nil:
			return
		case tea.BatchMsg:
			for _, c := range m {
				h.spawn(c)
			}
			return
		default:
			select {
			case h.msgs <- msg:
			default:
			}
		}
	}()
}

// key sends a key press by the name the terminal layer renders it to.
func (h *harness) key(name string) {
	h.t.Helper()
	h.send(keyPress(name))
}

// typeText sends each rune of s as a key press carrying text, which is how a
// prompt receives input.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// emit delivers a change batch, as the observer would.
func (h *harness) emit(changes ...watch.Change) {
	h.t.Helper()
	for i := range changes {
		if changes[i].At.IsZero() {
			changes[i].At = clock
		}
	}
	h.send(app.BatchMsgForTest(watch.Batch{Changes: changes, At: clock}))
}

// frame renders the model.
func (h *harness) frame() string {
	h.t.Helper()
	return h.model.View().Content
}

// lines returns the rendered frame split into rows.
func (h *harness) lines() []string {
	h.t.Helper()
	return strings.Split(h.frame(), "\n")
}

// assertFits checks the invariant every frame must hold: exactly the terminal's
// height in lines, and exactly its width in cells on each.
func (h *harness) assertFits(size layout.Rect) {
	h.t.Helper()
	lines := h.lines()
	if len(lines) != size.H {
		h.t.Fatalf("frame has %d lines, want %d", len(lines), size.H)
	}
	for i, line := range lines {
		if w := textx.Width(line); w != size.W {
			h.t.Errorf("line %d is %d cells, want %d: %q", i, w, size.W, textx.Abbrev(line))
		}
	}
}

// keyPress builds the message the terminal layer produces for a key name.
func keyPress(name string) tea.KeyPressMsg {
	switch name {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	}
	if strings.HasPrefix(name, "ctrl+") {
		r := []rune(strings.TrimPrefix(name, "ctrl+"))
		return tea.KeyPressMsg{Code: r[0], Mod: tea.ModCtrl}
	}
	r := []rune(name)
	if len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: name}
	}
	return tea.KeyPressMsg{Code: tea.KeyExtended}
}

// workspaceFixture is the workspace every frame test renders, chosen to carry
// one of each condition the panes distinguish.
func workspaceFixture() fstest.MapFS {
	return fstest.MapFS{
		"agent-researcher/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"running","task":"retrieval","step":3,"steps_total":8,"model":"claude-opus-5"}`)},
		"agent-researcher/logs/run.log": {Data: []byte(
			"2026-04-08 12:59:58 INFO  [researcher] step=2\n2026-04-08 12:59:59 WARN  [researcher] retrying\n")},
		"agent-researcher/memory/context.json":     {Data: []byte(`{"facts":["a","b"]}`)},
		"agent-researcher/runs/run-001/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"done","run_id":"eval-a"}`)},
		"agent-writer/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"done","task":"publish","problem":"retried twice"}`)},
		"agent-writer/artifacts/draft.md": {Data: []byte("# draft\n")},
		"agent-broken/state.json":         {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
		"agent-torn/logs/x.log":           {Data: []byte("x\n")},
	}
}

// selectedPath returns the path under the tree cursor.
func (h *harness) selectedPath() string { return h.model.SelectedPathForTest() }

// tick delivers the recurring tick and lets what it produced settle, which is
// how a test drives the path a workspace nobody is writing depends on.
func (h *harness) tick() {
	h.t.Helper()
	h.send(app.TickMsgForTest(clock))
}

// clock is the model's view of the time. It is read from whichever goroutine a
// command runs on, so it is guarded.
func (h *harness) clock() time.Time {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	return h.now
}

// advance moves the model's clock forward without writing to the workspace,
// which is how a test separates what a scan reports from what the passage of
// time reports.
func (h *harness) advance(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
}
