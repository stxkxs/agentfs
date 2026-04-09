package app

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stxkxs/agentfs/internal/activity"
	"github.com/stxkxs/agentfs/internal/agent"
	"github.com/stxkxs/agentfs/internal/tree"
	"github.com/stxkxs/agentfs/internal/viewer"
	"github.com/stxkxs/agentfs/internal/watcher"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	sectionTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12"))

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	agentBarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8"))

	modeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("11")).
			Padding(0, 1)
)

// WatcherEventMsg wraps a watcher event as a Bubble Tea message.
type WatcherEventMsg watcher.Event

// viewMode controls whether we show the normal view or runs view.
type viewMode int

const (
	modeNormal viewMode = iota
	modeRuns
)

// Model is the top-level application model.
type Model struct {
	tree     tree.Model
	viewer   viewer.Model
	activity activity.Model
	agents   []agent.Agent
	runs     []agent.Run
	watcher  *watcher.Watcher
	root     string
	width    int
	height   int
	focus    pane
	mode     viewMode
	runIdx   int // cursor in runs list
}

type pane int

const (
	paneTree pane = iota
	paneViewer
	paneActivity
)

// New creates the application model.
func New(root string, w *watcher.Watcher) Model {
	return Model{
		tree:     tree.New(root),
		viewer:   viewer.New(),
		activity: activity.New(),
		agents:   agent.Scan(root),
		watcher:  w,
		root:     root,
		focus:    paneTree,
	}
}

func (m Model) Init() tea.Cmd {
	return waitForEvent(m.watcher)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// When viewer is in search mode, pass all keys directly to it.
		if m.focus == paneViewer && m.viewer.Searching() {
			var cmd tea.Cmd
			m.viewer, cmd = m.viewer.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "R":
			if m.mode == modeNormal {
				m.mode = modeRuns
				m.runs = agent.ScanRuns(m.root)
				m.runIdx = 0
			} else {
				m.mode = modeNormal
			}
			return m, nil
		case "tab":
			switch m.focus {
			case paneTree:
				m.focus = paneViewer
			case paneViewer:
				m.focus = paneActivity
			case paneActivity:
				m.focus = paneTree
			}
			return m, nil
		case "shift+tab":
			switch m.focus {
			case paneTree:
				m.focus = paneActivity
			case paneViewer:
				m.focus = paneTree
			case paneActivity:
				m.focus = paneViewer
			}
			return m, nil
		case "r":
			m.tree.Reload()
			m.tree.ClearModified()
			m.viewer.Reload()
			m.agents = agent.Scan(m.root)
			if m.mode == modeRuns {
				m.runs = agent.ScanRuns(m.root)
			}
			return m, nil
		}

		// Runs mode navigation
		if m.mode == modeRuns && m.focus == paneTree {
			switch msg.String() {
			case "up", "k":
				if m.runIdx > 0 {
					m.runIdx--
				}
				return m, nil
			case "down", "j":
				if m.runIdx < len(m.runs)-1 {
					m.runIdx++
				}
				return m, nil
			case "enter":
				if m.runIdx >= 0 && m.runIdx < len(m.runs) {
					run := m.runs[m.runIdx]
					// Load the run dir into the tree and switch to normal browsing
					m.tree = tree.New(run.Dir)
					m.focus = paneTree
				}
				return m, nil
			case "esc":
				// Reset tree to root and exit runs mode.
				m.tree = tree.New(m.root)
				m.mode = modeNormal
				return m, nil
			}
		}

		switch m.focus {
		case paneTree:
			// On enter for a file, open it in the viewer.
			if msg.String() == "enter" && !m.tree.SelectedIsDir() && m.tree.SelectedPath() != "" {
				m.viewer.LoadFile(m.tree.SelectedPath(), m.tree.SelectedRelPath())
				return m, nil
			}

			prevPath := m.tree.SelectedPath()
			var cmd tea.Cmd
			m.tree, cmd = m.tree.Update(msg)

			// If selection changed and it's a file, load it in the viewer.
			newPath := m.tree.SelectedPath()
			if newPath != prevPath && !m.tree.SelectedIsDir() && newPath != "" {
				m.viewer.LoadFile(newPath, m.tree.SelectedRelPath())
			}
			return m, cmd

		case paneViewer:
			var cmd tea.Cmd
			m.viewer, cmd = m.viewer.Update(msg)
			return m, cmd
		}

	case WatcherEventMsg:
		ev := watcher.Event(msg)
		m.activity.Push(ev)
		m.tree.Reload()
		m.tree.MarkModified(ev.RelPath)

		// Re-scan agents on state file changes.
		if isAgentStateFile(ev.RelPath) {
			m.agents = agent.Scan(m.root)
		}

		// If the changed file is currently open in the viewer, reload it.
		if ev.RelPath == m.viewer.RelPath() {
			m.viewer.Reload()
		}

		return m, waitForEvent(m.watcher)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateSizes()
		return m, nil
	}

	return m, nil
}

