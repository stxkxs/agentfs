package metrics_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stxkxs/agentfs/internal/metrics"
)

// budgetName is the name the budget tests register under; the tests assert on
// records rather than on names.
const budgetName = "probe"

// fakeClock is a clock a test advances by hand, so a timed span has the
// duration the assertion states rather than one the machine happened to take.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestBudgetWithoutObservationsReportsNothing(t *testing.T) {
	t.Parallel()
	b := metrics.NewBudget(budgetName, metrics.DeadlineScanRoot)
	s := b.Stats()
	if s.Name != budgetName {
		t.Errorf("Name = %q, want %q", s.Name, budgetName)
	}
	if s.Deadline != metrics.DeadlineScanRoot {
		t.Errorf("Deadline = %v, want %v", s.Deadline, metrics.DeadlineScanRoot)
	}
	if s.Count != 0 || s.Breached != 0 {
		t.Errorf("Count = %d, Breached = %d, want 0 and 0", s.Count, s.Breached)
	}
	if s.P50 != 0 || s.P90 != 0 || s.P99 != 0 || s.Max != 0 {
		t.Errorf("percentiles = %v/%v/%v/%v, want zeroes", s.P50, s.P90, s.P99, s.Max)
	}
	if !s.Met() {
		t.Error("Met() = false for a budget nothing has missed")
	}
}

func TestBudgetBreachIsAtOrBeyondTheDeadline(t *testing.T) {
	t.Parallel()
	const deadline = 100 * time.Millisecond
	cases := []struct {
		name string
		d    time.Duration
		want int64
	}{
		{"well inside", 1 * time.Millisecond, 0},
		{"a nanosecond inside", deadline - 1, 0},
		{"exactly at the deadline", deadline, 1},
		{"a nanosecond beyond", deadline + 1, 1},
		{"far beyond", 10 * deadline, 1},
		{"zero", 0, 0},
		{"negative records as zero", -time.Hour, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := metrics.NewBudget(budgetName, deadline)
			b.Observe(tc.d)
			s := b.Stats()
			if s.Count != 1 {
				t.Fatalf("Count = %d, want 1", s.Count)
			}
			if s.Breached != tc.want {
				t.Errorf("Breached = %d, want %d", s.Breached, tc.want)
			}
			if s.Met() != (tc.want == 0) {
				t.Errorf("Met() = %v with Breached = %d", s.Met(), s.Breached)
			}
		})
	}
}

func TestBudgetClampsNegativeObservationsToZero(t *testing.T) {
	t.Parallel()
	b := metrics.NewBudget(budgetName, time.Second)
	b.Observe(-time.Hour)
	s := b.Stats()
	if s.Max != 0 || s.P50 != 0 {
		t.Fatalf("Max = %v, P50 = %v, want zeroes", s.Max, s.P50)
	}
}

func TestBudgetPercentilesUseNearestRank(t *testing.T) {
	t.Parallel()
	b := metrics.NewBudget(budgetName, time.Hour)
	// Observed out of order, so the result depends on the sort rather than
	// on arrival.
	for i := 100; i >= 1; i-- {
		b.Observe(time.Duration(i) * time.Millisecond)
	}
	s := b.Stats()
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"P50", s.P50, 50 * time.Millisecond},
		{"P90", s.P90, 90 * time.Millisecond},
		{"P99", s.P99, 99 * time.Millisecond},
		{"Max", s.Max, 100 * time.Millisecond},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if s.Count != 100 {
		t.Errorf("Count = %d, want 100", s.Count)
	}
}

func TestBudgetPercentileOfOneObservationIsThatObservation(t *testing.T) {
	t.Parallel()
	b := metrics.NewBudget(budgetName, time.Hour)
	b.Observe(7 * time.Millisecond)
	s := b.Stats()
	if s.P50 != 7*time.Millisecond || s.P90 != 7*time.Millisecond || s.P99 != 7*time.Millisecond {
		t.Fatalf("percentiles = %v/%v/%v, want 7ms throughout", s.P50, s.P90, s.P99)
	}
}

func TestBudgetReservoirHoldsTheTrailingWindow(t *testing.T) {
	t.Parallel()
	const (
		early = 1 * time.Millisecond
		late  = 100 * time.Millisecond
	)
	b := metrics.NewBudget(budgetName, time.Hour)
	for range metrics.ReservoirSize * 2 {
		b.Observe(early)
	}
	for range metrics.ReservoirSize {
		b.Observe(late)
	}
	s := b.Stats()
	if want := int64(metrics.ReservoirSize * 3); s.Count != want {
		t.Errorf("Count = %d, want %d", s.Count, want)
	}
	if s.P50 != late {
		t.Errorf("P50 = %v, want %v: the reservoir kept spans the window has passed", s.P50, late)
	}
	if s.Max != late {
		t.Errorf("Max = %v, want %v", s.Max, late)
	}
}

func TestBudgetReservoirWrapsInPlace(t *testing.T) {
	t.Parallel()
	const (
		early = 1 * time.Millisecond
		late  = 3 * time.Millisecond
	)
	b := metrics.NewBudget(budgetName, time.Hour)
	for range metrics.ReservoirSize {
		b.Observe(early)
	}
	// Half the reservoir replaced leaves an even split, so the median is
	// still the earlier value and the upper percentiles are the later one.
	for range metrics.ReservoirSize / 2 {
		b.Observe(late)
	}
	s := b.Stats()
	if s.P50 != early {
		t.Errorf("P50 = %v, want %v", s.P50, early)
	}
	if s.P90 != late {
		t.Errorf("P90 = %v, want %v", s.P90, late)
	}
}

