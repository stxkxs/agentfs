package watch

import (
	"testing"
	"time"
)

func TestMergeOpKeepsAPathNewToTheConsumer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prev, next, want Op
	}{
		{OpCreate, OpModify, OpCreate},
		{OpCreate, OpRemove, OpRemove},
		{OpModify, OpModify, OpModify},
		{OpModify, OpRemove, OpRemove},
		{OpRemove, OpCreate, OpCreate},
		{OpModify, OpRename, OpRename},
	}
	for _, tc := range cases {
		if got := mergeOp(tc.prev, tc.next); got != tc.want {
			t.Errorf("mergeOp(%v,%v) = %v, want %v", tc.prev, tc.next, got, tc.want)
		}
	}
}

func TestBuilderFoldsRepeatsOnOnePath(t *testing.T) {
	t.Parallel()
	b := newBuilder(100)
	for range 50 {
		b.add(Change{Path: "a/state.json", Op: OpModify})
	}
	batch := b.snapshot(time.Now(), Stats{})
	if len(batch.Changes) != 1 {
		t.Fatalf("50 writes to one path produced %d changes, want 1", len(batch.Changes))
	}
	if b.coalesced != 49 {
		t.Errorf("coalesced = %d, want 49", b.coalesced)
	}
}

// Loss beyond the ceiling is announced, never absorbed.
func TestBuilderOverflowSetsTruncatedAndResync(t *testing.T) {
	t.Parallel()
	b := newBuilder(4)
	for i := range 10 {
		b.add(Change{Path: string(rune('a' + i)), Op: OpCreate})
	}
	batch := b.snapshot(time.Now(), Stats{})
	if len(batch.Changes) != 4 {
		t.Fatalf("held %d changes, want the ceiling of 4", len(batch.Changes))
	}
	if !batch.Truncated {
		t.Error("an overflowing batch is not marked truncated")
	}
	if !batch.Resync {
		t.Error("an overflowing batch does not request a resync, so loss would be silent")
	}
	if b.dropped != 6 {
		t.Errorf("dropped = %d, want 6", b.dropped)
	}
}

// A batch that could not be delivered while the consumer was busy must
// accumulate rather than be discarded.
func TestBuilderMergeAccumulatesRatherThanDropping(t *testing.T) {
	t.Parallel()
	first := newBuilder(100)
	first.add(Change{Path: "a", Op: OpCreate})
	second := newBuilder(100)
	second.add(Change{Path: "b", Op: OpCreate})
	second.add(Change{Path: "a", Op: OpModify})

	first.merge(second)
	batch := first.snapshot(time.Now(), Stats{})
	if len(batch.Changes) != 2 {
		t.Fatalf("merged batch holds %d changes, want 2", len(batch.Changes))
	}
	if batch.Changes[0].Op != OpCreate {
		t.Errorf("path a folded to %v, want create", batch.Changes[0].Op)
	}
}

func TestSweepSliceCoversTheSetAcrossCycles(t *testing.T) {
	t.Parallel()
	e := &engine{opts: Options{SweepBudget: 2}, tracked: []string{"a", "b", "c", "d", "e"}}

	seen := map[string]int{}
	for range 6 {
		for _, p := range e.sweepSlice() {
			seen[p]++
		}
	}
	for _, p := range e.tracked {
		if seen[p] == 0 {
			t.Errorf("%q was never swept", p)
		}
	}
}

func TestSweepSliceReturnsTheWholeSetWhenItFitsTheBudget(t *testing.T) {
	t.Parallel()
	e := &engine{opts: Options{SweepBudget: 10}, tracked: []string{"a", "b"}}
	if got := e.sweepSlice(); len(got) != 2 {
		t.Fatalf("swept %d of 2 tracked paths", len(got))
	}
}
