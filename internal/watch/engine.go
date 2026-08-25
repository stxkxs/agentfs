package watch

import (
	"errors"
	"sync"
	"time"

	"github.com/stxkxs/agentfs/internal/fsx"
)

// source is a mechanism that discovers changes. The engine owns delivery,
// coalescing and statistics; a source only has to find changes and describe
// what it costs.
type source interface {
	// start begins discovery, pushing changes through emit. It returns the
	// number of kernel watches it established and how many it was refused.
	start(emit func(Change), fail func(error)) (watches int, refused uint64, err error)
	// sweep performs one bounded pass over the tracked set, reporting the
	// operations it spent. A source that does not sweep returns zero.
	sweep(tracked []string, budget int, emit func(Change), fail func(error)) (ops uint64)
	// seed establishes what exists without reporting it as change, so a
	// source's first pass does not announce the whole workspace as created.
	seed(tracked []string, fail func(error))
	// watchCounts reports the kernel watches the source holds and how many it
	// was refused. A source that establishes none reports zero.
	watchCounts() (watches int, refused uint64)
	// close releases the source's resources.
	close() error
}

// builder accumulates changes for one batch, folding repeats on the same path.
type builder struct {
	byPath    map[string]Change
	order     []string
	coalesced uint64
	dropped   uint64
	truncated bool
	resync    bool
	// queueOverflow and batchCeiling name which ceiling lost a change. They
	// are separate because they are raised by different flags, and a hint
	// naming the wrong knob sends an operator to change a setting that was
	// never reached.
	queueOverflow bool
	batchCeiling  bool
	rootLost      bool
	rootBack      bool
	seeded        bool
	maxBatch      int
}

func newBuilder(maxBatch int) *builder {
	return &builder{byPath: make(map[string]Change), maxBatch: maxBatch}
}

// add folds c into the batch. Two changes to one path within a window are one
// change to the consumer, which is what makes a fifty-write burst cost one
// rebuild.
func (b *builder) add(c Change) {
	if prev, seen := b.byPath[c.Path]; seen {
		b.coalesced++
		c.Op = mergeOp(prev.Op, c.Op)
		c.IsDir = prev.IsDir || c.IsDir
		b.byPath[c.Path] = c
		return
	}
	if len(b.order) >= b.maxBatch {
		// Beyond the ceiling the batch is no longer a complete account, so the
		// consumer is told to rebuild rather than handed a partial one.
		b.dropped++
		b.batchCeiling = true
		b.truncated = true
		b.resync = true
		return
	}
	b.byPath[c.Path] = c
	b.order = append(b.order, c.Path)
}

// merge folds another builder into b, so a batch that could not be delivered
// while the consumer was busy accumulates rather than being discarded.
func (b *builder) merge(o *builder) {
	for _, p := range o.order {
		b.add(o.byPath[p])
	}
	b.coalesced += o.coalesced
	b.dropped += o.dropped
	b.queueOverflow = b.queueOverflow || o.queueOverflow
	b.batchCeiling = b.batchCeiling || o.batchCeiling
	b.truncated = b.truncated || o.truncated
	b.resync = b.resync || o.resync
	b.rootLost = o.rootLost
	b.rootBack = b.rootBack || o.rootBack
	// A seed that has since accumulated change is no longer only a seed, and a
	// consumer that skipped it would skip the changes with it.
	if len(b.order) > 0 || b.resync || b.rootLost || b.rootBack {
		b.seeded = false
	}
}

func (b *builder) snapshot(at time.Time, stats Stats) Batch {
	changes := make([]Change, 0, len(b.order))
	for _, p := range b.order {
		changes = append(changes, b.byPath[p])
	}
	sortChanges(changes)
	return Batch{
		Changes:       changes,
		At:            at,
		Seeded:        b.seeded,
		Truncated:     b.truncated,
		Resync:        b.resync,
		RootLost:      b.rootLost,
		RootRecovered: b.rootBack,
		Stats:         stats,
	}
}

// mergeOp folds two operations on one path into the one a consumer must act on.
func mergeOp(prev, next Op) Op {
	switch {
	case prev == OpCreate && next == OpModify:
		// The path is still new to the consumer.
		return OpCreate
	case prev == OpRemove && next == OpCreate:
		return OpCreate
	default:
		return next
	}
}

