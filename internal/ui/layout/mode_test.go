package layout_test

import (
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/ui/layout"
)

func TestModeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode layout.Mode
		want string
	}{
		{layout.ModeBrowse, "browse"},
		{layout.ModeRuns, "runs"},
		{layout.ModeHelp, "help"},
		{layout.ModeBudgets, "budgets"},
		{layout.Mode(4), "mode(4)"},
		{layout.Mode(-1), "mode(-1)"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.mode.String(); got != tc.want {
				t.Errorf("Mode(%d).String() = %q, want %q", int(tc.mode), got, tc.want)
			}
		})
	}
}

// Modes is what every sweep here iterates, so a mode missing from it would take
// the whole suite's coverage of that mode with it.
func TestModesCoversTheVocabulary(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, m := range layout.Modes() {
		name := m.String()
		if strings.HasPrefix(name, "mode(") {
			t.Errorf("Modes() returned %d, which String does not name", int(m))
		}
		if seen[name] {
			t.Errorf("Modes() returned %q twice", name)
		}
		seen[name] = true
	}
	for _, want := range []string{"browse", "runs", "help", "budgets"} {
		if !seen[want] {
			t.Errorf("Modes() omits %q", want)
		}
	}
}

// The returned slice is the caller's to keep; a shared backing array would let
// one caller's edit reach another.
func TestModesReturnsAFreshSlice(t *testing.T) {
	t.Parallel()
	first := layout.Modes()
	first[0] = layout.ModeHelp
	if second := layout.Modes(); second[0] != layout.ModeBrowse {
		t.Fatalf("Modes()[0] = %v after an edit to an earlier result", second[0])
	}
}
