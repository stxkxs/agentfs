// Package watch observes a workspace for change and delivers coalesced batches.
//
// Kernel change notification reports activity that passes through this kernel's
// VFS. A write made by another client of an NFS or EFS export, or an object
// replaced behind a FUSE mount, passes through a different one and raises no
// event here. A workspace on such a mount is therefore observed by a bounded
// stat sweep in addition to kernel events, and [Hybrid] runs both. Which
// mechanism a root gets is chosen from [fsx.Classify], and the choice is
// reported in [Stats] so an operator can see how the workspace is being read
// rather than infer it.
//
// Cost is a function of the tracked set, not of the workspace. A consumer
// calls [Observer.Track] with the paths it can actually display, so sweeping a
// 400,000-file workspace costs the same as sweeping a small one.
package watch

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// Op is the kind of change observed at a path.
type Op int

// The observable changes.
const (
	// OpCreate: the path came into existence.
	OpCreate Op = iota
	// OpModify: the path's content or metadata changed.
	OpModify
	// OpRemove: the path ceased to exist.
	OpRemove
	// OpRename: the path was renamed. A rename is reported as a removal at the
	// old name and a creation at the new one when both are observable.
	OpRename
)

// String returns the lowercase name of the operation.
func (o Op) String() string {
	switch o {
	case OpCreate:
		return "create"
	case OpModify:
		return "modify"
	case OpRemove:
		return "remove"
	case OpRename:
		return "rename"
	default:
		return "unknown"
	}
}

// Change is one observed change to one path.
type Change struct {
	// Path is workspace-relative and slash-separated, already normalized by
	// [fsx.Clean]. A change whose path escapes the root is dropped at the
	// source rather than delivered.
	Path string `json:"path"`
	// Op is what happened.
	Op Op `json:"op"`
	// IsDir reports whether the path is a directory, as far as the source
	// could tell. A removal cannot be stated, so it reports false.
	IsDir bool `json:"is_dir"`
	// At is when the change was observed, which for a swept change is the
	// sweep rather than the write.
	At time.Time `json:"at"`
}

// DedupKey identifies a change independently of when it was observed, so a
// consumer of the at-least-once stream can discard a repeat that arrived from
// a second source.
func (c Change) DedupKey() string {
	return c.Path + "\x00" + c.Op.String()
}

// String renders the change for a log line.
func (c Change) String() string { return c.Op.String() + " " + c.Path }

// Stats is what a source has observed. Every way the observer can fall behind
// or degrade is a number here, and every number here is rendered on screen, so
// an operator can answer "am I seeing everything" rather than assume it.
type Stats struct {
	// Mode is the detection mechanism in use.
	Mode config.Mode `json:"mode"`
	// Filesystem is what the root was probed as.
	Filesystem fsx.Filesystem `json:"filesystem"`
	// Events is the number of raw changes the source observed.
	Events uint64 `json:"events"`
	// Coalesced is the number of raw changes folded into another change
	// because they named the same path within one batch.
	Coalesced uint64 `json:"coalesced"`
	// Deduplicated is the number of changes discarded because another source
	// had already reported them within the dedup window.
	Deduplicated uint64 `json:"deduplicated"`
	// Dropped is the number of changes lost, whichever ceiling lost them. A
	// non-zero value always accompanies a resync, so loss is announced rather
	// than silent.
	Dropped uint64 `json:"dropped"`
	// QueueOverflows and BatchCeilingHits count the occasions each ceiling was
	// reached. They are separate from Dropped and from each other because they
	// are raised by different flags: an operator told only how much was lost
	// cannot tell which setting to change.
	QueueOverflows   uint64 `json:"queue_overflows"`
	BatchCeilingHits uint64 `json:"batch_ceiling_hits"`
	// Errors is the number of source errors observed.
	Errors uint64 `json:"errors"`
	// LastError names the most recent source error.
	LastError string `json:"last_error,omitempty"`
	// Watches is the number of kernel watches held.
	Watches int `json:"watches"`
	// WatchBudget is the ceiling on Watches.
	WatchBudget int `json:"watch_budget"`
	// WatchesRefused counts directories that could not be watched because the
	// budget or the kernel refused. Those directories are swept instead.
	WatchesRefused uint64 `json:"watches_refused"`
	// Tracked is the size of the swept set.
	Tracked int `json:"tracked"`
	// SweepOps is the number of filesystem operations the last sweep cycle
	// performed.
	SweepOps uint64 `json:"sweep_ops"`
	// SweepCycle is how long the last full pass over the tracked set took.
	SweepCycle time.Duration `json:"sweep_cycle"`
	// RootLost reports that the workspace root is unreadable.
	RootLost bool `json:"root_lost"`
}

// Degradations returns one short phrase per condition that limits what the
// observer can see, most serious first, or nil when the observer is healthy.
// The status line renders these; an empty result is what "seeing everything"
// looks like.
func (s Stats) Degradations() []string {
	var out []string
	if s.RootLost {
		out = append(out, "root unreadable")
	}
	if s.Dropped > 0 {
		out = append(out, fmt.Sprintf("%d changes dropped", s.Dropped))
	}
	if s.WatchesRefused > 0 {
		out = append(out, fmt.Sprintf("%d dirs swept not watched", s.WatchesRefused))
	}
	if s.Errors > 0 {
		out = append(out, fmt.Sprintf("%d source errors", s.Errors))
	}
	return out
}

