package watch

import (
	"fmt"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
)

func newTestHybrid(ttl time.Duration) *hybridSource {
	return newHybridSource(fsx.New("ws", fstest.MapFS{}), 64, ttl)
}

// Kernel notification and the sweep both report a local write to a tracked
// directory. Suppressing the second report inside the window is what keeps the
// record stream from doubling every change on a hybrid root; admitting it once
// the window has passed is what stops a path that keeps changing at a steady
// cadence from being reported once and then never again.
func TestHybridDedupeFoldsARepeatInsideTheWindowAndAdmitsItAfter(t *testing.T) {
	t.Parallel()
	const ttl = 100 * time.Millisecond
	h := newTestHybrid(ttl)
	base := time.Now()
	at := func(d time.Duration) Change {
		return Change{Path: "agent/state.json", Op: OpModify, At: base.Add(d)}
	}

	if h.dedupe(at(0)) {
		t.Fatal("the first report of a change was suppressed")
	}
	if !h.dedupe(at(ttl / 2)) {
		t.Error("a repeat inside the window was admitted, so one write reaches the consumer twice")
	}
	if h.dedupe(at(ttl)) {
		t.Error("a repeat at the window boundary was suppressed")
	}
	if h.dedupe(at(3 * ttl)) {
		t.Error("a change past the window was suppressed, so a steadily changing path stops being reported")
	}
}

// A path that was created and then removed inside one window is two things the
// consumer must act on, not one.
func TestHybridDedupeSeparatesOperationsOnOnePath(t *testing.T) {
	t.Parallel()
	h := newTestHybrid(time.Second)
	now := time.Now()

	if h.dedupe(Change{Path: "agent/state.json", Op: OpCreate, At: now}) {
		t.Fatal("the first report was suppressed")
	}
	if h.dedupe(Change{Path: "agent/state.json", Op: OpRemove, At: now}) {
		t.Error("a removal was folded into the creation that preceded it")
	}
	if h.dedupe(Change{Path: "other/state.json", Op: OpCreate, At: now}) {
		t.Error("a change to one path suppressed the same operation on another")
	}
}

// The dedup table is bounded by expiry rather than by count, so a session that
// runs for days cannot accumulate an entry for every path that ever changed.
func TestHybridDedupeExpiresRatherThanGrowingWithoutBound(t *testing.T) {
	t.Parallel()
	const ttl = time.Second
	h := newTestHybrid(ttl)
	base := time.Now()

	for i := range 4100 {
		h.dedupe(Change{Path: fmt.Sprintf("agent/%d/state.json", i), Op: OpModify, At: base})
	}
	h.dedupe(Change{Path: "agent/late/state.json", Op: OpModify, At: base.Add(2 * ttl)})

	h.mu.Lock()
	held := len(h.seen)
	h.mu.Unlock()
	if held != 1 {
		t.Errorf("the dedup table holds %d entries after 4100 expired, want 1", held)
	}
}

// wrap is what the two mechanisms emit through, so the suppression has to
// happen before the engine ever sees the change.
func TestHybridWrapDeliversARepeatOnce(t *testing.T) {
	t.Parallel()
	h := newTestHybrid(time.Second)
	var got []Change
	emit := h.wrap(func(c Change) { got = append(got, c) })

	c := Change{Path: "agent/state.json", Op: OpModify, At: time.Now()}
	emit(c)
	emit(c)

	if len(got) != 1 {
		t.Fatalf("one change reported by both mechanisms reached the engine %d times, want 1", len(got))
	}
	if got[0].Path != c.Path {
		t.Errorf("delivered %+v, want %+v", got[0], c)
	}
}

// A test that drives a consumer supplies the statistics that consumer renders,
// so the status line can be exercised without a degraded filesystem.
func TestManualReportsTheStatisticsItWasGiven(t *testing.T) {
	t.Parallel()
	m := NewManual()
	t.Cleanup(func() { _ = m.Close() })

	if got := m.Stats().Mode; got != config.ModeNotify {
		t.Errorf("a fresh manual observer reports mode %v, want notify", got)
	}

	want := Stats{
		Mode:           config.ModeHybrid,
		Filesystem:     fsx.Filesystem{Kind: fsx.KindNetwork, Type: "nfs"},
		Watches:        3,
		WatchBudget:    8,
		WatchesRefused: 2,
		Dropped:        7,
		Tracked:        4,
	}
	m.SetStats(want)

	if got := m.Stats(); got != want {
		t.Errorf("Stats() = %+v, want %+v", got, want)
	}

	m.Emit(Change{Path: "agent/state.json", Op: OpModify})
	b := <-m.Batches()
	if b.Stats != want {
		t.Errorf("batch carried stats %+v, want %+v", b.Stats, want)
	}
	// The status line renders these in order of seriousness, so a supplied
	// degradation is rendered exactly as a real one would be.
	wantDegraded := []string{"7 changes dropped", "2 dirs swept not watched"}
	if got := b.Stats.Degradations(); !slices.Equal(got, wantDegraded) {
		t.Errorf("degradations = %v, want %v", got, wantDegraded)
	}
}