func (m *Model) updateSizes() {
	treeWidth := m.width * 3 / 10
	if treeWidth < 28 {
		treeWidth = 28
	}
	rightWidth := m.width - treeWidth - 3

	agentBarH := 3
	contentHeight := m.height - agentBarH - 5
	if contentHeight < 8 {
		contentHeight = 8
	}

	viewerHeight := contentHeight * 3 / 5
	actHeight := contentHeight - viewerHeight - 1

	m.tree.SetSize(treeWidth-4, contentHeight-2)
	m.viewer.SetSize(rightWidth-4, viewerHeight-2)
	m.activity.SetSize(rightWidth-4, actHeight-2)
}

func (m Model) View() string {
	title := titleStyle.Render(" agentfs ") + "  " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.root)
	if m.mode == modeRuns {
		title += "  " + modeStyle.Render("RUNS")
	}

	// Agent status bar
	agentBar := agentBarStyle.
		Width(m.width - 4).
		Render(agent.RenderBar(m.agents))

	treeWidth := m.width * 3 / 10
	if treeWidth < 28 {
		treeWidth = 28
	}
	rightWidth := m.width - treeWidth - 3

	agentBarH := 3
	contentHeight := m.height - agentBarH - 5
	if contentHeight < 8 {
		contentHeight = 8
	}
	viewerHeight := contentHeight * 3 / 5
	actHeight := contentHeight - viewerHeight - 1

	// Border colors based on focus
	treeBorder := borderStyle
	viewBorder := borderStyle
	actBorder := borderStyle
	switch m.focus {
	case paneTree:
		treeBorder = treeBorder.BorderForeground(lipgloss.Color("12"))
	case paneViewer:
		viewBorder = viewBorder.BorderForeground(lipgloss.Color("12"))
	case paneActivity:
		actBorder = actBorder.BorderForeground(lipgloss.Color("12"))
	}

	// Left pane — tree or runs list
	var leftPane string
	if m.mode == modeRuns {
		runsHeader := sectionTitle.Render("  Runs")
		runsContent := agent.RenderRunList(m.runs, m.runIdx, contentHeight-3)
		leftPane = treeBorder.
			Width(treeWidth).
			Height(contentHeight).
			Render(runsHeader + "\n" + runsContent)
	} else {
		treeHeader := sectionTitle.Render("  Files")
		leftPane = treeBorder.
			Width(treeWidth).
			Height(contentHeight).
			Render(treeHeader + "\n" + m.tree.View())
	}

	// Viewer pane (top-right)
	viewHeader := sectionTitle.Render("  Preview")
	viewPane := viewBorder.
		Width(rightWidth).
		Height(viewerHeight).
		Render(viewHeader + "\n" + m.viewer.View())

	// Activity pane (bottom-right)
	actHeader := sectionTitle.Render(fmt.Sprintf("  Activity (%d)", m.activity.Len()))
	actPane := actBorder.
		Width(rightWidth).
		Height(actHeight).
		Render(actHeader + "\n" + m.activity.View())

	// Compose layout
	rightSide := lipgloss.JoinVertical(lipgloss.Left, viewPane, actPane)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightSide)

	var help string
	if m.mode == modeRuns {
		help = helpStyle.Render("  j/k navigate  enter browse run  esc back  R toggle runs  q quit")
	} else {
		help = helpStyle.Render("  j/k navigate  enter open/expand  / search  tab switch  R runs  r reload  q quit")
	}

	var view strings.Builder
	view.WriteString("\n " + title + "\n")
	view.WriteString(agentBar + "\n")
	view.WriteString(body + "\n")
	view.WriteString(help)

	return view.String()
}

// isAgentStateFile checks if a changed path is likely an agent state file.
func isAgentStateFile(relPath string) bool {
	base := filepath.Base(relPath)
	return base == "state.json" || base == "status.json" || base == "agent.json"
}

func waitForEvent(w *watcher.Watcher) tea.Cmd {
	return func() tea.Msg {
		ev := <-w.Events()
		return WatcherEventMsg(ev)
	}
}
