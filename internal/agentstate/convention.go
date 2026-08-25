// Package agentstate is the agentfs workspace contract: the document an agent
// writes to declare what it is doing, and the decoder that reads it.
//
// The contract is the product. A workspace convention that exists only as prose
// and as filename literals scattered through a reader is not a contract — it
// cannot be validated, versioned, or conformed to. Everything an integrator
// needs is declared here and published as JSON Schema, and every reader in
// agentfs goes through this package rather than matching filenames of its own.
package agentstate

import "slices"

// SchemaVersion is the contract version this build implements. A document
// declaring a different version is refused rather than guessed at.
const SchemaVersion = "agentfs/v1"

// StateFile is the document name an agent writes.
const StateFile = "state.json"

// LegacyStateFiles are document names accepted under the compatibility profile.
// They are read indefinitely: removing them would make agentfs blind to
// workspaces that already exist, which is a worse outcome than an info
// diagnostic naming the canonical name.
var LegacyStateFiles = []string{"status.json", "agent.json"}

// StateFiles returns every accepted state document name, canonical first. The
// order is the precedence a scanner applies.
func StateFiles() []string {
	return append([]string{StateFile}, LegacyStateFiles...)
}

// IsStateFile reports whether base names a state document.
func IsStateFile(base string) bool {
	return base == StateFile || slices.Contains(LegacyStateFiles, base)
}

// IsLegacyStateFile reports whether base names a compatibility state document.
func IsLegacyStateFile(base string) bool {
	return slices.Contains(LegacyStateFiles, base)
}

// Directory roles an agent workspace declares by convention. A directory
// carrying one of these names is evidence that its parent is an agent
// workspace, which is how agentfs discovers an agent that writes no state
// document.
const (
	DirLogs      = "logs"
	DirMemory    = "memory"
	DirArtifacts = "artifacts"
	DirTools     = "tools"
	DirRuns      = "runs"
)

// SignalDirs are the directory names whose presence marks a directory as an
// agent workspace.
var SignalDirs = []string{DirLogs, DirMemory, DirArtifacts, DirTools, DirRuns}

// RunDirs are the directory names whose children are runs.
var RunDirs = []string{DirRuns, "history"}

// IsSignalDir reports whether base is a conventional agent subdirectory.
func IsSignalDir(base string) bool { return slices.Contains(SignalDirs, base) }

// IsRunDir reports whether base is a directory whose children are runs.
func IsRunDir(base string) bool { return slices.Contains(RunDirs, base) }
