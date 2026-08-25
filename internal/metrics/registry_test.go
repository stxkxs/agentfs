package metrics_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
)

// The budget names the registry tests register under, chosen so that the order
// they are registered in is not the order an enumeration has to return.
const (
	nameAlpha = "alpha"
	nameBeta  = "beta"
	nameGamma = "gamma"
)

func TestRegistryFixesABudgetDeadlineAtItsFirstRegistration(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	first := r.Budget(nameAlpha, 100*time.Millisecond)
	first.Observe(150 * time.Millisecond)

	second := r.Budget(nameAlpha, time.Hour)
	if first != second {
		t.Fatal("Budget returned a different instrument for the same name")
	}
	s := second.Stats()
	if s.Deadline != 100*time.Millisecond {
		t.Errorf("Deadline = %v, want 100ms: a later call moved the deadline", s.Deadline)
	}
	if s.Count != 1 || s.Breached != 1 {
		t.Errorf("Count = %d, Breached = %d, want 1 and 1: the record was reset", s.Count, s.Breached)
	}
}

func TestRegistryReturnsOneBudgetPerName(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	if other := r.Budget(nameBeta, time.Second); other == r.Budget(nameAlpha, time.Second) {
		t.Fatal("Budget returned one instrument for two names")
	}
}

// TestZeroRegistryTakesABudget holds the registry to the zero value its
// documentation offers: the first registration has to reach a registry that was
// declared rather than constructed, which is where a map that was never made
// would surface.
func TestZeroRegistryTakesABudget(t *testing.T) {
	t.Parallel()
	var r metrics.Registry
	r.Budget(nameBeta, time.Second).Observe(2 * time.Second)

	got := r.Budgets()
	if len(got) != 1 || got[0].Breached != 1 {
		t.Fatalf("Budgets() = %+v, want one breached observation", got)
	}
}

func TestBudgetsOfAnEmptyRegistry(t *testing.T) {
	t.Parallel()
	if got := metrics.NewRegistry().Budgets(); len(got) != 0 {
		t.Errorf("Budgets() = %+v, want no instruments", got)
	}
}

func TestBudgetsSortByName(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	for _, name := range []string{nameGamma, nameAlpha, nameBeta} {
		r.Budget(name, time.Second).Observe(time.Millisecond)
	}

	names := make([]string, 0, 3)
	for _, b := range r.Budgets() {
		names = append(names, b.Name)
	}
	if want := []string{nameAlpha, nameBeta, nameGamma}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestBudgetsReadCurrentValues(t *testing.T) {
	t.Parallel()
	r := metrics.NewRegistry()
	b := r.Budget(nameAlpha, 10*time.Millisecond)

	if got := r.Budgets(); len(got) != 1 || got[0].Count != 0 {
		t.Fatalf("Budgets() = %+v, want one budget with no observation", got)
	}
	b.Observe(25 * time.Millisecond)
	got := r.Budgets()
	if len(got) != 1 || got[0].Count != 1 || got[0].Met() {
		t.Errorf("Budgets() = %+v, want one breached observation", got)
	}
}
