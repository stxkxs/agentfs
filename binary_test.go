package main_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// buildAgentfs compiles the binary under test. A test whose subject is the
// program rather than a package — the signal disposition it installs, the
// transcript it prints — cannot be reached by calling into one.
//
// It carries no build tag, because every test file that needs the binary would
// otherwise have to carry the same one to keep the package compiling under a
// platform where the callers do not build.
func buildAgentfs(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the go tool is not available")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	name := "agentfs"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agentfs: %v\n%s", err, out)
	}
	return bin
}
