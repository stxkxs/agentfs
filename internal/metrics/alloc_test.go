//go:build !race

package metrics_test

import (
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
)

// TestObserveHoldsItsMemoryOnceTheReservoirIsFull is the direct form of the
// bound the reservoir exists for: past [metrics.ReservoirSize] observations a
// budget takes no further memory, however long the session runs. The race
// detector accounts allocations of its own, so the assertion is made without
// it.
func TestObserveHoldsItsMemoryOnceTheReservoirIsFull(t *testing.T) {
	b := metrics.NewBudget(budgetName, time.Second)
	for range metrics.ReservoirSize {
		b.Observe(time.Millisecond)
	}
	if allocs := testing.AllocsPerRun(1000, func() { b.Observe(time.Millisecond) }); allocs != 0 {
		t.Fatalf("Observe allocated %.2f times per call against a full reservoir", allocs)
	}
}