// engine owns delivery for every observer. A single goroutine holds the batch
// state, so coalescing needs no lock and the ordering of a change against a
// window close is not a race.
type engine struct {
	opts Options
	root *fsx.Root
	src  source

	raw     chan Change
	lost    chan struct{}
	errs    chan error
	trackCh chan []string
	statsCh chan chan Stats
	out     chan Batch
	quit    chan struct{}
	stopped sync.Once
	wg      sync.WaitGroup

	// Owned by run.
	stats   Stats
	tracked []string
	cursor  int
}

func newEngine(root *fsx.Root, src source, opts Options, stats Stats) *engine {
	opts = opts.withDefaults()
	return &engine{
		opts:    opts,
		root:    root,
		src:     src,
		raw:     make(chan Change, opts.MaxQueue),
		lost:    make(chan struct{}, 1),
		errs:    make(chan error, 64),
		trackCh: make(chan []string, 1),
		statsCh: make(chan chan Stats),
		out:     make(chan Batch),
		quit:    make(chan struct{}),
		stats:   stats,
	}
}

func (e *engine) emit(c Change) {
	select {
	case e.raw <- c:
	case <-e.quit:
	default:
		// The queue is full only when discovery has outrun delivery for a
		// whole window. The change itself is gone, so the consumer is told to
		// rebuild rather than handed a batch that is not a complete account —
		// and the loss is counted, because a resync with no number beside it
		// tells an operator nothing about how much was missed.
		e.overflow()
	}
}

// overflow records a change the queue could not hold. It never blocks: the
// path it serves is the one where the queue is already full.
func (e *engine) overflow() {
	select {
	case e.lost <- struct{}{}:
	default:
		// Even the overflow signal is full, which means a resync is already
		// pending. One resync covers any number of losses.
	}
}

func (e *engine) fail(err error) {
	if err == nil {
		return
	}
	select {
	case e.errs <- err:
	default:
	}
}

func (e *engine) start() error {
	watches, refused, err := e.src.start(e.emit, e.fail)
	if err != nil {
		return err
	}
	e.stats.Watches = watches
	e.stats.WatchesRefused = refused
	e.stats.WatchBudget = e.opts.MaxWatches

	e.wg.Add(1)
	go e.run()
	return nil
}