// Batch is a set of changes delivered together.
//
// A batch is the unit of delivery because a consumer that rebuilt its view once
// per raw event would rebuild it fifty times for one agent's write burst.
type Batch struct {
	// Changes are ordered by path, then by operation.
	Changes []Change `json:"changes,omitempty"`
	// At is when the batch was closed.
	At time.Time `json:"at"`
	// Seeded marks a source's first delivery. It carries no changes: the first
	// pass establishes what exists, and reporting the whole workspace as
	// created would announce a storm that did not happen.
	Seeded bool `json:"seeded,omitempty"`
	// Truncated reports that changes were discarded because the batch reached
	// its ceiling. It always accompanies Resync.
	Truncated bool `json:"truncated,omitempty"`
	// Resync tells the consumer to rebuild from the filesystem rather than
	// apply Changes, because the batch is not a complete account of what
	// happened.
	Resync bool `json:"resync,omitempty"`
	// RootLost reports that the workspace root became unreadable.
	RootLost bool `json:"root_lost,omitempty"`
	// RootRecovered reports that the root became readable again. It always
	// accompanies Resync, because changes during the outage were not observed.
	RootRecovered bool `json:"root_recovered,omitempty"`
	// Stats is the source's state at the moment the batch was closed.
	Stats Stats `json:"stats"`
}

// Empty reports whether the batch carries nothing a consumer must act on.
func (b Batch) Empty() bool {
	return len(b.Changes) == 0 && !b.Resync && !b.RootLost && !b.RootRecovered
}

// Observer is a source of change batches.
type Observer interface {
	// Batches returns the delivery channel. It is closed when the observer
	// stops. Exactly one consumer may read it.
	Batches() <-chan Batch
	// Track replaces the swept set with paths. A consumer calls it with what
	// it can display, which is what keeps sweep cost independent of workspace
	// size. It is a no-op for a source that does not sweep.
	Track(paths []string)
	// Stats returns the observer's current state.
	Stats() Stats
	// Close stops the observer and releases its resources.
	Close() error
}

// Options bounds an observer.
type Options struct {
	// Mode selects the mechanism. [config.ModeAuto] resolves against the
	// probed filesystem.
	Mode config.Mode
	// Interval is how often the sweep runs and how often root liveness is
	// probed.
	Interval time.Duration
	// Window is how long changes accumulate before a batch is closed.
	// Coalescing over a window is what turns a write burst into one batch.
	Window time.Duration
	// SweepBudget caps the filesystem operations one sweep cycle performs. A
	// tracked set larger than the budget is swept across several cycles,
	// round-robin, so a large set slows detection rather than stalling the
	// process.
	SweepBudget int
	// MaxBatch caps the changes one batch carries. Beyond it the batch is
	// truncated and a resync is requested.
	MaxBatch int
	// MaxQueue caps the changes held between discovery and the close of a
	// window. Beyond it a change is lost, which is announced as a resync
	// rather than absorbed: a queue that grew without bound would trade a
	// reported gap for an unreported memory ceiling.
	MaxQueue int
	// MaxWatches caps kernel watches. Beyond it directories are swept.
	MaxWatches int
	// DedupTTL is how long a change reported by one source suppresses the same
	// change from another.
	DedupTTL time.Duration
	// RootRetryMin and RootRetryMax bound the backoff between attempts to
	// reopen a lost root.
	RootRetryMin time.Duration
	RootRetryMax time.Duration
}

// DefaultOptions returns the options an observer uses when a caller supplies
// none. The command line resolves these from the configuration table; they are
// repeated here so the package is usable without one.
func DefaultOptions() Options {
	return Options{
		Mode:         config.ModeAuto,
		Interval:     2 * time.Second,
		Window:       50 * time.Millisecond,
		SweepBudget:  512,
		MaxBatch:     4096,
		MaxQueue:     8192,
		MaxWatches:   8192,
		DedupTTL:     2 * time.Second,
		RootRetryMin: time.Second,
		RootRetryMax: 30 * time.Second,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Interval <= 0 {
		o.Interval = d.Interval
	}
	if o.Window <= 0 {
		o.Window = d.Window
	}
	if o.SweepBudget <= 0 {
		o.SweepBudget = d.SweepBudget
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = d.MaxBatch
	}
	if o.MaxQueue <= 0 {
		o.MaxQueue = d.MaxQueue
	}
	if o.MaxWatches <= 0 {
		o.MaxWatches = d.MaxWatches
	}
	if o.DedupTTL <= 0 {
		o.DedupTTL = d.DedupTTL
	}
	if o.RootRetryMin <= 0 {
		o.RootRetryMin = d.RootRetryMin
	}
	if o.RootRetryMax < o.RootRetryMin {
		o.RootRetryMax = max(d.RootRetryMax, o.RootRetryMin)
	}
	return o
}

// sortChanges orders changes by path then operation, so a batch is comparable
// and a golden test over a batch is stable.
func sortChanges(cs []Change) {
	slices.SortFunc(cs, func(a, b Change) int {
		if n := strings.Compare(a.Path, b.Path); n != 0 {
			return n
		}
		return cmp.Compare(a.Op, b.Op)
	})
}
