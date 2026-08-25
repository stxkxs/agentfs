package agentstate_test

import (
	"slices"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// The order is the precedence a scanner applies, so a workspace holding both a
// canonical and a compatibility document is read from the canonical one.
func TestStateFilesNamesTheCanonicalDocumentFirst(t *testing.T) {
	t.Parallel()
	names := agentstate.StateFiles()
	if len(names) == 0 || names[0] != agentstate.StateFile {
		t.Fatalf("StateFiles = %v, want %q first", names, agentstate.StateFile)
	}
	for _, legacy := range agentstate.LegacyStateFiles {
		if !slices.Contains(names, legacy) {
			t.Errorf("StateFiles = %v, want it to carry the compatibility name %q", names, legacy)
		}
	}
	for _, name := range names {
		if !agentstate.IsStateFile(name) {
			t.Errorf("StateFiles offers %q, which IsStateFile refuses", name)
		}
	}
}

// Every scanner in the tree reads the table through this call, so a caller
// that sorts or truncates what it received must not reach the next one.
func TestStateFilesReturnsAFreshTable(t *testing.T) {
	t.Parallel()
	first := agentstate.StateFiles()
	first[0] = "clobbered"
	if second := agentstate.StateFiles(); second[0] != agentstate.StateFile {
		t.Errorf("a mutated result reached the next call: %v", second)
	}
}

// Discovery reads a directory entry by name. A name outside the table names no
// state document, so a workspace holding one is not read as an agent's
// declaration.
func TestOnlyDocumentNamesInTheTableAreStateDocuments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base          string
		state, legacy bool
	}{
		{"state.json", true, false},
		{"status.json", true, true},
		{"agent.json", true, true},
		{"state.json.tmp", false, false},
		{"State.json", false, false},
		{"notes.json", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		if got := agentstate.IsStateFile(tc.base); got != tc.state {
			t.Errorf("IsStateFile(%q) = %v, want %v", tc.base, got, tc.state)
		}
		if got := agentstate.IsLegacyStateFile(tc.base); got != tc.legacy {
			t.Errorf("IsLegacyStateFile(%q) = %v, want %v", tc.base, got, tc.legacy)
		}
	}
}

// A signal directory is how agentfs discovers an agent that writes no state
// document, so the set decides which directories are workspaces at all.
func TestSignalDirectoriesMarkAWorkspace(t *testing.T) {
	t.Parallel()
	for _, base := range agentstate.SignalDirs {
		if !agentstate.IsSignalDir(base) {
			t.Errorf("IsSignalDir(%q) = false for a name in the table", base)
		}
	}
	for _, base := range []string{"src", "logs.old", "Logs", ""} {
		if agentstate.IsSignalDir(base) {
			t.Errorf("IsSignalDir(%q) = true for a name outside the table", base)
		}
	}
}

// A run directory's children are runs; a directory that is a workspace signal
// without being one holds no runs to enumerate.
func TestRunDirectoriesHoldRuns(t *testing.T) {
	t.Parallel()
	for _, base := range agentstate.RunDirs {
		if !agentstate.IsRunDir(base) {
			t.Errorf("IsRunDir(%q) = false for a name in the table", base)
		}
	}
	for _, base := range []string{agentstate.DirLogs, agentstate.DirMemory, "run", ""} {
		if agentstate.IsRunDir(base) {
			t.Errorf("IsRunDir(%q) = true for a name outside the table", base)
		}
	}
}