//nolint:gocyclo // one arm per event the loop must serialize; splitting it would need shared locks.
func (e *engine) run() {
	defer e.wg.Done()
	defer close(e.out)

	ticker := time.NewTicker(e.opts.Interval)
	defer ticker.Stop()
	window := time.NewTimer(e.opts.Window)
	if !window.Stop() {
		<-window.C
	}

	windowOpen := false
	retry := e.opts.RootRetryMin
	var nextProbe time.Time

	// The first delivery establishes what exists without reporting it as
	// change. It is queued rather than sent here: sending before the loop
	// starts would block every other request — a Stats or a Track call — until
	// a consumer read it, and a caller that inspects the observer before
	// reading its batches is doing nothing unreasonable.
	e.src.seed(e.tracked, e.fail)
	var pending *builder
	ready := newBuilder(e.opts.MaxBatch)
	ready.seeded = true

	for {
		var outCh chan Batch
		var next Batch
		if ready != nil {
			outCh = e.out
			next = ready.snapshot(time.Now(), e.stats)
		}
		var windowC <-chan time.Time
		if windowOpen {
			windowC = window.C
		}

		select {
		case <-e.quit:
			return

		case c := <-e.raw:
			e.stats.Events++
			if pending == nil {
				pending = newBuilder(e.opts.MaxBatch)
			}
			pending.add(c)
			if !windowOpen {
				window.Reset(e.opts.Window)
				windowOpen = true
			}

		case <-e.lost:
			// The count is folded once, when the window closes, alongside every
			// other change the batch lost. Counting here as well would report
			// one lost change as two.
			if pending == nil {
				pending = newBuilder(e.opts.MaxBatch)
			}
			pending.dropped++
			pending.queueOverflow = true
			pending.truncated = true
			pending.resync = true
			if !windowOpen {
				window.Reset(e.opts.Window)
				windowOpen = true
			}

		case err := <-e.errs:
			e.stats.Errors++
			e.stats.LastError = err.Error()
			if errors.Is(err, fsx.ErrRootLost) {
				if pending == nil {
					pending = newBuilder(e.opts.MaxBatch)
				}
				pending.rootLost = true
				e.stats.RootLost = true
				if !windowOpen {
					window.Reset(e.opts.Window)
					windowOpen = true
				}
			}

		case <-windowC:
			windowOpen = false
			if pending != nil {
				if ready == nil {
					ready = pending
				} else {
					ready.merge(pending)
				}
				e.stats.Coalesced += pending.coalesced
				e.stats.Dropped += pending.dropped
				if pending.queueOverflow {
					e.stats.QueueOverflows++
				}
				if pending.batchCeiling {
					e.stats.BatchCeilingHits++
				}
				pending = nil
			}

		case <-ticker.C:
			now := time.Now()
			if e.stats.RootLost {
				if now.Before(nextProbe) {
					continue
				}
				// Reopening resolves the path again; only a successful read
				// through the reopened root proves the workspace is back. A
				// reopen that succeeds against a still-broken mount would
				// declare recovery and then immediately lose it again.
				if err := e.root.Reopen(); err != nil {
					retry = min(retry*2, e.opts.RootRetryMax)
					nextProbe = now.Add(retry)
					continue
				}
				if err := e.root.Health(); err != nil {
					retry = min(retry*2, e.opts.RootRetryMax)
					nextProbe = now.Add(retry)
					continue
				}
				retry = e.opts.RootRetryMin
				e.stats.RootLost = false
				e.src.seed(e.tracked, e.fail)
				b := newBuilder(e.opts.MaxBatch)
				b.rootBack = true
				b.resync = true
				if ready == nil {
					ready = b
				} else {
					ready.merge(b)
				}
				continue
			}
			if err := e.root.Health(); err != nil {
				e.fail(err)
				continue
			}
			start := time.Now()
			ops := e.src.sweep(e.sweepSlice(), e.opts.SweepBudget, e.emit, e.fail)
			e.stats.SweepOps = ops
			e.stats.SweepCycle = time.Since(start)
			e.stats.Tracked = len(e.tracked)
			// A workspace that grows past the watch budget while running does
			// so between ticks, so the counts are re-read rather than taken
			// once at startup. A degradation reported only at startup is one
			// the status line never shows.
			e.stats.Watches, e.stats.WatchesRefused = e.src.watchCounts()

		case outCh <- next:
			ready = nil

		case paths := <-e.trackCh:
			e.tracked = paths
			e.stats.Tracked = len(paths)
			if e.cursor >= len(paths) {
				e.cursor = 0
			}

		case reply := <-e.statsCh:
			reply <- e.stats
		}
	}
}

// sweepSlice returns the next portion of the tracked set to sweep. A set larger
// than the budget is covered across successive cycles rather than in one, so a
// large set slows detection instead of stalling the loop.
func (e *engine) sweepSlice() []string {
	if len(e.tracked) == 0 {
		return nil
	}
	budget := e.opts.SweepBudget
	if budget >= len(e.tracked) {
		e.cursor = 0
		return e.tracked
	}
	if e.cursor >= len(e.tracked) {
		e.cursor = 0
	}
	end := min(e.cursor+budget, len(e.tracked))
	slice := e.tracked[e.cursor:end]
	e.cursor = end
	if e.cursor >= len(e.tracked) {
		e.cursor = 0
	}
	return slice
}

// Batches implements [Observer].
func (e *engine) Batches() <-chan Batch { return e.out }

// Track implements [Observer].
func (e *engine) Track(paths []string) {
	cp := append([]string(nil), paths...)
	select {
	case e.trackCh <- cp:
	case <-e.quit:
	}
}

// Stats implements [Observer].
func (e *engine) Stats() Stats {
	reply := make(chan Stats, 1)
	select {
	case e.statsCh <- reply:
		return <-reply
	case <-e.quit:
		return e.stats
	}
}

// Close implements [Observer].
func (e *engine) Close() error {
	var err error
	e.stopped.Do(func() {
		close(e.quit)
		err = e.src.close()
		e.wg.Wait()
	})
	return err
}
