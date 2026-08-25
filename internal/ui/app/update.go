package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// batchMsg carries one change batch from the observer.
type batchMsg watch.Batch

// loadedMsg carries the result of reading one directory.
type loadedMsg index.Loaded

// previewMsg carries a loaded file window.
type previewMsg struct {
	path   string
	window *fileview.Window
}

// scanMsg carries a workspace scan.
type scanMsg workspace.Result

// runsMsg carries the runs recorded under one agent.
type runsMsg struct {
	agent string
	runs  []workspace.Run
}

// tickMsg drives recency decay, which is a change to what the frame shows that
// no message from the workspace announces.
type tickMsg time.Time

// Update implements [tea.Model].
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.key(msg)

	case tea.WindowSizeMsg:
		// Geometry is computed here and nowhere else. View reads the stored
		// frame, so a resize costs one computation rather than one per render.
		m.frame = layout.Compute(layout.Rect{W: msg.Width, H: msg.Height}, m.mode)
		return m, nil

	case batchMsg:
		return m.batch(watch.Batch(msg))

	case loadedMsg:
		m.index.Adopt(index.Loaded(msg))
		cmd := m.walkReveal()
		return m, cmd

	case previewMsg:
		if msg.path == m.preview.Path() {
			m.window = msg.window
			m.preview.Search(m.window, m.preview.Query())
		}
		return m, nil

	case scanMsg:
		res := workspace.Result(msg)
		m.agents, m.scanDiags = res.Agents, res.Diagnostics
		return m, nil

	case runsMsg:
		if msg.agent == m.runsFor {
			m.runs = msg.runs
		}
		return m, nil

	case tickMsg:
		// A workspace nobody is writing produces no batch, and staleness is
		// computed by the scan rather than by the clock. Without a rescan on
		// the tick, an agent that stopped without declaring a terminal status
		// reads as running until an operator presses reload — which is the one
		// signal the tool exists to surface.
		return m, tea.Batch(m.rescan(), tick())
	}
	return m, nil
}

// batch applies one change batch: the feed records it, the index reloads only
// the directories it names, and the agent bar is rescanned.
func (m *Model) batch(b watch.Batch) (tea.Model, tea.Cmd) {
	done := m.metrics.Budget(metrics.BudgetEventToFrame, metrics.DeadlineEventToFrame).Record(m.now)
	defer done()

	m.stats = b.Stats
	if b.RootRecovered {
		m.rootRecoveries++
	}
	if !b.Seeded {
		m.feed.Push(b)
		m.index.Apply(b)
	}

	cmds := []tea.Cmd{m.waitForBatch(), m.readPending(), m.rescan()}
	if m.touchesPreview(b) {
		cmds = append(cmds, m.loadPreview(m.preview.Path()))
	}
	if m.mode == layout.ModeRuns && m.runsFor != "" {
		cmds = append(cmds, m.loadRuns(m.runsFor))
	}
	return m, tea.Batch(cmds...)
}

// touchesPreview reports whether the batch names the file on display.
func (m *Model) touchesPreview(b watch.Batch) bool {
	p := m.preview.Path()
	if p == "" {
		return false
	}
	if b.Resync || b.RootRecovered {
		return true
	}
	for _, c := range b.Changes {
		if c.Path == p {
			return true
		}
	}
	return false
}

// key routes a press through the binding registry.
func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done := m.metrics.Budget(metrics.BudgetKeyToFrame, metrics.DeadlineKeyToFrame).Record(m.now)
	defer done()

	press := msg.String()
	if m.searching {
		return m.searchKey(msg, press), nil
	}

	action, bound := m.keys.Resolve(press, m.scope())
	if !bound {
		return m, nil
	}
	return m.act(action)
}

