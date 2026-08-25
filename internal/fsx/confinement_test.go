//go:build !windows

package fsx_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stxkxs/agentfs/internal/fsx"
)

// Confinement is a property of the real filesystem, so this test uses one. It
// is the security control the workspace pitch depends on: agentfs is pointed at
// directories the operator does not control the contents of.
func TestSymlinkEscapeIsRefused(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	outside := filepath.Join(tmp, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(tmp, "ws")
	if err := os.Mkdir(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.txt", filepath.Join(ws, "climb.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "absolute.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	// os.DirFS resolves both escapes, which is why it is not the seam.
	for _, name := range []string{"climb.txt", "absolute.txt"} {
		if _, err := os.ReadFile(filepath.Join(ws, name)); err != nil {
			t.Fatalf("fixture symlink %q does not resolve: %v", name, err)
		}
	}

	root, err := fsx.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	for _, name := range []string{"climb.txt", "absolute.txt"} {
		if b, err := root.FS().ReadFile(name); err == nil {
			t.Errorf("confined root read %q and returned %q", name, b)
		}
	}
	if b, err := root.FS().ReadFile("inside.txt"); err != nil || string(b) != "ok" {
		t.Errorf("confined root refused a path inside it: %q %v", b, err)
	}
}

func TestReopenRecoversAVanishedRoot(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	if err := os.Mkdir(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	root, err := fsx.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	if err := os.Remove(ws); err != nil {
		t.Fatal(err)
	}
	if err := root.Reopen(); err == nil {
		t.Fatal("Reopen succeeded against a removed root")
	}

	if err := os.Mkdir(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := root.Reopen(); err != nil {
		t.Fatalf("Reopen failed after the root returned: %v", err)
	}
	if err := root.Health(); err != nil {
		t.Fatalf("Health after recovery: %v", err)
	}
}
