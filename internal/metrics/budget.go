package metrics

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// ReservoirSize is the number of observations a budget keeps for estimating
// percentiles. It is what bounds a budget's memory: a session that observes a
// million spans costs the same as one that observes a thousand.
const ReservoirSize = 1024

// Budget is a named response-time deadline and the record of how observed
// durations compared with it.
//
// Count, Breached and Max cover every observation. The percentiles are drawn
// from a reservoir holding the most recent [ReservoirSize] observations, so a
// path that degrades part way through a session shows the change in its
// percentiles while its counts still carry the earlier, faster spans.
type Budget struct {
	name     string
	deadline time.Duration

	mu       sync.Mutex
	ring     []time.Duration
	next     int
	count    int64
	breached int64
	peak     time.Duration
}

// NewBudget returns a budget named name holding observations to deadline. An
// observation at or beyond deadline is a breach, so a deadline of zero or less
// admits nothing.
func NewBudget(name string, deadline time.Duration) *Budget {
	return &Budget{name: name, deadline: deadline}
}

// Observe records that a span took d. A negative d records as zero: it reports
// a clock that ran backwards rather than a span shorter than no time.
func (b *Budget) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if d >= b.deadline {
		b.breached++
	}
	if d > b.peak {
		b.peak = d
	}
	if len(b.ring) < ReservoirSize {
		b.ring = append(b.ring, d)
		return
	}
	b.ring[b.next] = d
	b.next = (b.next + 1) % ReservoirSize
}

// Record starts timing a span and returns the function that ends it. The span
// is observed the first time that function is called and later calls do
// nothing, so a deferred stop beside an explicit one counts the span once.
//
// now supplies the clock, letting a caller time against a clock it controls. A
// nil now uses [time.Now].
func (b *Budget) Record(now func() time.Time) func() {
	if now == nil {
		now = time.Now
	}
	start := now()
	var done atomic.Bool
	return func() {
		if done.Swap(true) {
			return
		}
		b.Observe(now().Sub(start))
	}
}

// Stats returns the budget's record.
func (b *Budget) Stats() BudgetStats {
	b.mu.Lock()
	window := slices.Clone(b.ring)
	s := BudgetStats{
		Name:     b.name,
		Deadline: b.deadline,
		Count:    b.count,
		Breached: b.breached,
		Max:      b.peak,
	}
	b.mu.Unlock()

	slices.Sort(window)
	s.P50 = percentile(window, 50)
	s.P90 = percentile(window, 90)
	s.P99 = percentile(window, 99)
	return s
}

// BudgetStats is one budget's record at a single instant.
type BudgetStats struct {
	// Name is the budget's name.
	Name string
	// Deadline is the duration an observation has to stay under.
	Deadline time.Duration
	// Count is the number of observations, over the whole session.
	Count int64
	// Breached is the number of observations at or beyond Deadline, over the
	// whole session.
	Breached int64
	// P50, P90 and P99 are percentiles of the reservoir, and Max is the
	// largest duration observed over the whole session.
	P50, P90, P99, Max time.Duration
}

// Met reports whether every observation stayed under the deadline. A budget
// with no observations is met, since nothing has missed it; Count is what says
// whether the path was exercised at all.
func (s BudgetStats) Met() bool { return s.Breached == 0 }

// percentile returns the value at rank p, from 0 to 100, of the ascending
// sorted durations, by the nearest-rank method: the smallest value at or beyond
// p percent of the way through. Every result is therefore a duration that was
// observed rather than an interpolation between two that were. A p outside 0 to
// 100 clamps to the nearer end, and an empty input has no percentile and yields
// zero.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