func TestBudgetMaxOutlivesTheReservoir(t *testing.T) {
	t.Parallel()
	const spike = time.Minute
	b := metrics.NewBudget(budgetName, time.Second)
	b.Observe(spike)
	for range metrics.ReservoirSize * 2 {
		b.Observe(time.Millisecond)
	}
	s := b.Stats()
	if s.Max != spike {
		t.Errorf("Max = %v, want %v: a breach the reservoir has forgotten still counts", s.Max, spike)
	}
	if s.Breached != 1 {
		t.Errorf("Breached = %d, want 1", s.Breached)
	}
	if s.Met() {
		t.Error("Met() = true for a budget one observation blew past")
	}
}

func TestRecordTimesTheSpanOnTheSuppliedClock(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	b := metrics.NewBudget(budgetName, 100*time.Millisecond)
	stop := b.Record(clk.now)
	clk.advance(42 * time.Millisecond)
	stop()
	s := b.Stats()
	if s.Count != 1 {
		t.Fatalf("Count = %d, want 1", s.Count)
	}
	if s.Max != 42*time.Millisecond {
		t.Errorf("Max = %v, want 42ms", s.Max)
	}
	if !s.Met() {
		t.Error("Met() = false for 42ms against a 100ms deadline")
	}
}

func TestRecordStopCountsTheSpanOnce(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	b := metrics.NewBudget(budgetName, time.Hour)
	stop := b.Record(clk.now)
	clk.advance(10 * time.Millisecond)
	stop()
	clk.advance(time.Hour)
	stop()
	s := b.Stats()
	if s.Count != 1 {
		t.Fatalf("Count = %d, want 1: a second stop recorded a second span", s.Count)
	}
	if s.Max != 10*time.Millisecond {
		t.Errorf("Max = %v, want 10ms", s.Max)
	}
}

func TestRecordSpansOverlap(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	b := metrics.NewBudget(budgetName, time.Hour)
	outer := b.Record(clk.now)
	clk.advance(5 * time.Millisecond)
	inner := b.Record(clk.now)
	clk.advance(5 * time.Millisecond)
	inner()
	clk.advance(5 * time.Millisecond)
	outer()
	s := b.Stats()
	if s.Count != 2 {
		t.Fatalf("Count = %d, want 2", s.Count)
	}
	if s.Max != 15*time.Millisecond {
		t.Errorf("Max = %v, want 15ms for the enclosing span", s.Max)
	}
	if s.P50 != 5*time.Millisecond {
		t.Errorf("P50 = %v, want 5ms for the enclosed span", s.P50)
	}
}

func TestRecordWithoutAClockUsesTheWallClock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		span time.Duration
		met  bool
	}{
		{"inside the deadline", 30 * time.Millisecond, true},
		{"beyond the deadline", 200 * time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := metrics.NewBudget(budgetName, 100*time.Millisecond)
				stop := b.Record(nil)
				<-time.After(tc.span)
				stop()
				s := b.Stats()
				if s.Max != tc.span {
					t.Errorf("Max = %v, want %v", s.Max, tc.span)
				}
				if s.Met() != tc.met {
					t.Errorf("Met() = %v, want %v", s.Met(), tc.met)
				}
			})
		})
	}
}

func TestStatsIsDetachedFromTheBudget(t *testing.T) {
	t.Parallel()
	b := metrics.NewBudget(budgetName, time.Hour)
	b.Observe(time.Millisecond)
	before := b.Stats()
	b.Observe(time.Minute)
	if before.Count != 1 || before.Max != time.Millisecond {
		t.Fatalf("a later observation reached a record already taken: %+v", before)
	}
	if after := b.Stats(); after.Count != 2 || after.Max != time.Minute {
		t.Fatalf("Stats() = %+v after a second observation", after)
	}
}

// FuzzBudgetObserve holds the record to its internal ordering whatever
// durations arrive: percentiles ascend, none exceeds the largest observation,
// and a breach is counted at most once per observation.
func FuzzBudgetObserve(f *testing.F) {
	f.Add([]byte{1, 2, 3}, int64(2))
	f.Add([]byte{}, int64(0))
	f.Add([]byte{255}, int64(-5))
	f.Add(make([]byte, 4096), int64(1))

	f.Fuzz(func(t *testing.T, ms []byte, deadlineMS int64) {
		if len(ms) > 1<<16 {
			t.Skip()
		}
		deadline := time.Duration(deadlineMS) * time.Millisecond
		b := metrics.NewBudget(budgetName, deadline)
		var largest time.Duration
		for _, m := range ms {
			d := time.Duration(m) * time.Millisecond
			b.Observe(d)
			if d > largest {
				largest = d
			}
		}
		s := b.Stats()
		if s.Count != int64(len(ms)) {
			t.Fatalf("Count = %d, want %d", s.Count, len(ms))
		}
		if s.Breached < 0 || s.Breached > s.Count {
			t.Fatalf("Breached = %d outside 0..%d", s.Breached, s.Count)
		}
		if s.Met() != (s.Breached == 0) {
			t.Fatalf("Met() = %v with Breached = %d", s.Met(), s.Breached)
		}
		if s.Max != largest {
			t.Fatalf("Max = %v, want %v", s.Max, largest)
		}
		if s.P50 > s.P90 || s.P90 > s.P99 || s.P99 > s.Max {
			t.Fatalf("percentiles out of order: %v/%v/%v/%v", s.P50, s.P90, s.P99, s.Max)
		}
		if s.P50 < 0 {
			t.Fatalf("P50 = %v below zero", s.P50)
		}
		if s.Deadline != deadline || s.Name != budgetName {
			t.Fatalf("Stats() lost its identity: %+v", s)
		}
	})
}
