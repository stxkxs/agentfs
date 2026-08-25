package agentstate

import (
	"encoding/json"
	"time"
)

// Profile selects how strictly a document is read.
type Profile int

// The reading profiles.
const (
	// ProfileV1 applies to a document declaring [SchemaVersion]. The status
	// vocabulary is closed and every member is typed.
	ProfileV1 Profile = iota
	// ProfileCompat applies to a document declaring no schema member. The
	// alias table and the compatibility member names are accepted, each with
	// an info diagnostic naming the canonical form.
	ProfileCompat
)

// String returns the profile's name.
func (p Profile) String() string {
	switch p {
	case ProfileV1:
		return SchemaVersion
	case ProfileCompat:
		return "compat"
	default:
		return "unknown"
	}
}

// State is one decoded agent state document.
//
// Status is authoritative and independent of Problem: an agent that finished
// with a recovered fault declares status done and a problem describing it, and
// one that is progressing after a transient failure declares status running and
// a problem. Deriving status from the presence of a problem, as a reader that
// conflates them must, makes both of those states unrepresentable.
type State struct {
	// Schema is the contract version the document declared, empty under the
	// compatibility profile.
	Schema string `json:"schema,omitempty"`
	// Profile is the profile the document was read under.
	Profile Profile `json:"-"`
	// Status is the declared state. Required.
	Status Status `json:"status"`
	// Agent is the agent's name. The directory name is used when this is
	// absent.
	Agent string `json:"agent,omitempty"`
	// Task names the work in progress.
	Task string `json:"task,omitempty"`
	// Step is the position within the task.
	Step Step `json:"step,omitzero"`
	// StepsTotal is the number of steps the task is expected to take.
	StepsTotal int `json:"steps_total,omitempty"`
	// Model names the model the agent is running against.
	Model string `json:"model,omitempty"`
	// RunID ties the document to a run directory.
	RunID string `json:"run_id,omitempty"`
	// Problem describes a fault, independent of Status.
	Problem string `json:"problem,omitempty"`
	// Heartbeat is how often the agent undertakes to rewrite the document.
	// A document not rewritten within it is reported stale.
	Heartbeat time.Duration `json:"-"`
	// StartedAt is when the agent began the task.
	StartedAt time.Time `json:"started_at,omitzero"`
	// UpdatedAt is when the document was last written by the agent, which is
	// the agent's own claim rather than the file's modification time.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	// Labels carries integrator-defined key/value metadata.
	Labels map[string]string `json:"labels,omitempty"`
	// Extra preserves members the contract does not define, so a round trip
	// through agentfs does not discard an integrator's own fields.
	Extra map[string]json.RawMessage `json:"-"`
}

// HeartbeatSeconds returns the declared heartbeat in seconds, for the wire form.
func (s State) HeartbeatSeconds() float64 { return s.Heartbeat.Seconds() }

// Rule describes one member of the contract. The table is the single source for
// the published JSON Schema, the generated reference, and the decoder's typing,
// so the three cannot disagree.
type Rule struct {
	// Member is the wire name.
	Member string
	// Type is the JSON type, or "integer|string" for the step union.
	Type string
	// Required marks a member a v1 document must declare.
	Required bool
	// Summary is one sentence describing the member.
	Summary string
	// MaxRunes caps a string member's length; zero is uncapped.
	MaxRunes int
	// Enum lists the permitted values of a closed vocabulary.
	Enum []string
	// Format names an RFC 3339 date-time member.
	Format string
	// Compat lists member names accepted under the compatibility profile.
	Compat []string
}

// MaxStringRunes caps every free-text member. A document is a status
// declaration, not a log: an uncapped string member is an unbounded allocation
// driven by a file agentfs does not control.
const MaxStringRunes = 4096

// Rules returns the contract's members in document order.
func Rules() []Rule {
	return []Rule{
		{Member: "schema", Type: "string", Required: true, Enum: []string{SchemaVersion},
			Summary: "The contract version the document declares."},
		{Member: "status", Type: "string", Required: true, Enum: Vocabulary(),
			Summary: "The state the agent declares itself to be in."},
		{Member: "agent", Type: "string", MaxRunes: 256,
			Summary: "The agent's name. The workspace directory name is used when absent."},
		{Member: "task", Type: "string", MaxRunes: MaxStringRunes,
			Summary: "The work in progress."},
		{Member: "step", Type: "integer|string",
			Summary: "The position within the task: a non-negative ordinal or a named phase."},
		{Member: "steps_total", Type: "integer",
			Summary: "The number of steps the task is expected to take."},
		{Member: "model", Type: "string", MaxRunes: 256,
			Summary: "The model the agent is running against."},
		{Member: "run_id", Type: "string", MaxRunes: 256,
			Summary: "The run directory this document belongs to."},
		{Member: "problem", Type: "string", MaxRunes: MaxStringRunes, Compat: []string{"error"},
			Summary: "A fault description, independent of status."},
		{Member: "heartbeat_seconds", Type: "number",
			Summary: "How often the agent undertakes to rewrite the document. A document older than this is reported stale."},
		{Member: "started_at", Type: "string", Format: "date-time",
			Summary: "When the agent began the task, as an RFC 3339 date-time with an offset."},
		{Member: "updated_at", Type: "string", Format: "date-time",
			Summary: "When the agent last wrote the document, as an RFC 3339 date-time with an offset."},
		{Member: "labels", Type: "object",
			Summary: "Integrator-defined string metadata."},
	}
}

// members indexes the rules by wire name, including compatibility names.
func members() map[string]Rule {
	out := make(map[string]Rule)
	for _, r := range Rules() {
		out[r.Member] = r
		for _, c := range r.Compat {
			out[c] = r
		}
	}
	return out
}
