package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Run represents a detected agent run.
type Run struct {
	ID        string
	Agent     string
	Dir       string
	StartTime time.Time
	Files     []string
	Status    Status
}

// Patterns for run directory names.
var runPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^run[-_](\d+)$`),                                     // run-001, run_42
	regexp.MustCompile(`^(\d{4}[-_]\d{2}[-_]\d{2}[-_T]\d{2}[-_:]\d{2}).*$`), // 2026-04-08T13:00...
	regexp.MustCompile(`^(\d{8}[-_]\d{6}).*$`),                               // 20260408_130000
	regexp.MustCompile(`^(\d+)$`),                                             // 1, 2, 3
	regexp.MustCompile(`^run$`),                                               // just "run"
	regexp.MustCompile(`^(attempt|try|iter|iteration)[-_]?\d*$`),              // attempt-1, try_3
}

// ScanRuns finds runs across all agent directories.
func ScanRuns(root string) []Run {
	agents := Scan(root)
	var runs []Run

	for _, a := range agents {
		agentRuns := scanAgentRuns(a.Name, a.Dir)
		runs = append(runs, agentRuns...)
	}

	// Sort newest first.
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartTime.After(runs[j].StartTime)
	})

	return runs
}

// ScanAgentRuns finds runs for a specific agent directory.
func ScanAgentRuns(agentName, agentDir string) []Run {
	return scanAgentRuns(agentName, agentDir)
}

func scanAgentRuns(agentName, agentDir string) []Run {
	var runs []Run

	// Check for a "runs/" or "history/" subdirectory.
	for _, subdir := range []string{"runs", "history", "output", "outputs"} {
		runsDir := filepath.Join(agentDir, subdir)
		entries, err := os.ReadDir(runsDir)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if isRunDir(e.Name()) {
				r := buildRun(agentName, e.Name(), filepath.Join(runsDir, e.Name()))
				runs = append(runs, r)
			}
		}
	}

	// Also check direct children of the agent dir.
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return runs
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip conventional dirs.
		switch name {
		case "logs", "log", "memory", "mem", "artifacts", "tools",
			"runs", "history", "output", "outputs":
			continue
		}
		if isRunDir(name) {
			r := buildRun(agentName, name, filepath.Join(agentDir, name))
			runs = append(runs, r)
		}
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartTime.After(runs[j].StartTime)
	})

	return runs
}

func isRunDir(name string) bool {
	for _, re := range runPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

func buildRun(agentName, id, dir string) Run {
	r := Run{
		ID:    id,
		Agent: agentName,
		Dir:   dir,
	}

	// Get modification time as proxy for start time.
	if info, err := os.Stat(dir); err == nil {
		r.StartTime = info.ModTime()
	}

	// List files in the run directory.
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			r.Files = append(r.Files, e.Name())
		}
	}

	// Try to detect run status from contents.
	r.Status = detectRunStatus(dir, r.Files)

	return r
}

func detectRunStatus(dir string, files []string) Status {
	for _, f := range files {
		lower := strings.ToLower(f)
		if lower == "state.json" || lower == "status.json" {
			a := Agent{}
			if data, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
				parseState(&a, data)
				return a.Status
			}
		}
		// Error file present = error run
		if lower == "error" || lower == "error.log" || lower == "error.json" {
			return Error
		}
	}
	// Has output/result = likely done
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "result") || strings.Contains(lower, "output") {
			return Done
		}
	}
	return Unknown
}

// RenderRunList renders a list of runs for display.
func RenderRunList(runs []Run, cursor int, height int) string {
	if len(runs) == 0 {
		return styleUnknown.Render("  no runs detected")
	}

	var b strings.Builder

	start := 0
	if cursor >= height {
		start = cursor - height + 1
	}
	end := start + height
	if end > len(runs) {
		end = len(runs)
	}

	for i := start; i < end; i++ {
		r := runs[i]

		var statusIcon string
		switch r.Status {
		case Running:
			statusIcon = styleRunning.Render("●")
		case Done:
			statusIcon = styleDone.Render("✓")
		case Error:
			statusIcon = styleError.Render("✕")
		case Idle:
			statusIcon = styleIdle.Render("○")
		default:
			statusIcon = styleUnknown.Render("·")
		}

		ts := styleIdle.Render(r.StartTime.Format("01/02 15:04"))
		name := styleName.Render(r.Agent)
		id := styleDetail.Render(r.ID)
		fileCount := styleIdle.Render(fmt.Sprintf("%d files", len(r.Files)))

		line := fmt.Sprintf("  %s %s %s/%s %s", statusIcon, ts, name, id, fileCount)
		if i == cursor {
			line = styleName.Render("│") + line[1:]
		}

		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}
