package metrics

import (
	"slices"
	"testing"
	"time"
)

// TestReservoirStopsGrowingAtItsBound holds the ring to the bound
// [ReservoirSize] states: past that many observations it neither lengthens nor
// is reallocated, so measurement costs a long session what it costs a short
// one. The allocation-counting form of this bound cannot run under the race
// detector, which accounts allocations of its own.
func TestReservoirStopsGrowingAtItsBound(t *testing.T) {
	t.Parallel()
	b := NewBudget("reservoir", time.Second)
	capAtFull := -1
	for i := range ReservoirSize * 3 {
		b.Observe(time.Duration(i) * time.Microsecond)

		b.mu.Lock()
		length, capacity, next := len(b.ring), cap(b.ring), b.next
		b.mu.Unlock()

		if length > ReservoirSize {
			t.Fatalf("observation %d left the ring holding %d, want at most %d", i+1, length, ReservoirSize)
		}
		if next < 0 || next >= ReservoirSize {
			t.Fatalf("observation %d left the write position at %d, outside 0 to %d", i+1, next, ReservoirSize-1)
		}
		if i+1 < ReservoirSize {
			continue
		}
		if length != ReservoirSize {
			t.Fatalf("observation %d left the ring holding %d, want it full at %d", i+1, length, ReservoirSize)
		}
		if capAtFull < 0 {
			capAtFull = capacity
		}
		if capacity != capAtFull {
			t.Fatalf("observation %d moved the ring's capacity from %d to %d", i+1, capAtFull, capacity)
		}
	}
}

// TestReservoirKeepsExactlyTheTrailingObservations is the claim [Budget] makes
// about which observations the percentiles are drawn from: the most recent
// [ReservoirSize] of them and no others, whatever order the ring wrapped in.
func TestReservoirKeepsExactlyTheTrailingObservations(t *testing.T) {
	t.Parallel()
	const extra = 7
	b := NewBudget("reservoir", time.Hour)
	observed := make([]time.Duration, 0, ReservoirSize+extra)
	for i := range ReservoirSize + extra {
		d := time.Duration(i+1) * time.Microsecond
		b.Observe(d)
		observed = append(observed, d)
	}

	b.mu.Lock()
	held := slices.Clone(b.ring)
	b.mu.Unlock()

	want := slices.Clone(observed[extra:])
	slices.Sort(want)
	slices.Sort(held)
	if !slices.Equal(held, want) {
		t.Fatalf("the ring holds %d..%d, want the trailing %d observations %d..%d",
			held[0], held[len(held)-1], ReservoirSize, want[0], want[len(want)-1])
	}
}
