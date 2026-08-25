//go:build !windows

package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// The demo is what a reader runs first, and the how-to guides tell integrators
// to write documents exactly the way it does. A demo that emits something the
// contract refuses would be the tool contradicting its own documentation, so
// what it writes is checked rather than assumed.
func TestDemoScriptEmitsConformantDocuments(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	dir := filepath.Join(t.TempDir(), "workspace")

	// The script loops until it is stopped, so it is given a deadline rather
	// than waited on.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "scripts/demo-agents.sh", dir)
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the demo: %v", err)
	}
	waitForDocuments(t, dir, 5)
	cancel()
	_ = cmd.Wait()

	documents := collectDocuments(t, dir)
	if len(documents) < 5 {
		t.Fatalf("the demo wrote %d state documents, want one per agent", len(documents))
	}

	now := time.Now()
	for _, path := range documents {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		rel, _ := filepath.Rel(dir, path)
		_, ds := agentstate.Decode(filepath.ToSlash(rel), body, agentstate.Options{Now: now})
		for _, d := range ds {
			if d.Severity == diag.Error {
				t.Errorf("the demo wrote a document the contract refuses: %s", d)
			}
		}
	}
}

// A rename is atomic, so a temporary file left in a workspace is a write the
// script did not finish — the failure mode the how-to tells integrators to
// avoid.
func TestDemoScriptLeavesNoPartialWrites(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	dir := filepath.Join(t.TempDir(), "workspace")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "scripts/demo-agents.sh", dir)
	// Cancellation sends the signal the script installs a trap for. The
	// default is SIGKILL, which no process can respond to, so a workspace
	// asked to be clean after one is asked for something no program can
	// promise. WaitDelay still escalates to a kill if the trap does not finish.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the demo: %v", err)
	}
	waitForDocuments(t, dir, 5)

	// A signal that arrives between the write and the rename leaves a
	// temporary file, and that window is too small to land on deliberately.
	// Planting one puts the workspace in the state an interruption produces,
	// so the trap answers the same question without a race deciding whether it
	// was asked.
	planted := filepath.Join(dir, "agent-researcher", ".agentfs-demo.interrupted")
	if err := os.WriteFile(planted, []byte("{"), 0o600); err != nil {
		t.Fatalf("plant an interrupted write: %v", err)
	}

	cancel()
	_ = cmd.Wait()

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable subtree holds nothing to check
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			t.Errorf("the demo left the temporary file %s behind", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}

// waitForDocuments polls until the script has written at least want documents,
// which is what makes the assertions run against a filled workspace rather than
// an empty one.
func waitForDocuments(t *testing.T, dir string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(collectDocuments(t, dir)) >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the demo wrote fewer than %d documents within the deadline", want)
}

// collectDocuments returns every state document under dir.
func collectDocuments(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a directory being written is not a failure to walk
		}
		if !d.IsDir() && agentstate.IsStateFile(d.Name()) {
			out = append(out, path)
		}
		return nil
	})
	return out
}
