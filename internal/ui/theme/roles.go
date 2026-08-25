package theme

import "strconv"

// StatusRole is the agent state a row reports. It mirrors the document
// contract's status vocabulary, which internal/agentstate decodes, without
// importing it: the shipped palette stays a leaf of the import graph, and
// TestStatusRolesMirrorTheContractVocabulary holds the copy faithful.
type StatusRole int

// The status roles, in the order agentstate.Vocabulary returns them, with the
// state a document cannot declare last.
const (
	// RoleRunning marks an agent holding work and progressing.
	RoleRunning StatusRole = iota
	// RoleIdle marks an agent holding no work.
	RoleIdle
	// RoleBlocked marks an agent waiting on an external input.
	RoleBlocked
	// RoleError marks an agent that stopped because it could not continue.
	RoleError
	// RoleDone marks an agent that completed its work.
	RoleDone
	// RoleUnknown marks a workspace whose status did not decode.
	RoleUnknown
)

// statusRoleCount is untyped so that it is not a member of the StatusRole
// vocabulary: a switch over the enum stays exhaustive without a sentinel case.
const statusRoleCount = int(RoleUnknown) + 1

var statusRoleNames = [statusRoleCount]string{"running", "idle", "blocked", "error", "done", "unknown"}

// String returns the role's lowercase name.
func (r StatusRole) String() string {
	if r < 0 || int(r) >= statusRoleCount {
		return "status(" + strconv.Itoa(int(r)) + ")"
	}
	return statusRoleNames[r]
}

// SeverityRole is the weight of a diagnostic.
type SeverityRole int

// The severity roles, ordered by increasing seriousness.
const (
	// RoleInfo marks a finding that does not affect usability.
	RoleInfo SeverityRole = iota
	// RoleWarning marks a finding worth acting on.
	RoleWarning
	// RoleSevere marks a finding that makes a document unusable.
	RoleSevere
)

// severityRoleCount is untyped, for the reason given at [statusRoleCount].
const severityRoleCount = int(RoleSevere) + 1

var severityRoleNames = [severityRoleCount]string{"info", "warning", "severe"}

// String returns the role's lowercase name.
func (r SeverityRole) String() string {
	if r < 0 || int(r) >= severityRoleCount {
		return "severity(" + strconv.Itoa(int(r)) + ")"
	}
	return severityRoleNames[r]
}

// JSONRole is the lexical class of a token in a rendered JSON document.
type JSONRole int

// The JSON token roles.
const (
	// RoleKey marks an object member name.
	RoleKey JSONRole = iota
	// RoleString marks a string value.
	RoleString
	// RoleNumber marks a number value.
	RoleNumber
	// RoleBool marks true or false.
	RoleBool
	// RoleNull marks null.
	RoleNull
	// RolePunct marks structural punctuation: braces, brackets, commas, colons.
	RolePunct
)

// jsonRoleCount is untyped, for the reason given at [statusRoleCount].
const jsonRoleCount = int(RolePunct) + 1

var jsonRoleNames = [jsonRoleCount]string{"key", "string", "number", "bool", "null", "punct"}

// String returns the role's lowercase name.
func (r JSONRole) String() string {
	if r < 0 || int(r) >= jsonRoleCount {
		return "json(" + strconv.Itoa(int(r)) + ")"
	}
	return jsonRoleNames[r]
}

// LogRole is the level a log line reports itself at.
type LogRole int

// The log level roles, ordered by increasing seriousness.
const (
	// RoleTrace marks the most detailed level.
	RoleTrace LogRole = iota
	// RoleDebug marks diagnostic detail.
	RoleDebug
	// RoleInfoLevel marks ordinary progress.
	RoleInfoLevel
	// RoleWarn marks a condition worth attention.
	RoleWarn
	// RoleErrorLevel marks a failure.
	RoleErrorLevel
)

// logRoleCount is untyped, for the reason given at [statusRoleCount].
const logRoleCount = int(RoleErrorLevel) + 1

var logRoleNames = [logRoleCount]string{"trace", "debug", "info", "warn", "error"}

// String returns the role's lowercase name.
func (r LogRole) String() string {
	if r < 0 || int(r) >= logRoleCount {
		return "log(" + strconv.Itoa(int(r)) + ")"
	}
	return logRoleNames[r]
}

// role indexes one entry of a palette's style table. The status, severity,
// JSON and log blocks are contiguous and ordered to match their public enum,
// so a lookup is an addition rather than a switch that can fall through.
type role int

const (
	roleTitle role = iota
	roleDim
	roleBody
	roleAccent
	roleDirectory
	roleCursor
	roleRecent
	roleBorderBlurred
	roleBorderFocused
	roleMatch
	roleMatchCurrent

	roleStatusRunning
	roleStatusIdle
	roleStatusBlocked
	roleStatusError
	roleStatusDone
	roleStatusUnknown

	roleSeverityInfo
	roleSeverityWarning
	roleSeveritySevere

	roleJSONKey
	roleJSONString
	roleJSONNumber
	roleJSONBool
	roleJSONNull
	roleJSONPunct

	roleLogTrace
	roleLogDebug
	roleLogInfo
	roleLogWarn
	roleLogError

	roleCount
)
