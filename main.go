// Command agentfs watches AI agent workspaces on disk.
//
// The binary is a thin wrapper: it builds the process environment, runs the
// command, and exits with the code it returns. Everything worth testing lives
// below [cli.Run], which takes its environment as a value and never calls
// os.Exit.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"charm.land/lipgloss/v2"

	"github.com/stxkxs/agentfs/internal/cli"
)

func main() { os.Exit(run()) }

// run holds everything that must unwind before the process exits. os.Exit skips
// deferred functions, so the signal handler is released here rather than in
// main, where the exit would leave it registered.
func run() int {
	// A write to a pipe whose reader is gone raises SIGPIPE, and the Go runtime
	// answers that by killing the process when the descriptor is standard
	// output or standard error. The exit-code contract is the opposite: a
	// reader that closes the pipe has decided it has enough, so the command
	// keeps the verdict it reached and report.IsBrokenPipe recognizes the
	// failed write. That rule only runs if the write returns instead of the
	// process dying, which is what ignoring the signal here buys — for a
	// descriptor under a signal disposition of ignore, the kernel fails the
	// write with EPIPE.
	signal.Ignore(syscall.SIGPIPE)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return int(cli.Run(ctx, cli.Env{
		Args:           os.Args,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Getenv:         os.Getenv,
		Interactive:    isTerminal(os.Stdout),
		DarkBackground: darkBackground(),
	}))
}

// isTerminal reports whether w is a terminal, which decides whether a command
// with a terminal form uses it.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// darkBackground reports the terminal's background so the palette can be
// chosen. A terminal that answers nothing is treated as dark, which is the
// common case and the one whose palette degrades more gracefully on the other.
func darkBackground() bool {
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}
