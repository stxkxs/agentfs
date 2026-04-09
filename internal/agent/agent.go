package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Status represents an agent's current state.
type Status int

const (
	Unknown Status = iota
	Running
	Idle
	Error
	Done
)

func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Idle:
		return "idle"
	case Error:
		return "error"
	case Done:
		return "done"
	default:
		return "unknown"
	}
}

var (
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	styleIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleUnknown = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleName    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	styleDetail  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleSep     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// Agent represents a detected agent in the workspace.
type Agent struct {
	Name     string
	Dir      string
	Status   Status
	Task     string
	Step     interface{} // could be int or string
	Model    string
	LastSeen time.Time
	HasLogs  bool
	HasMem   bool
}

// stateFile is the shape we try to parse from state.json / status.json.
type stateFile struct {
	Status string      `json:"status"`
	State  string      `json:"state"`
	Task   string      `json:"task"`
	Step   interface{} `json:"step"`
	Model  string      `json:"model"`
	Agent  string      `json:"agent"`
	Name   string      `json:"name"`
	Error  string      `json:"error"`
}

// Scan looks for agent directories under root and returns detected agents.
func Scan(root string) []Agent {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var agents []Agent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if a, ok := detect(e.Name(), dir); ok {
			agents = append(agents, a)
		}
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents
}

// detect checks if a directory looks like an agent workspace.
func detect(name, dir string) (Agent, bool) {
	a := Agent{
		Name:   name,
		Dir:    dir,
		Status: Unknown,
	}

	// Check for signal files/dirs.
	hasState := false

	for _, sf := range []string{"state.json", "status.json", "agent.json"} {
		path := filepath.Join(dir, sf)
		if data, err := os.ReadFile(path); err == nil {
			hasState = true
			parseState(&a, data)
			if info, err := os.Stat(path); err == nil {
				a.LastSeen = info.ModTime()
			}
			break
		}
	}

	// Check for conventional subdirs.
	if isDir(filepath.Join(dir, "logs")) || isDir(filepath.Join(dir, "log")) {
		a.HasLogs = true
	}
	if isDir(filepath.Join(dir, "memory")) || isDir(filepath.Join(dir, "mem")) {
		a.HasMem = true
	}
	if isDir(filepath.Join(dir, "artifacts")) || isDir(filepath.Join(dir, "output")) || isDir(filepath.Join(dir, "tools")) {
		hasState = true // enough signal
	}

	if !hasState && !a.HasLogs && !a.HasMem {
		return a, false
	}

	return a, true
}

func parseState(a *Agent, data []byte) {
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return
	}

	// Determine status from status or state field.
	raw := strings.ToLower(sf.Status)
	if raw == "" {
		raw = strings.ToLower(sf.State)
	}

	switch {
	case contains(raw, "run", "active", "working", "busy", "processing"):
		a.Status = Running
	case contains(raw, "idle", "wait", "sleep", "paused", "pending"):
		a.Status = Idle
	case contains(raw, "error", "fail", "crash"):
		a.Status = Error
	case contains(raw, "done", "complete", "finish", "success"):
		a.Status = Done
	}

	if sf.Task != "" {
		a.Task = sf.Task
	}
	if sf.Step != nil {
		a.Step = sf.Step
	}
	if sf.Model != "" {
		a.Model = sf.Model
	}
	if sf.Error != "" && a.Status != Error {
		a.Status = Error
	}
}

func contains(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// RenderBar renders a compact status bar showing all detected agents.
func RenderBar(agents []Agent) string {
	if len(agents) == 0 {
		return styleUnknown.Render("  no agents detected")
	}

	parts := make([]string, len(agents))
	for i, a := range agents {
		parts[i] = renderAgent(a)
	}

	sep := styleSep.Render("  │  ")
	return "  " + strings.Join(parts, sep)
}

func renderAgent(a Agent) string {
	name := styleName.Render(a.Name)

	var statusStr string
	switch a.Status {
	case Running:
		statusStr = styleRunning.Render("● " + a.Status.String())
	case Idle:
		statusStr = styleIdle.Render("○ " + a.Status.String())
	case Error:
		statusStr = styleError.Render("✕ " + a.Status.String())
	case Done:
		statusStr = styleDone.Render("✓ " + a.Status.String())
	default:
		statusStr = styleUnknown.Render("? " + a.Status.String())
	}

	detail := ""
	if a.Task != "" {
		d := a.Task
		if a.Step != nil {
			d += fmt.Sprintf(" step:%v", a.Step)
		}
		detail = " " + styleDetail.Render("("+d+")")
	}

	return name + " " + statusStr + detail
}
