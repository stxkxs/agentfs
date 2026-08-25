package metrics

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// Registry collects the response-time budgets one session holds. A name
// identifies a budget for the life of the registry: asking for the same name
// twice yields the same instrument, so two components timing the same path
// reach it by naming it rather than by threading a pointer between them.
//
// The zero Registry is ready to use, holding no budgets, so a registry embedded
// in a struct measures without a construction step to forget.
type Registry struct {
	mu      sync.Mutex
	budgets map[string]*Budget
}

// NewRegistry returns a registry holding no budgets.
func NewRegistry() *Registry { return &Registry{} }

// Budget returns the budget named name, creating it with deadline on first use.
// The deadline is fixed by that first call, and a later call naming the same
// budget returns it with its record intact: what a budget means cannot shift
// under a report while the session runs.
func (r *Registry) Budget(name string, deadline time.Duration) *Budget {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.budgets[name]
	if !ok {
		if r.budgets == nil {
			r.budgets = make(map[string]*Budget)
		}
		b = NewBudget(name, deadline)
		r.budgets[name] = b
	}
	return b
}

// Budgets returns every budget's record, sorted by name.
//
// The budgets are taken out of the registry before any of them is read, so a
// caller holding the result is not holding the registry's lock. They are then
// read one after another rather than as one atomic act: the result describes a
// session that kept running, not a session held still.
func (r *Registry) Budgets() []BudgetStats {
	r.mu.Lock()
	budgets := make([]*Budget, 0, len(r.budgets))
	for _, b := range r.budgets {
		budgets = append(budgets, b)
	}
	r.mu.Unlock()

	out := make([]BudgetStats, 0, len(budgets))
	for _, b := range budgets {
		out = append(out, b.Stats())
	}
	slices.SortFunc(out, func(a, b BudgetStats) int { return strings.Compare(a.Name, b.Name) })
	return out
}
