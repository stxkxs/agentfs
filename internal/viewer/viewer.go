package viewer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleLineNo  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Width(4).Align(lipgloss.Right)
	styleKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleString  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleNumber  = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	styleBool    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleNull    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBracket = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	// Log level styles
	styleLogError = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleLogWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	styleLogInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleLogDebug = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Search
	styleMatch    = lipgloss.NewStyle().Background(lipgloss.Color("11")).Foreground(lipgloss.Color("0"))
	styleSearchBg = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

// Model is the file content viewer.
type Model struct {
	path     string
	relPath  string
	lines    []string // display lines (may be colorized)
	rawLines []string // uncolorized lines for search
	raw      []byte
	offset   int
	width    int
	height   int
	follow   bool // tail mode for logs
	fileType fileType
	err      error

	// Search state
	searching   bool
	searchInput string
	searchQuery string
	matches     []int // line indices that match
	matchIdx    int   // current match cursor
}

type fileType int

const (
	ftPlain fileType = iota
	ftJSON
	ftYAML
	ftLog
)

// New creates an empty viewer.
func New() Model {
	return Model{}
}

// LoadFile loads a file into the viewer.
func (m *Model) LoadFile(path, relPath string) {
	m.path = path
	m.relPath = relPath
	m.offset = 0
	m.err = nil
	m.fileType = detectType(path)
	m.clearSearch()

	data, err := os.ReadFile(path)
	if err != nil {
		m.err = err
		m.lines = nil
		m.rawLines = nil
		m.raw = nil
		return
	}

	m.raw = data
	m.rawLines = strings.Split(string(data), "\n")

	switch m.fileType {
	case ftJSON:
		m.lines = highlightJSON(data)
		// rawLines for JSON should be the pretty-printed version for search
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			m.rawLines = strings.Split(pretty.String(), "\n")
		}
	case ftLog:
		m.lines = highlightLog(m.rawLines)
	default:
		m.lines = make([]string, len(m.rawLines))
		copy(m.lines, m.rawLines)
	}

	if m.fileType == ftLog {
		m.follow = true
		m.scrollToEnd()
	} else {
		m.follow = false
	}
}

// Reload re-reads the current file.
func (m *Model) Reload() {
	if m.path == "" {
		return
	}
	wasFollow := m.follow
	prevQuery := m.searchQuery
	m.LoadFile(m.path, m.relPath)
	m.follow = wasFollow
	if prevQuery != "" {
		m.searchQuery = prevQuery
		m.runSearch()
	}
	if m.follow {
		m.scrollToEnd()
	}
}

// Path returns the currently loaded file path.
func (m Model) Path() string {
	return m.path
}

// RelPath returns the relative path of the current file.
func (m Model) RelPath() string {
	return m.relPath
}

