// Package app is the Bubble Tea model at the root of agentfs.
//
// It is the only type in the repository that satisfies [tea.Model]. The panes
// below it are plain values with cursors, which is what lets them be exercised
// by calling their methods instead of by driving a terminal.
//
// Update performs no filesystem work. Reading a directory on a network export
// can block for the mount's timeout, so every read is a command run off the
// update goroutine and applied when its message arrives. The index names what
// it needs, the command reads it, and the model adopts the result.
package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/metrics"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/keys"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/watch"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// Options wires a model to its collaborators.
type Options struct {
	// Root is the workspace being observed.
	Root *fsx.Root
	// Observer supplies change batches. The model reads it with exactly one
	// outstanding command at a time.
	Observer watch.Observer
	// Config carries the ceilings.
	Config config.Config
	// Palette resolves semantic roles to styles.
	Palette theme.Palette
	// Keys is the binding registry. The default registry is used when nil.
	Keys *keys.Registry
	// Metrics records the response-time budgets. A registry is created when
	// nil.
	Metrics *metrics.Registry
	// Now is the model's clock.
	Now func() time.Time
}

// Model is the application.
type Model struct {
	root    *fsx.Root
	obs     watch.Observer
	cfg     config.Config
	keys    *keys.Registry
	pal     theme.Palette
	metrics *metrics.Registry
	now     func() time.Time

	index   *index.Index
	scanner *workspace.Scanner
	agents  []workspace.Agent
	runs    []workspace.Run
	runsFor string
	window  *fileview.Window
	stats   watch.Stats

	// scanDiags are the last scan's findings about the workspace itself, as
	// distinct from findings about one agent's document.
	scanDiags []diag.Diagnostic
	// rootRecoveries counts the times the root was lost and reopened. The
	// observer reports a recovery on the batch that carries it and never
	// again, so the count is kept here.
	rootRecoveries uint64

	frame layout.Frame
	mode  layout.Mode
	focus pane.ID

	tree    pane.Tree
	preview pane.Preview
	feed    pane.Feed
	runList pane.Runs
	help    pane.Help
	budgets pane.Budgets
	bar     pane.AgentBar
	status  pane.Status

	searching bool
	search    string
	quitting  bool

	// revealing is the path a reveal is walking towards. A directory the index
	// has not read cannot be opened in the update that asked for it, so the
	// request outlives the press and is retried as each read lands.
	revealing string
}

// New returns a model over the workspace in opts.
func New(opts Options) *Model {
	if opts.Keys == nil {
		opts.Keys = keys.Default()
	}
	if opts.Metrics == nil {
		opts.Metrics = metrics.NewRegistry()
		metrics.DefaultBudgets(opts.Metrics)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	limits := index.Limits{
		MaxDepth:         opts.Config.MaxDepth,
		MaxEntriesPerDir: opts.Config.MaxEntriesPerDir,
		MaxNodes:         opts.Config.MaxNodes,
		RecentFor:        recentFor,
	}
	m := &Model{
		root:    opts.Root,
		obs:     opts.Observer,
		cfg:     opts.Config,
		keys:    opts.Keys,
		pal:     opts.Palette,
		metrics: opts.Metrics,
		now:     opts.Now,
		index:   index.NewDeferred(opts.Root, limits),
		scanner: workspace.New(opts.Root, workspace.Options{
			Now:              opts.Now,
			StaleAfter:       opts.Config.StaleAfter,
			SkewTolerance:    opts.Config.SkewTolerance,
			MaxDocumentBytes: opts.Config.MaxDocumentBytes,
			MaxExtraBytes:    opts.Config.MaxExtraBytes,
			SettleReads:      settleReads(opts.Config.Strict),
		}),
		feed:  pane.NewFeed(opts.Config.MaxFeedEntries),
		mode:  layout.ModeBrowse,
		focus: pane.IDTree,
	}
	if m.obs != nil {
		m.stats = m.obs.Stats()
	}
	return m
}

// recentFor is how long after a change a row is marked recent. It is long
// enough to catch the eye and short enough that a busy workspace does not end
// up entirely marked.
const recentFor = 3 * time.Second

// rescanInterval is how often the agent bar is rebuilt when no change arrives,
// so a document that went stale is reported without waiting for a write.
const rescanInterval = time.Second

func settleReads(strict bool) int {
	if strict {
		return 1
	}
	return 2
}

// Init implements [tea.Model].
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.readPending(), m.waitForBatch(), m.rescan(), tick())
}

// View implements [tea.Model].
func (m *Model) View() tea.View {
	v := tea.NewView(m.frameString())
	v.AltScreen = true
	v.WindowTitle = "agentfs " + m.root.Name()
	return v
}

