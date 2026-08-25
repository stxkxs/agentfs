package watch

import (
	"fmt"
	"sync"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// hybridSource runs kernel notification and the stat sweep together.
//
// The two overlap: a local write to a watched directory is reported by both. A
// consumer of the batch stream sees one change because the coalescer folds
// them; a consumer of the record stream sees the duplicate and discards it on
// [Change.DedupKey], which is why that stream is documented as at-least-once
// rather than exactly-once.
type hybridSource struct {
	notify *notifySource
	sweep  *sweepSource
	ttl    time.Duration

	mu   sync.Mutex
	seen map[string]time.Time
}

func newHybridSource(root *fsx.Root, maxWatches int, ttl time.Duration) *hybridSource {
	return &hybridSource{
		notify: newNotifySource(root, maxWatches),
		sweep:  newSweepSource(root),
		ttl:    ttl,
		seen:   make(map[string]time.Time),
	}
}

// dedupe reports whether c has already been delivered within the window, and
// records it when it has not.
func (h *hybridSource) dedupe(c Change) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := c.DedupKey()
	if at, ok := h.seen[key]; ok && c.At.Sub(at) < h.ttl {
		return true
	}
	h.seen[key] = c.At

	// The map is bounded by expiry rather than by count, so a long session
	// cannot accumulate keys for paths that stopped changing.
	if len(h.seen) > 4096 {
		for k, at := range h.seen {
			if c.At.Sub(at) >= h.ttl {
				delete(h.seen, k)
			}
		}
	}
	return false
}

func (h *hybridSource) wrap(emit func(Change)) func(Change) {
	return func(c Change) {
		if h.dedupe(c) {
			return
		}
		emit(c)
	}
}

func (h *hybridSource) start(emit func(Change), fail func(error)) (watches int, refused uint64, err error) {
	return h.notify.start(h.wrap(emit), fail)
}

func (h *hybridSource) sweep2(tracked []string, budget int, emit func(Change), fail func(error)) uint64 {
	return h.sweep.sweep(tracked, budget, h.wrap(emit), fail)
}

func (h *hybridSource) seed(tracked []string, fail func(error)) {
	h.notify.seed(tracked, fail)
	h.sweep.seed(tracked, fail)
}

func (h *hybridSource) close() error { return h.notify.close() }

// Manual is an [Observer] a test drives directly. It performs no filesystem
// work, so a test of a consumer asserts what the consumer does with a batch
// rather than how quickly a real filesystem produced one.
type Manual struct {
	out     chan Batch
	mu      sync.Mutex
	tracked []string
	stats   Stats
	closed  bool
}

// NewManual returns an observer whose batches a caller supplies.
func NewManual() *Manual {
	return &Manual{out: make(chan Batch, 64), stats: Stats{Mode: config.ModeNotify}}
}

// Send delivers a batch to the consumer.
func (m *Manual) Send(b Batch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	b.Stats = m.stats
	m.out <- b
}

// Emit delivers one batch carrying the given changes.
func (m *Manual) Emit(changes ...Change) {
	sortChanges(changes)
	m.Send(Batch{Changes: changes, At: time.Now()})
}

// SetStats replaces the statistics the observer reports.
func (m *Manual) SetStats(s Stats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats = s
}

// Tracked returns the paths the consumer asked to have swept.
func (m *Manual) Tracked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.tracked...)
}

// Batches implements [Observer].
func (m *Manual) Batches() <-chan Batch { return m.out }

// Track implements [Observer].
func (m *Manual) Track(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracked = append([]string(nil), paths...)
}

// Stats implements [Observer].
func (m *Manual) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// Close implements [Observer].
func (m *Manual) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.out)
	}
	return nil
}

// New returns an observer over root.
//
// [config.ModeAuto] resolves against the probed filesystem: a local filesystem
// is observed by kernel notification alone, and anything else — a network
// export, a FUSE mount, or a filesystem the probe did not recognize — is
// observed by both mechanisms, because on those a write can reach the workspace
// without passing through this kernel.
func New(root *fsx.Root, opts Options) (Observer, error) {
	opts = opts.withDefaults()
	filesystem := fsx.Classify(root.Name())

	// ModeAuto resolves through the configuration table, so the rule that a
	// non-local filesystem is swept as well as watched has one statement.
	mode := config.Config{Watch: opts.Mode}.FilesystemMode(filesystem.Kind)

	stats := Stats{Mode: mode, Filesystem: filesystem, WatchBudget: opts.MaxWatches}

	var src source
	switch mode {
	case config.ModeNotify:
		src = newNotifySource(root, opts.MaxWatches)
	case config.ModeSweep:
		src = newSweepSource(root)
	case config.ModeHybrid:
		src = hybridAdapter{newHybridSource(root, opts.MaxWatches, opts.DedupTTL)}
	case config.ModeAuto:
		return nil, fmt.Errorf("watch: mode auto did not resolve for filesystem %s", filesystem.Type)
	default:
		return nil, fmt.Errorf("watch: unknown mode %v", mode)
	}

	e := newEngine(root, src, opts, stats)
	if err := e.start(); err != nil {
		_ = src.close()
		return nil, err
	}
	return e, nil
}

// hybridAdapter presents [hybridSource] as a source. The sweep method name
// differs so the two embedded mechanisms cannot be confused at the call site.
type hybridAdapter struct{ h *hybridSource }

func (a hybridAdapter) start(emit func(Change), fail func(error)) (watches int, refused uint64, err error) {
	return a.h.start(emit, fail)
}

func (a hybridAdapter) sweep(tracked []string, budget int, emit func(Change), fail func(error)) uint64 {
	return a.h.sweep2(tracked, budget, emit, fail)
}

func (a hybridAdapter) seed(tracked []string, fail func(error)) { a.h.seed(tracked, fail) }

func (a hybridAdapter) close() error { return a.h.close() }

// watchCounts reports the kernel watches the notification half holds.
func (h *hybridSource) watchCounts() (watches int, refused uint64) { return h.notify.watchCounts() }

// watchCounts implements the source seam for the composed pair.
func (a hybridAdapter) watchCounts() (watches int, refused uint64) { return a.h.watchCounts() }
