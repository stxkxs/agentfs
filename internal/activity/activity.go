package activity

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stxkxs/agentfs/internal/watcher"
)

const maxEntries = 100

var (
	styleTime   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleCreate = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleWrite  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleRemove = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleRename = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	stylePath   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

// Entry is a single activity feed item.
type Entry struct {
	Event watcher.Event
}

// Model is the Bubble Tea model for the activity feed.
type Model struct {
	entries []Entry
	width   int
	height  int
	offset  int
}

// New creates an empty activity feed model.
func New() Model {
	return Model{}
}

// Push adds a new event to the feed.
func (m *Model) Push(ev watcher.Event) {
	m.entries = append([]Entry{{Event: ev}}, m.entries...)
	if len(m.entries) > maxEntries {
		m.entries = m.entries[:maxEntries]
	}
}

// Len returns the number of entries.
func (m Model) Len() int {
	return len(m.entries)
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	return m, nil
}

func (m Model) View() string {
	if len(m.entries) == 0 {
		return styleTime.Render("  waiting for changes...")
	}

	h := m.height
	if h <= 0 {
		h = len(m.entries)
	}

	var b strings.Builder
	end := h
	if end > len(m.entries) {
		end = len(m.entries)
	}

	for i := 0; i < end; i++ {
		e := m.entries[i]
		ts := styleTime.Render(e.Event.Time.Format("15:04:05"))
		op := renderOp(e.Event.Op)
		path := stylePath.Render(e.Event.RelPath)
		b.WriteString(fmt.Sprintf("  %s %s %s", ts, op, path))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// SetSize sets the available viewport dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func renderOp(op watcher.Op) string {
	switch op {
	case watcher.Create:
		return styleCreate.Render("+++")
	case watcher.Write:
		return styleWrite.Render("~~~")
	case watcher.Remove:
		return styleRemove.Render("---")
	case watcher.Rename:
		return styleRename.Render("<->")
	default:
		return "???"
	}
}