// searchKey handles the search prompt, where every key the registry does not
// bind is text.
func (m *Model) searchKey(msg tea.KeyPressMsg, press string) tea.Model {
	action, bound := m.keys.Resolve(press, keys.ScopeSearch)
	if bound {
		switch action {
		case keys.ActionOpen, keys.ActionNextMatch, keys.ActionPrevMatch:
			// Submitting the query and moving to a match are the same gesture:
			// a reader who typed a query wants to be at a hit rather than
			// wherever they were reading when they started typing.
			m.searching = false
			m.preview.Search(m.window, m.search)
			height := boxInner(m.frame.Preview).H
			if action == keys.ActionPrevMatch {
				// Submitting backwards asks for the hit before the first, which
				// is the last one.
				m.preview.Update(keys.ActionPrevMatch, m.window, height)
				return m
			}
			m.preview.ShowMatch(m.window, height)
			return m
		case keys.ActionCancel, keys.ActionClearSearch:
			m.searching = false
			m.search = ""
			m.preview.Search(m.window, "")
			return m
		default:
		}
	}
	switch press {
	case "backspace":
		if r := []rune(m.search); len(r) > 0 {
			m.search = string(r[:len(r)-1])
		}
	case "space":
		m.search += " "
	default:
		// A press that renders as one grapheme is text; anything longer is a
		// named key the prompt does not bind.
		if msg.Text != "" {
			m.search += msg.Text
		}
	}
	return m
}

func (m *Model) act(a keys.Action) (tea.Model, tea.Cmd) {
	switch a {
	case keys.ActionQuit:
		m.quitting = true
		return m, tea.Quit

	case keys.ActionToggleHelp:
		if m.mode == layout.ModeHelp {
			m.mode = layout.ModeBrowse
		} else {
			m.mode = layout.ModeHelp
		}
		m.reflow()
		return m, nil

	case keys.ActionToggleBudgets:
		if m.mode == layout.ModeBudgets {
			m.mode = layout.ModeBrowse
		} else {
			m.mode = layout.ModeBudgets
		}
		m.reflow()
		return m, nil

	case keys.ActionToggleRuns:
		return m.toggleRuns()

	case keys.ActionReload:
		m.index.Rebuild()
		return m, tea.Batch(m.readPending(), m.rescan(), m.loadPreview(m.preview.Path()))

	case keys.ActionNextPane:
		m.focus = nextInRing(m.ring(), m.focus, 1)
		return m, nil

	case keys.ActionPrevPane:
		m.focus = nextInRing(m.ring(), m.focus, -1)
		return m, nil

	case keys.ActionSearch:
		m.searching, m.search = true, ""
		return m, nil

	case keys.ActionCancel:
		if m.mode == layout.ModeHelp || m.mode == layout.ModeBudgets {
			m.mode = layout.ModeBrowse
			m.reflow()
			return m, nil
		}
		if m.mode == layout.ModeRuns {
			return m.toggleRuns()
		}
		// With no mode to leave, what is left to escape is the search. The
		// highlight outlives the prompt that started it, and the pane carrying
		// it is on screen whichever pane holds focus.
		m.preview.Update(keys.ActionClearSearch, m.window, boxInner(m.frame.Preview).H)
		return m, nil
	default:
	}
	return m.paneAction(a)
}

// paneAction routes an action to the focused pane.
func (m *Model) paneAction(a keys.Action) (tea.Model, tea.Cmd) {
	if m.mode == layout.ModeHelp {
		m.help.Update(a, m.keys, boxInner(m.frame.Help).H)
		return m, nil
	}
	if m.mode == layout.ModeBudgets {
		m.budgets.Update(a, m.metrics.Budgets(), boxInner(m.frame.Budgets).H)
		return m, nil
	}

	switch m.focus {
	case pane.IDTree:
		before, _ := m.tree.Selected(m.index)
		m.tree.Update(a, m.index, boxInner(m.frame.Left).H)
		after, ok := m.tree.Selected(m.index)
		var cmds []tea.Cmd
		if reqs := m.index.Pending(); len(reqs) > 0 {
			cmds = append(cmds, m.readPending())
		}
		if ok && after != before && !after.IsDir {
			m.preview.SetPath(after.Path)
			cmds = append(cmds, m.loadPreview(after.Path))
		}
		return m, tea.Batch(cmds...)

	case pane.IDPreview:
		m.preview.Update(a, m.window, boxInner(m.frame.Preview).H)
		return m, nil

	case pane.IDFeed:
		m.feed.Update(a, boxInner(m.frame.Feed).H)
		if a != keys.ActionOpen {
			return m, nil
		}
		// An entry names a path; opening it is showing that path, which is the
		// tree's and the preview's work rather than the feed's.
		entry, ok := m.feed.Selected()
		if !ok {
			return m, nil
		}
		cmd := m.reveal(entry.Change.Path, entry.Change.IsDir)
		return m, cmd

	case pane.IDRuns:
		m.runList.Update(a, m.runs, boxInner(m.frame.Left).H)
		if a == keys.ActionOpen {
			if run, ok := m.runList.Selected(m.runs); ok {
				m.mode, m.focus = layout.ModeBrowse, pane.IDTree
				m.reflow()
				cmd := m.reveal(run.Dir, true)
				return m, cmd
			}
		}
		return m, nil
	}
	return m, nil
}

