package agentstate_test

import (
	"encoding/json"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// A renderer that reached for %v over an any would print a step's Go form. The
// union is closed so every consumer branches on a kind instead.
func TestStepKindDiscriminatesTheUnion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		step agentstate.Step
		want agentstate.StepKind
	}{
		{agentstate.NoStep, agentstate.StepNone},
		{agentstate.OrdinalStep(0), agentstate.StepOrdinal},
		{agentstate.OrdinalStep(7), agentstate.StepOrdinal},
		{agentstate.LabelStep("retrieval"), agentstate.StepLabel},
	}
	for _, tc := range cases {
		if got := tc.step.Kind(); got != tc.want {
			t.Errorf("Kind of %v = %v, want %v", tc.step, got, tc.want)
		}
	}
}

// A step at ordinal zero is a declared position, not an absent one.
func TestOnlyAnUndeclaredStepIsZero(t *testing.T) {
	t.Parallel()
	if !agentstate.NoStep.IsZero() {
		t.Error("NoStep does not report itself as undeclared")
	}
	for _, s := range []agentstate.Step{
		agentstate.OrdinalStep(0),
		agentstate.OrdinalStep(7),
		agentstate.LabelStep("retrieval"),
	} {
		if s.IsZero() {
			t.Errorf("%v reports itself as undeclared", s)
		}
	}
}

func TestStepRendersTheFormItWasDeclaredIn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		step agentstate.Step
		want string
	}{
		{agentstate.NoStep, ""},
		{agentstate.OrdinalStep(0), "0"},
		{agentstate.OrdinalStep(42), "42"},
		{agentstate.LabelStep("retrieval"), "retrieval"},
	}
	for _, tc := range cases {
		if got := tc.step.String(); got != tc.want {
			t.Errorf("String of %#v = %q, want %q", tc.step, got, tc.want)
		}
	}
}

// A document an agent wrote survives a trip through agentfs, so a step
// declared as an integer is written back as an integer and one declared as a
// phase name as a string.
func TestStepMarshalsBackIntoItsDeclaredForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		step agentstate.Step
		want string
	}{
		{agentstate.NoStep, `null`},
		{agentstate.OrdinalStep(0), `0`},
		{agentstate.OrdinalStep(42), `42`},
		{agentstate.LabelStep("retrieval"), `"retrieval"`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.step)
		if err != nil {
			t.Fatalf("marshal %#v: %v", tc.step, err)
		}
		if string(got) != tc.want {
			t.Errorf("marshal of %#v = %s, want %s", tc.step, got, tc.want)
		}
	}
}