// A producer may still be emitting when the consumer shuts the observer down.
// Sending into a closed channel would take the process with it.
func TestManualSendAfterCloseDeliversNothingAndDoesNotPanic(t *testing.T) {
	t.Parallel()
	m := NewManual()
	if err := m.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	m.Emit(Change{Path: "agent/state.json", Op: OpModify})
	m.Send(Batch{Resync: true})

	if b, ok := <-m.Batches(); ok {
		t.Errorf("a closed observer delivered %+v", b)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// A batch is ordered by path and then by operation, so a consumer applying
// changes in order sees a creation before the removal that followed it.
func TestBatchOrdersChangesByPathThenOperation(t *testing.T) {
	t.Parallel()
	m := NewManual()
	t.Cleanup(func() { _ = m.Close() })

	m.Emit(
		Change{Path: "b/state.json", Op: OpCreate},
		Change{Path: "a/state.json", Op: OpRemove},
		Change{Path: "a/state.json", Op: OpCreate},
	)

	want := []Change{
		{Path: "a/state.json", Op: OpCreate},
		{Path: "a/state.json", Op: OpRemove},
		{Path: "b/state.json", Op: OpCreate},
	}
	if got := (<-m.Batches()).Changes; !slices.Equal(got, want) {
		t.Errorf("batch = %v, want %v", got, want)
	}
}

func TestOpStringNamesEveryOperation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		op   Op
		want string
	}{
		{OpCreate, "create"},
		{OpModify, "modify"},
		{OpRemove, "remove"},
		{OpRename, "rename"},
		{Op(9), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.op.String(); got != tc.want {
			t.Errorf("Op(%d).String() = %q, want %q", int(tc.op), got, tc.want)
		}
	}
}

// The record stream is at-least-once, so a consumer discards a repeat on this
// key. A key that ignored the operation would swallow the removal that
// followed a creation of the same path.
func TestChangeDedupKeyDistinguishesOperationsAndPaths(t *testing.T) {
	t.Parallel()
	create := Change{Path: "agent/state.json", Op: OpCreate, At: time.Now()}
	remove := Change{Path: "agent/state.json", Op: OpRemove, At: time.Now().Add(time.Hour)}

	if create.DedupKey() == remove.DedupKey() {
		t.Error("a creation and a removal of one path share a dedup key")
	}
	later := create
	later.At = create.At.Add(time.Hour)
	if create.DedupKey() != later.DedupKey() {
		t.Error("the same change observed twice produced different dedup keys")
	}
	if got := create.String(); got != "create agent/state.json" {
		t.Errorf("Change.String() = %q, want %q", got, "create agent/state.json")
	}
}

// Empty is what a consumer skips a rebuild on, so anything that requires work
// must not report empty.
func TestBatchEmptyOnlyWhenThereIsNothingToActOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		batch Batch
		want  bool
	}{
		{"nothing happened", Batch{}, true},
		{"the seeded batch establishes what exists", Batch{Seeded: true}, true},
		{"a change arrived", Batch{Changes: []Change{{Path: "a"}}}, false},
		{"a resync was requested", Batch{Resync: true}, false},
		{"the root went away", Batch{RootLost: true}, false},
		{"the root came back", Batch{RootRecovered: true}, false},
	}
	for _, tc := range cases {
		if got := tc.batch.Empty(); got != tc.want {
			t.Errorf("%s: Empty() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Each source implements the whole interface, and the members outside its own
// mechanism report the absence rather than an approximation. A sweep that
// claimed a kernel watch, or a notify source that reported a sweep it never
// ran, would make `agentfs doctor` describe observation the process does not
// perform.
func TestASourceReportsNothingForTheMechanismItDoesNotUse(t *testing.T) {
	t.Parallel()

	t.Run("notification seeds and sweeps nothing", func(t *testing.T) {
		t.Parallel()
		n := newNotifySource(fsx.New("ws", fstest.MapFS{}), 32)
		t.Cleanup(func() { _ = n.close() })

		var failures []error
		n.seed([]string{"."}, func(err error) { failures = append(failures, err) })
		if len(failures) != 0 {
			t.Errorf("seeding raised %v", failures)
		}
		var changes []Change
		if ops := n.sweep([]string{"."}, 64,
			func(c Change) { changes = append(changes, c) },
			func(err error) { failures = append(failures, err) }); ops != 0 {
			t.Errorf("sweeping spent %d filesystem operations", ops)
		}
		if len(changes) != 0 || len(failures) != 0 {
			t.Errorf("sweeping reported %v and %v", changes, failures)
		}
	})

	t.Run("the sweep establishes no kernel watch", func(t *testing.T) {
		t.Parallel()
		s := newSweepSource(fsx.New("ws", fstest.MapFS{}))
		t.Cleanup(func() { _ = s.close() })

		watches, refused := s.watchCounts()
		if watches != 0 || refused != 0 {
			t.Errorf("the sweep reports %d watches and %d refused", watches, refused)
		}
	})
}