// frameString assembles the frame from the geometry computed at the last resize.
func (m *Model) frameString() string {
	f := m.frame
	c := render.NewCanvas(f.Term)

	if f.TooSmall {
		c.Place(f.Status, render.TooSmall(m.pal, f.Status))
		return c.String()
	}

	c.Place(f.Title, m.titleRow(f.Title))
	c.Place(f.AgentBar, m.bar.View(m.agents, f.AgentBar, m.pal))

	if m.mode == layout.ModeHelp {
		c.Place(f.Help, render.Box{
			Rect: f.Help, Title: "Keys", Badge: "? closes", Focused: true,
		}.Render(m.pal, m.help.View(m.keys, boxInner(f.Help), m.pal)))
		c.Place(f.Status, m.statusRow(f.Status))
		return c.String()
	}

	if m.mode == layout.ModeBudgets {
		// The record is read as the frame is drawn rather than held from the
		// press that opened the overlay, so an operator watching a slow
		// workspace sees the counts move under a session that keeps running.
		stats := m.metrics.Budgets()
		c.Place(f.Budgets, render.Box{
			Rect: f.Budgets, Title: "Response-time budgets", Badge: m.budgets.Badge(stats),
			Focused: true,
		}.Render(m.pal, m.budgets.View(stats, boxInner(f.Budgets), m.pal)))
		c.Place(f.Status, m.statusRow(f.Status))
		return c.String()
	}

	c.Place(f.Left, m.leftBox(f.Left))
	c.Place(f.Preview, render.Box{
		Rect: f.Preview, Title: m.previewTitle(), Badge: m.preview.Badge(m.window),
		Focused: m.focus == pane.IDPreview,
	}.Render(m.pal, m.preview.View(m.window, boxInner(f.Preview), m.pal)))
	c.Place(f.Feed, render.Box{
		Rect: f.Feed, Title: "Activity", Badge: m.feed.Badge(),
		Focused: m.focus == pane.IDFeed,
	}.Render(m.pal, m.feed.View(boxInner(f.Feed), m.pal)))
	c.Place(f.Status, m.statusRow(f.Status))
	return c.String()
}

func (m *Model) leftBox(r layout.Rect) []string {
	if m.mode == layout.ModeRuns {
		return render.Box{
			Rect: r, Title: "Runs · " + m.runsFor, Badge: m.runList.Badge(m.runs),
			Focused: m.focus == pane.IDRuns,
		}.Render(m.pal, m.runList.View(m.runs, boxInner(r), m.pal, m.now()))
	}
	return render.Box{
		Rect: r, Title: "Files", Badge: m.tree.Badge(m.index),
		Focused: m.focus == pane.IDTree,
	}.Render(m.pal, m.tree.View(m.index, boxInner(r), m.pal, m.recent))
}

// boxInner is the content rect of a box drawn at r.
func boxInner(r layout.Rect) layout.Rect { return render.Box{Rect: r}.Inner() }

func (m *Model) titleRow(r layout.Rect) []string {
	left := m.pal.Title().Render(" agentfs ") + " " + m.pal.Dim().Render(textx.Sanitize(m.root.Name()))
	if m.searching {
		left += "  " + m.pal.Accent().Render("/"+textx.Sanitize(m.search)+"▏")
		return render.Rows([]string{textx.Fit(left, r.W)}, r)
	}
	footer := m.keys.Footer(m.scope(), max(r.W-textx.Width(left)-2, 0))
	return render.Rows([]string{textx.Fit(left+"  "+footer, r.W)}, r)
}

func (m *Model) statusRow(r layout.Rect) []string {
	return m.status.View(m.stats, pane.Conditions(m.stats, m.index.Stats(), m.invalidDocs()), r, m.pal)
}

func (m *Model) invalidDocs() int {
	n := 0
	for _, a := range m.agents {
		if a.Presence == workspace.PresenceInvalid || a.Presence == workspace.PresenceUnreadable {
			n++
		}
	}
	return n
}

func (m *Model) previewTitle() string {
	if p := m.preview.Path(); p != "" {
		return textx.Sanitize(p)
	}
	return "Preview"
}

// recent reports whether a node changed inside the recency window. Recency is
// also carried by a glyph, so it survives a palette that draws no colour.
func (m *Model) recent(n *index.Node) bool {
	return !n.ChangedAt.IsZero() && m.now().Sub(n.ChangedAt) < recentFor
}

// scope is the key scope presses resolve in.
func (m *Model) scope() keys.Scope {
	if m.searching {
		return keys.ScopeSearch
	}
	if m.mode == layout.ModeHelp || m.mode == layout.ModeBudgets {
		return keys.ScopeGlobal
	}
	return m.focus.Scope()
}

// Agents returns the agents the last scan found, for a caller assembling a
// report from the same state the frame renders.
func (m *Model) Agents() []workspace.Agent { return m.agents }

// Stats returns the observer's state as the model last saw it.
func (m *Model) Stats() watch.Stats { return m.stats }

// Diagnostics returns every finding the model holds: what limits the observer,
// what the last scan found about the workspace, and what it found in each
// agent's document.
//
// The observer's findings come first because a bounded store must not shed the
// reason it is incomplete, and because a ceiling qualifies the findings under
// it: a report from a lost root lists no agent because none could be read, not
// because none exist.
//
// The list is bounded by [config.Config.MaxDiagnostics]. Beyond it the tail is
// counted rather than listed, so a workspace with one defect repeated across
// ten thousand documents costs a bounded report rather than an unbounded one.
func (m *Model) Diagnostics() []diag.Diagnostic {
	out := observerDiagnostics(m.stats, m.index.Stats(), m.rootRecoveries)
	out = append(out, m.scanDiags...)
	for _, a := range m.agents {
		out = append(out, a.Diagnostics...)
	}
	if n := m.cfg.MaxDiagnostics; n > 0 && len(out) > n {
		out = append(out[:n-1], shedDiagnostic(len(out)-(n-1)))
	}
	return out
}

// SchemaVersion reports the contract version the model reads documents under.
func (m *Model) SchemaVersion() string { return agentstate.SchemaVersion }
