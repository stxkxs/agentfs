package tree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleDir      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleFile     = lipgloss.NewStyle()
	styleCursor   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	styleModified = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

// Node represents a file or directory in the tree.
type Node struct {
	Name     string
	Path     string
	RelPath  string
	IsDir    bool
	Children []*Node
	Expanded bool
	Depth    int
	Modified bool // recently changed
}

// Model is the Bubble Tea model for the directory tree.
type Model struct {
	root     string
	rootNode *Node
	flat     []flatEntry // flattened visible rows
	cursor   int
	width    int
	height   int
}

type flatEntry struct {
	node  *Node
	depth int
}

// New creates a tree model for the given root directory.
func New(root string) Model {
	m := Model{root: root}
	m.Reload()
	return m
}

// Reload rescans the directory tree from disk, preserving expanded state.
func (m *Model) Reload() {
	expanded := m.expandedSet()
	m.rootNode = scanDir(m.root, m.root, 0)
	if m.rootNode != nil {
		m.rootNode.Expanded = true
		restoreExpanded(m.rootNode, expanded)
	}
	m.flatten()
}

func (m *Model) expandedSet() map[string]bool {
	set := make(map[string]bool)
	collectExpanded(m.rootNode, set)
	return set
}

func collectExpanded(n *Node, set map[string]bool) {
	if n == nil {
		return
	}
	if n.IsDir && n.Expanded {
		set[n.RelPath] = true
	}
	for _, c := range n.Children {
		collectExpanded(c, set)
	}
}

func restoreExpanded(n *Node, set map[string]bool) {
	if n == nil {
		return
	}
	if n.IsDir && set[n.RelPath] {
		n.Expanded = true
	}
	for _, c := range n.Children {
		restoreExpanded(c, set)
	}
}

// MarkModified marks a relative path as recently modified.
func (m *Model) MarkModified(relPath string) {
	parts := strings.Split(relPath, string(filepath.Separator))
	node := m.rootNode
	if node == nil {
		return
	}
	for _, part := range parts {
		found := false
		for _, child := range node.Children {
			if child.Name == part {
				node = child
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	node.Modified = true
}

// ClearModified clears all modified flags.
func (m *Model) ClearModified() {
	clearModified(m.rootNode)
}

func clearModified(n *Node) {
	if n == nil {
		return
	}
	n.Modified = false
	for _, c := range n.Children {
		clearModified(c)
	}
}

// SelectedPath returns the path of the currently selected node.
func (m Model) SelectedPath() string {
	if m.cursor >= 0 && m.cursor < len(m.flat) {
		return m.flat[m.cursor].node.Path
	}
	return ""
}

// SelectedRelPath returns the relative path of the currently selected node.
func (m Model) SelectedRelPath() string {
	if m.cursor >= 0 && m.cursor < len(m.flat) {
		return m.flat[m.cursor].node.RelPath
	}
	return ""
}

// SelectedIsDir returns whether the selected node is a directory.
func (m Model) SelectedIsDir() bool {
	if m.cursor >= 0 && m.cursor < len(m.flat) {
		return m.flat[m.cursor].node.IsDir
	}
	return false
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.flat)-1 {
				m.cursor++
			}
		case "enter", "right", "l":
			if m.cursor >= 0 && m.cursor < len(m.flat) {
				entry := m.flat[m.cursor]
				if entry.node.IsDir {
					entry.node.Expanded = !entry.node.Expanded
					m.flatten()
				}
			}
		case "left", "h":
			if m.cursor >= 0 && m.cursor < len(m.flat) {
				entry := m.flat[m.cursor]
				if entry.node.IsDir && entry.node.Expanded {
					entry.node.Expanded = false
					m.flatten()
				}
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.rootNode == nil {
		return "  (empty)"
	}

	var b strings.Builder
	start, end := m.visibleRange()

	for i := start; i < end; i++ {
		entry := m.flat[i]
		indent := strings.Repeat("  ", entry.depth)

		var prefix, name string
		if entry.node.IsDir {
			if entry.node.Expanded {
				prefix = "▼ "
			} else {
				prefix = "▶ "
			}
			name = styleDir.Render(entry.node.Name + "/")
		} else {
			prefix = "  "
			if entry.node.Modified {
				name = styleModified.Render(entry.node.Name)
			} else {
				name = styleFile.Render(entry.node.Name)
			}
		}

		line := indent + prefix + name
		if i == m.cursor {
			line = styleCursor.Render("│") + " " + indent + prefix + name
		} else {
			line = "  " + line
		}

		b.WriteString(line)
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

func (m *Model) flatten() {
	m.flat = m.flat[:0]
	if m.rootNode != nil {
		m.flattenNode(m.rootNode, 0)
	}
	if m.cursor >= len(m.flat) {
		m.cursor = len(m.flat) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) flattenNode(n *Node, depth int) {
	// Skip root node itself — show its children at depth 0
	if n == m.rootNode {
		if n.Expanded {
			for _, c := range n.Children {
				m.flattenNode(c, 0)
			}
		}
		return
	}

	m.flat = append(m.flat, flatEntry{node: n, depth: depth})
	if n.IsDir && n.Expanded {
		for _, c := range n.Children {
			m.flattenNode(c, depth+1)
		}
	}
}

func (m Model) visibleRange() (int, int) {
	h := m.height
	if h <= 0 {
		h = len(m.flat)
	}

	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	end := start + h
	if end > len(m.flat) {
		end = len(m.flat)
	}
	return start, end
}

func scanDir(path, root string, depth int) *Node {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	rel, _ := filepath.Rel(root, path)
	if rel == "." {
		rel = ""
	}

	node := &Node{
		Name:    info.Name(),
		Path:    path,
		RelPath: rel,
		IsDir:   info.IsDir(),
		Depth:   depth,
	}

	if !info.IsDir() {
		return node
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return node
	}

	// Dirs first, then files, both sorted alphabetically.
	var dirs, files []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	for _, e := range append(dirs, files...) {
		child := scanDir(filepath.Join(path, e.Name()), root, depth+1)
		if child != nil {
			node.Children = append(node.Children, child)
		}
	}

	return node
}
