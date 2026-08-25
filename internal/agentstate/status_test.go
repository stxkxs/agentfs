package agentstate_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// A terminal status is the one a watcher stops waiting on, so widening the set
// would have it abandon an agent that is still progressing.
func TestTerminalStatusesAreTheOnesNoTransitionFollows(t *testing.T) {
	t.Parallel()
	cases := map[agentstate.Status]bool{
		agentstate.StatusDone:    true,
		agentstate.StatusError:   true,
		agentstate.StatusRunning: false,
		agentstate.StatusIdle:    false,
		agentstate.StatusBlocked: false,
		agentstate.StatusUnknown: false,
	}
	for status, want := range cases {
		if got := status.Terminal(); got != want {
			t.Errorf("%v.Terminal() = %v, want %v", status, got, want)
		}
	}
}

// A status leaves agentfs in the spelling the contract publishes, so a
// consumer reading agentfs output and one reading an agent's document match on
// the same value.
func TestStatusEncodesAsItsCanonicalSpelling(t *testing.T) {
	t.Parallel()
	for _, spelling := range agentstate.Vocabulary() {
		status, ok := agentstate.ParseStatus(spelling, agentstate.ProfileV1)
		if !ok {
			t.Fatalf("the vocabulary carries %q, which does not parse", spelling)
		}
		text, err := status.MarshalText()
		if err != nil {
			t.Fatalf("marshal %v: %v", status, err)
		}
		if string(text) != spelling {
			t.Errorf("%v encodes as %q, want %q", status, text, spelling)
		}
		encoded, err := json.Marshal(status)
		if err != nil {
			t.Fatalf("marshal %v as JSON: %v", status, err)
		}
		if want := `"` + spelling + `"`; string(encoded) != want {
			t.Errorf("%v marshals as %s, want %s", status, encoded, want)
		}
	}
}

// A value outside the vocabulary renders as the unknown spelling rather than
// as an integer, so no output carries a number where a status belongs.
func TestAStatusOutsideTheVocabularyRendersAsUnknown(t *testing.T) {
	t.Parallel()
	for _, status := range []agentstate.Status{agentstate.StatusUnknown, agentstate.Status(-1), agentstate.Status(99)} {
		if got := status.String(); got != "unknown" {
			t.Errorf("Status(%d) renders as %q, want unknown", status, got)
		}
	}
}

// The alias table is the compatibility profile's whole surface: an entry that
// also resolved under v1 would widen the versioned vocabulary, and one that
// resolved to nothing would document a spelling agentfs refuses.
func TestAliasesResolveUnderCompatibilityAndNowhereElse(t *testing.T) {
	t.Parallel()
	table := agentstate.Aliases()
	if len(table) == 0 {
		t.Fatal("the alias table is empty")
	}

	spellings := make([]string, 0, len(table))
	for _, entry := range table {
		spellings = append(spellings, entry.Spelling)

		got, ok := agentstate.ParseStatus(entry.Spelling, agentstate.ProfileCompat)
		if !ok || got != entry.Status {
			t.Errorf("%q resolves to (%v,%v) under the compatibility profile, want (%v,true)",
				entry.Spelling, got, ok, entry.Status)
		}
		if _, ok := agentstate.ParseStatus(entry.Spelling, agentstate.ProfileV1); ok {
			t.Errorf("%q resolves under the versioned contract, where only the vocabulary does", entry.Spelling)
		}
		if !slices.Contains(agentstate.Vocabulary(), entry.Status.String()) {
			t.Errorf("%q resolves to %v, which is outside the vocabulary", entry.Spelling, entry.Status)
		}
	}
	if !slices.IsSorted(spellings) {
		t.Errorf("the alias table is not sorted by spelling: %v", spellings)
	}
}