// reveal shows path: the tree opens down to it and takes the cursor there, and
// the preview loads it unless it is a directory. isDir is what the caller knows
// about the path, which the index overrides where it holds the node.
//
// Opening a directory the index has not read costs a read, so a path below one
// cannot be walked in a single update. The request is held and walked again as
// each read arrives.
func (m *Model) reveal(path string, isDir bool) tea.Cmd {
	if path == "" || path == "." {
		return nil
	}
	if n, ok := m.index.Lookup(path); ok {
		isDir = n.IsDir
	}
	m.revealing = path
	if isDir {
		return m.walkReveal()
	}
	m.preview.SetPath(path)
	return tea.Batch(m.walkReveal(), m.loadPreview(path))
}

// walkReveal opens as much of the held path as the index can reach and reads
// what it cannot, dropping the request once the row is on screen or nothing is
// left to read. With no request held it is [Model.readPending] alone.
func (m *Model) walkReveal() tea.Cmd {
	if target := m.revealing; target != "" {
		for _, dir := range ancestorsOf(target) {
			if n, ok := m.index.Lookup(dir); !ok || !n.IsDir {
				break
			}
			m.index.Expand(dir)
		}
		if n, ok := m.index.Lookup(target); ok && n.IsDir {
			m.index.Expand(target)
		}
		if m.tree.Select(m.index, target, boxInner(m.frame.Left).H) {
			m.revealing = ""
		}
	}

	cmd := m.readPending()
	if cmd == nil {
		// Nothing is left to read, so the path is either on screen or absent
		// from the workspace, and holding the request would outlive its answer.
		m.revealing = ""
	}
	return cmd
}

// ancestorsOf returns the directories above p, outermost first.
func ancestorsOf(p string) []string {
	var out []string
	for i := range len(p) {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	return out
}

// toggleRuns enters or leaves the run-history mode, which replaces the tree in
// the left pane.
func (m *Model) toggleRuns() (tea.Model, tea.Cmd) {
	if m.mode == layout.ModeRuns {
		m.mode, m.focus = layout.ModeBrowse, pane.IDTree
		m.reflow()
		return m, nil
	}

	agent := m.agentUnderCursor()
	if agent == "" {
		return m, nil
	}
	m.mode, m.focus, m.runsFor = layout.ModeRuns, pane.IDRuns, agent
	m.runs = nil
	m.reflow()
	load := m.loadRuns(agent)
	return m, load
}

// agentUnderCursor returns the agent workspace the tree selection lies within.
func (m *Model) agentUnderCursor() string {
	node, ok := m.tree.Selected(m.index)
	if !ok {
		if len(m.agents) > 0 {
			return m.agents[0].Dir
		}
		return ""
	}
	top := node.Path
	for {
		parent := parentDir(top)
		if parent == "." {
			return top
		}
		top = parent
	}
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func (m *Model) ring() []pane.ID {
	if m.mode == layout.ModeRuns {
		return pane.RunsFocusRing()
	}
	return pane.FocusRing()
}

// nextInRing moves focus by step, wrapping.
func nextInRing(ring []pane.ID, current pane.ID, step int) pane.ID {
	for i, id := range ring {
		if id == current {
			return ring[((i+step)%len(ring)+len(ring))%len(ring)]
		}
	}
	return ring[0]
}

// reflow recomputes the geometry after a mode change, which is the only other
// thing that changes it.
func (m *Model) reflow() {
	m.frame = layout.Compute(m.frame.Term, m.mode)
}
