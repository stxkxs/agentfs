package agentstate

import (
	"cmp"
	"slices"
	"strings"
)

// Status is the state an agent declares itself to be in. The vocabulary is
// closed: a value outside it is a diagnostic, never a guess.
type Status int

// The contract vocabulary.
const (
	// StatusUnknown is the zero value, carried by a document whose status did
	// not decode. It is not a wire value.
	StatusUnknown Status = iota
	// StatusRunning: the agent holds work and is progressing.
	StatusRunning
	// StatusIdle: the agent holds no work and is available.
	StatusIdle
	// StatusBlocked: the agent holds work it cannot progress without an
	// external input, such as an approval.
	StatusBlocked
	// StatusError: the agent stopped because it could not continue.
	StatusError
	// StatusDone: the agent completed its work.
	StatusDone
)

// wire is the canonical spelling of each status.
var wire = map[Status]string{
	StatusUnknown: "unknown",
	StatusRunning: "running",
	StatusIdle:    "idle",
	StatusBlocked: "blocked",
	StatusError:   "error",
	StatusDone:    "done",
}

// String returns the canonical wire spelling.
func (s Status) String() string {
	if v, ok := wire[s]; ok {
		return v
	}
	return "unknown"
}

// MarshalText encodes the status as its canonical spelling.
func (s Status) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Terminal reports whether no further transition is expected.
func (s Status) Terminal() bool { return s == StatusDone || s == StatusError }

// Vocabulary returns the canonical spellings a v1 document may declare, in
// contract order. The published schema's enum is generated from this, so the
// schema and the decoder cannot disagree about what is accepted.
func Vocabulary() []string {
	return []string{
		StatusRunning.String(),
		StatusIdle.String(),
		StatusBlocked.String(),
		StatusError.String(),
		StatusDone.String(),
	}
}

// canonical maps a v1 spelling to its status.
var canonical = map[string]Status{
	"running": StatusRunning,
	"idle":    StatusIdle,
	"blocked": StatusBlocked,
	"error":   StatusError,
	"done":    StatusDone,
}

// aliases maps spellings accepted under the compatibility profile. Matching is
// exact against this table after trimming and case folding — never by
// substring, which is what made "not running" read as running and "unfailed"
// read as an error.
var aliases = map[string]Status{
	"active":            StatusRunning,
	"working":           StatusRunning,
	"busy":              StatusRunning,
	"in_progress":       StatusRunning,
	"in-progress":       StatusRunning,
	"processing":        StatusRunning,
	"waiting":           StatusIdle,
	"pending":           StatusIdle,
	"paused":            StatusIdle,
	"sleeping":          StatusIdle,
	"queued":            StatusIdle,
	"needs_input":       StatusBlocked,
	"awaiting_input":    StatusBlocked,
	"awaiting_approval": StatusBlocked,
	"failed":            StatusError,
	"failure":           StatusError,
	"crashed":           StatusError,
	"fatal":             StatusError,
	"complete":          StatusDone,
	"completed":         StatusDone,
	"finished":          StatusDone,
	"succeeded":         StatusDone,
	"success":           StatusDone,
}

// ParseStatus resolves a wire spelling to a status. It matches the canonical
// vocabulary exactly, and under the compatibility profile the alias table as
// well. It reports false for anything else, including a value that merely
// contains a vocabulary word.
func ParseStatus(raw string, profile Profile) (Status, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if s, ok := canonical[key]; ok {
		return s, true
	}
	if profile == ProfileCompat {
		if s, ok := aliases[key]; ok {
			return s, true
		}
	}
	return StatusUnknown, false
}

// Aliases returns every compatibility spelling and the status it resolves to,
// sorted by spelling. The generated reference is rendered from this.
func Aliases() []struct {
	Spelling string
	Status   Status
} {
	out := make([]struct {
		Spelling string
		Status   Status
	}, 0, len(aliases))
	for k, v := range aliases {
		out = append(out, struct {
			Spelling string
			Status   Status
		}{k, v})
	}
	slices.SortFunc(out, func(a, b struct {
		Spelling string
		Status   Status
	}) int {
		return cmp.Compare(a.Spelling, b.Spelling)
	})
	return out
}
