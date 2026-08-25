package agentstate

import (
	"encoding/json"
	"strconv"
)

// StepKind discriminates what a [Step] holds.
type StepKind int

// The forms a step takes.
const (
	// StepNone: the document declares no step.
	StepNone StepKind = iota
	// StepOrdinal: the step is a non-negative position in a sequence.
	StepOrdinal
	// StepLabel: the step is a named phase.
	StepLabel
)

// Step is the position an agent declares within its task: either an ordinal or
// a label, never both and never an arbitrary value.
//
// The wire form permits an integer or a string because both are in use, but the
// in-memory form is a closed union rather than an any, so no renderer reaches
// for %v and no consumer has to test what it got.
type Step struct {
	kind    StepKind
	ordinal int
	label   string
}

// NoStep is the zero step.
var NoStep = Step{}

// OrdinalStep returns a step at position n.
func OrdinalStep(n int) Step { return Step{kind: StepOrdinal, ordinal: n} }

// LabelStep returns a step named label.
func LabelStep(label string) Step { return Step{kind: StepLabel, label: label} }

// Kind reports what the step holds.
func (s Step) Kind() StepKind { return s.kind }

// Ordinal returns the position and true when the step is an ordinal.
func (s Step) Ordinal() (int, bool) { return s.ordinal, s.kind == StepOrdinal }

// Label returns the name and true when the step is a label.
func (s Step) Label() (string, bool) { return s.label, s.kind == StepLabel }

// IsZero reports whether the document declared no step.
func (s Step) IsZero() bool { return s.kind == StepNone }

// String renders the step for display, empty when there is none.
func (s Step) String() string {
	switch s.kind {
	case StepOrdinal:
		return strconv.Itoa(s.ordinal)
	case StepLabel:
		return s.label
	case StepNone:
		return ""
	default:
		return ""
	}
}

// MarshalJSON writes the step back in the form it was declared in, so a
// round trip through agentfs preserves the document an agent wrote.
func (s Step) MarshalJSON() ([]byte, error) {
	switch s.kind {
	case StepOrdinal:
		return json.Marshal(s.ordinal)
	case StepLabel:
		return json.Marshal(s.label)
	case StepNone:
		return []byte("null"), nil
	default:
		return []byte("null"), nil
	}
}
