//go:build !windows

package main_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The exit-code contract says a reader closing the pipe is a decision that it
// has enough rather than a fault, so the command keeps the verdict it reached.
// Holding that costs a signal disposition in the process wrapper: SIGPIPE is
// delivered on a write to a closed standard output, and the Go runtime answers
// it by killing the process unless the signal is ignored. A process killed by a
// signal reports no exit status at all, so a caller reading the status sees
// neither the verdict nor a code it can act on.
//
// These run the built binary with a real pipe on its standard output, which is
// the only arrangement the disposition applies to — an in-process writer that
// returns EPIPE exercises the rule that reads the error, not the rule that lets
// the write return one.

// A one-shot result larger than the pipe buffer cannot be written before the
// reader goes away, so the write fails rather than completing into a buffer
// nobody drains.
func TestAClosedPipeLeavesAOneShotResultWithItsOwnStatus(t *testing.T) {
	t.Parallel()
	bin := buildAgentfs(t)
	ws := paddedWorkspace(t, 400)

	code := runIntoAClosedPipe(t, bin, "scan", "--format", "json", ws)
	if code != 0 {
		t.Errorf("scan --format json into a closed pipe exited %d, want 0", code)
	}
}

// The stream writes for as long as it is watching, so a reader that stops
// reading is met on the next record rather than at a single write.
func TestAClosedPipeLeavesTheStreamWithItsOwnStatus(t *testing.T) {
	t.Parallel()
	bin := buildAgentfs(t)
	ws := paddedWorkspace(t, 4)

	code := runIntoAClosedPipe(t, bin, "watch", "--format", "ndjson", ws)
	if code != 0 {
		t.Errorf("watch --format ndjson into a closed pipe exited %d, want 0", code)
	}
}

// runIntoAClosedPipe runs the binary with a pipe on standard output, reads one
// record from it, closes the read end, and returns the status the process
// exited with. A process killed by a signal returns -1, which is the failure
// these tests exist to catch.
func runIntoAClosedPipe(t *testing.T, bin string, args ...string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	// The process is watching a workspace in the stream case, so it is stopped
	// by the deadline rather than waited on indefinitely.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	// The parent's copy of the write end would hold the pipe open against the
	// child, so the child's write could never fail.
	_ = w.Close()

	if _, err := io.CopyN(io.Discard, r, 1); err != nil {
		t.Fatalf("read the first byte of %v: %v", args, err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close the read end: %v", err)
	}

	// The stream writes only when the workspace changes, so it is given
	// something to report until it meets the closed pipe.
	done := make(chan struct{})
	defer close(done)
	go churn(cmd.Args[len(cmd.Args)-1], done)

	_ = cmd.Wait()
	if cmd.ProcessState == nil {
		t.Fatalf("%v left no process state", args)
	}
	if code := cmd.ProcessState.ExitCode(); code < 0 {
		t.Fatalf("%v was killed by a signal (%v) rather than exiting, so a caller reads no status",
			args, cmd.ProcessState)
	}
	return cmd.ProcessState.ExitCode()
}

// churn rewrites a state document until it is told to stop, so a watching
// process has changes to report.
func churn(ws string, done <-chan struct{}) {
	for i := 0; ; i++ {
		select {
		case <-done:
			return
		case <-time.After(50 * time.Millisecond):
			_ = os.WriteFile(filepath.Join(ws, "agent-1", "state.json"),
				[]byte(stateDocument(1, i)), 0o600)
		}
	}
}

// paddedWorkspace writes a workspace of the given size, which is how a one-shot
// result is made larger than the buffer a pipe holds.
func paddedWorkspace(t *testing.T, agents int) string {
	t.Helper()
	ws := t.TempDir()
	for i := 1; i <= agents; i++ {
		dir := filepath.Join(ws, fmt.Sprintf("agent-%d", i))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"),
			[]byte(stateDocument(i, 1)), 0o600); err != nil {
			t.Fatalf("write state: %v", err)
		}
	}
	return ws
}

// stateDocument returns a conformant document for the numbered agent. The task
// is long enough that a workspace of a few hundred agents exceeds the pipe
// buffer, which is what makes the one-shot write fail rather than complete.
func stateDocument(agent, step int) string {
	at := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"schema":"agentfs/v1","status":"running","agent":"agent-%d",`+
		`"task":"Retrieve and rank sources for the question under consideration",`+
		`"step":%d,"steps_total":1024,"model":"claude-opus-5","run_id":"run-%03d",`+
		`"heartbeat_seconds":300,"started_at":%q,"updated_at":%q}`,
		agent, step%1024+1, agent, at, at)
}