// Searching returns whether the viewer is in search input mode.
func (m Model) Searching() bool {
	return m.searching
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Search input mode
		if m.searching {
			switch msg.String() {
			case "enter":
				m.searching = false
				m.searchQuery = m.searchInput
				m.runSearch()
				if len(m.matches) > 0 {
					m.matchIdx = 0
					m.jumpToMatch()
				}
			case "esc":
				m.searching = false
				m.searchInput = ""
			case "backspace":
				if len(m.searchInput) > 0 {
					m.searchInput = m.searchInput[:len(m.searchInput)-1]
				}
			default:
				if len(msg.String()) == 1 || msg.String() == " " {
					m.searchInput += msg.String()
				}
			}
			return m, nil
		}

		// Normal mode
		switch msg.String() {
		case "up", "k":
			if m.offset > 0 {
				m.offset--
				m.follow = false
			}
		case "down", "j":
			if m.offset < m.maxOffset() {
				m.offset++
			}
		case "g":
			m.offset = 0
			m.follow = false
		case "G":
			m.scrollToEnd()
			if m.fileType == ftLog {
				m.follow = true
			}
		case "pgup", "ctrl+u":
			m.offset -= m.viewHeight() / 2
			if m.offset < 0 {
				m.offset = 0
			}
			m.follow = false
		case "pgdown", "ctrl+d":
			m.offset += m.viewHeight() / 2
			if m.offset > m.maxOffset() {
				m.offset = m.maxOffset()
			}
		case "/":
			m.searching = true
			m.searchInput = ""
		case "n":
			m.nextMatch()
		case "N":
			m.prevMatch()
		case "esc":
			m.clearSearch()
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.path == "" {
		return styleHeader.Render("  select a file to view")
	}

	var b strings.Builder

	// Header line
	header := styleHeader.Render(fmt.Sprintf("  %s", m.relPath))
	if m.follow {
		header += styleHeader.Render(" (following)")
	}
	if m.searchQuery != "" && !m.searching {
		header += styleSearchBg.Render(fmt.Sprintf(" [%d/%d \"%s\"]", m.matchIdx+1, len(m.matches), m.searchQuery))
	}
	b.WriteString(header + "\n")

	// Search input bar
	if m.searching {
		b.WriteString(styleSearchBg.Render("  /") + m.searchInput + styleSearchBg.Render("█") + "\n")
	}

	if m.err != nil {
		b.WriteString(styleErr.Render(fmt.Sprintf("  error: %v", m.err)))
		return b.String()
	}

	vh := m.viewHeight()
	if m.searching {
		vh-- // search bar takes a line
	}
	end := m.offset + vh
	if end > len(m.lines) {
		end = len(m.lines)
	}

	// Build a set of match lines for highlighting
	matchSet := make(map[int]bool)
	for _, idx := range m.matches {
		matchSet[idx] = true
	}
	currentMatch := -1
	if len(m.matches) > 0 && m.matchIdx >= 0 && m.matchIdx < len(m.matches) {
		currentMatch = m.matches[m.matchIdx]
	}

	for i := m.offset; i < end; i++ {
		lineNo := styleLineNo.Render(fmt.Sprintf("%d", i+1))
		line := m.lines[i]

		// Highlight search matches in the line
		if m.searchQuery != "" && matchSet[i] {
			if i == currentMatch {
				line = highlightSearchInLine(m.rawLines[i], m.searchQuery, true)
			} else {
				line = highlightSearchInLine(m.rawLines[i], m.searchQuery, false)
			}
		}

		b.WriteString(fmt.Sprintf("%s  %s", lineNo, line))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// SetSize sets the viewport dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m Model) viewHeight() int {
	h := m.height - 2 // header
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) maxOffset() int {
	max := len(m.lines) - m.viewHeight()
	if max < 0 {
		return 0
	}
	return max
}

func (m *Model) scrollToEnd() {
	m.offset = m.maxOffset()
}

// Search operations

func (m *Model) clearSearch() {
	m.searching = false
	m.searchInput = ""
	m.searchQuery = ""
	m.matches = nil
	m.matchIdx = 0
}

func (m *Model) runSearch() {
	m.matches = nil
	if m.searchQuery == "" {
		return
	}
	q := strings.ToLower(m.searchQuery)
	for i, line := range m.rawLines {
		if strings.Contains(strings.ToLower(line), q) {
			m.matches = append(m.matches, i)
		}
	}
}

func (m *Model) nextMatch() {
	if len(m.matches) == 0 {
		return
	}
	m.matchIdx = (m.matchIdx + 1) % len(m.matches)
	m.jumpToMatch()
}

func (m *Model) prevMatch() {
	if len(m.matches) == 0 {
		return
	}
	m.matchIdx--
	if m.matchIdx < 0 {
		m.matchIdx = len(m.matches) - 1
	}
	m.jumpToMatch()
}

func (m *Model) jumpToMatch() {
	if m.matchIdx < 0 || m.matchIdx >= len(m.matches) {
		return
	}
	line := m.matches[m.matchIdx]
	vh := m.viewHeight()
	// Center the match in the viewport.
	m.offset = line - vh/2
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > m.maxOffset() {
		m.offset = m.maxOffset()
	}
	m.follow = false
}

func highlightSearchInLine(line, query string, isCurrent bool) string {
	lower := strings.ToLower(line)
	q := strings.ToLower(query)
	idx := strings.Index(lower, q)
	if idx < 0 {
		return line
	}

	before := line[:idx]
	match := line[idx : idx+len(query)]
	after := line[idx+len(query):]

	style := styleMatch
	if !isCurrent {
		style = lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
	}

	return before + style.Render(match) + after
}

// Log rendering

func highlightLog(rawLines []string) []string {
	result := make([]string, len(rawLines))
	for i, line := range rawLines {
		result[i] = colorizeLogLine(line)
	}
	return result
}

func colorizeLogLine(line string) string {
	upper := strings.ToUpper(line)

	switch {
	case strings.Contains(upper, "ERROR") ||
		strings.Contains(upper, "FATAL") ||
		strings.Contains(upper, "PANIC"):
		return styleLogError.Render(line)

	case strings.Contains(upper, "WARN"):
		return styleLogWarn.Render(line)

	case strings.Contains(upper, "DEBUG") ||
		strings.Contains(upper, "TRACE"):
		return styleLogDebug.Render(line)

	default:
		return styleLogInfo.Render(line)
	}
}

// File type detection

func detectType(path string) fileType {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return ftJSON
	case ".yaml", ".yml":
		return ftYAML
	case ".log":
		return ftLog
	default:
		return ftPlain
	}
}

// JSON rendering

func highlightJSON(data []byte) []string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return strings.Split(string(data), "\n")
	}

	raw := pretty.String()
	lines := strings.Split(raw, "\n")
	result := make([]string, len(lines))

	for i, line := range lines {
		result[i] = colorizeJSONLine(line)
	}

	return result
}

func colorizeJSONLine(line string) string {
	trimmed := strings.TrimSpace(line)
	indent := line[:len(line)-len(trimmed)]

	if trimmed == "" {
		return line
	}
	if trimmed == "{" || trimmed == "}" || trimmed == "}," ||
		trimmed == "[" || trimmed == "]" || trimmed == "]," {
		return indent + styleBracket.Render(trimmed)
	}

	if strings.HasPrefix(trimmed, "\"") {
		colonIdx := strings.Index(trimmed, "\": ")
		if colonIdx > 0 {
			key := trimmed[:colonIdx+1]
			rest := trimmed[colonIdx+2:]
			return indent + styleKey.Render(key) + ":" + colorizeValue(rest)
		}
		colonIdx = strings.Index(trimmed, "\":")
		if colonIdx > 0 {
			key := trimmed[:colonIdx+1]
			rest := trimmed[colonIdx+2:]
			return indent + styleKey.Render(key) + ":" + colorizeValue(rest)
		}
	}

	return indent + colorizeValue(trimmed)
}

func colorizeValue(s string) string {
	s = strings.TrimSpace(s)
	bare := strings.TrimSuffix(s, ",")
	comma := ""
	if len(bare) < len(s) {
		comma = ","
	}

	switch {
	case strings.HasPrefix(bare, "\""):
		return " " + styleString.Render(bare) + comma
	case bare == "true" || bare == "false":
		return " " + styleBool.Render(bare) + comma
	case bare == "null":
		return " " + styleNull.Render(bare) + comma
	case bare == "{" || bare == "[":
		return " " + styleBracket.Render(bare)
	case bare == "}" || bare == "]":
		return styleBracket.Render(bare) + comma
	default:
		return " " + styleNumber.Render(bare) + comma
	}
}
