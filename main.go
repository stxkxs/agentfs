package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/stxkxs/agentfs/internal/app"
	"github.com/stxkxs/agentfs/internal/watcher"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: agentfs <directory>\n")
		os.Exit(1)
	}

	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "error: %s is not a directory\n", root)
		os.Exit(1)
	}

	w, err := watcher.New(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error starting watcher: %v\n", err)
		os.Exit(1)
	}
	defer w.Close()

	model := app.New(root, w)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
