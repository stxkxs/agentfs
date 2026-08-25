package cli

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/report"
	"github.com/stxkxs/agentfs/internal/watch"
)

// The conditions a status record reports, which is the closed vocabulary of
// [statusRecord.Event]. Each names one fact, so a batch that both recovers the
// root and demands a resync writes one record for each rather than one record
// a consumer has to decompose.
const (
	// eventOpened is the first record of a stream: what it is watching, and
	// with which mechanism.
	eventOpened = "opened"
	// eventResync tells the consumer that the changes it has been given are
	// not a complete account, and it must rebuild from the filesystem.
	eventResync = "resync"
	// eventRootLost reports that the workspace root became unreadable, so no
	// change is being observed until it returns.
	eventRootLost = "root_lost"
	// eventRootRecovered reports that the root became readable again. A
	// resync always follows, because changes during the outage were not
	// observed.
	eventRootRecovered = "root_recovered"
)

// changeRecord is the payload of a change record: which path changed and what
// happened to it. The operation is a word rather than a number, so adding one
// cannot silently change what an existing value means.
type changeRecord struct {
	// Path is workspace-relative and slash-separated.
	Path string `json:"path"`
	// Op is one of create, modify, remove or rename.
	Op string `json:"op"`
	// IsDir reports whether the path is a directory, as far as the source
	// could tell. A removal cannot be stated, so it reports false.
	IsDir bool `json:"is_dir"`
}

// statusRecord is the payload of a status record: what the stream is watching,
// and every way it can be falling behind.
//
// The counters are cumulative for the producer's lifetime. A consumer reads
// them to size loss rather than to detect it: a resync is announced by an
// event of its own.
type statusRecord struct {
	// Event is one of the event constants.
	Event string `json:"event"`
	// Watching is the workspace root.
	Watching string `json:"watching"`
	// Mode is the resolved detection mechanism: notify, sweep or hybrid.
	Mode string `json:"mode"`
	// Filesystem is what the root was probed as.
	Filesystem string `json:"filesystem"`
	// Tracked is the number of directories the sweep covers, and zero under a
	// mode that does not sweep.
	Tracked int `json:"tracked"`
	// Watches is the number of kernel watches held.
	Watches int `json:"watches"`
	// WatchesRefused counts directories the budget or the kernel refused a
	// watch for.
	WatchesRefused uint64 `json:"watches_refused"`
	// Coalesced counts changes folded into another change on the same path.
	Coalesced uint64 `json:"coalesced"`
	// Deduplicated counts changes discarded because another source had
	// already reported them.
	Deduplicated uint64 `json:"deduplicated"`
	// Dropped counts changes lost to a batch ceiling. Every drop is
	// accompanied by a resync.
	Dropped uint64 `json:"dropped"`
	// Errors counts source faults observed, each of which is also an error
	// record.
	Errors uint64 `json:"errors"`
}

// errorRecord is the payload of an error record: a fault the producer met and
// could not resolve, such as a tracked directory it could not read.
type errorRecord struct {
	// Message is what the source reported.
	Message string `json:"message"`
	// Count is how many faults this producer has observed, the reported one
	// included.
	Count uint64 `json:"count"`
}

// streamer turns observed batches into NDJSON records.
//
// It owns the tracked set as well, because the two are the same question: the
// directories the sweep covers are the directories a consumer is told about,
// so a stream that reported change from a set it did not maintain would go
// quiet on exactly the filesystems the sweep exists for.
type streamer struct {
	out    *report.Stream
	root   *fsx.Root
	obs    watch.Observer
	now    func() time.Time
	sweeps bool
	// entries caps the members of one directory the tracked set is built
	// from, so a workspace that declares its own number of agents cannot
	// choose how much work one refresh costs.
	entries int
	tracked map[string]struct{}
	// reported is the number of source faults already written as records, so
	// each fault is written once rather than once per batch that carries the
	// counter.
	reported uint64
}

// stream writes the workspace's change stream to standard output until the
// context is cancelled, the observer stops, or a write fails.
//
// It returns [report.CodeInterrupted] on cancellation, which is what SIGINT
// reaches this loop as. A reader that closes the pipe is a decision that it has
// enough rather than a fault, so the command keeps the code it had reached.
func stream(ctx context.Context, env Env, opts Options, root *fsx.Root, obs watch.Observer) report.Code {
	s := &streamer{
		out:     report.NewStream(env.Stdout),
		root:    root,
		obs:     obs,
		now:     env.now,
		entries: opts.Config.MaxEntriesPerDir,
		tracked: make(map[string]struct{}),
	}
	stats := obs.Stats()
	s.sweeps = sweeps(stats.Mode)
	stats.Tracked = s.track()

	if err := s.status(eventOpened, env.now(), stats); err != nil {
		return writeErr(env, err, report.CodeOK)
	}

	// A source fault raises a counter rather than a batch, so a stream that
	// only read the counter off a delivered batch would stay silent about a
	// directory it cannot read for as long as nothing else changed. The
	// observer is polled at the cadence it sweeps at.
	poll := time.NewTicker(opts.Config.SweepInterval)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return report.CodeInterrupted
		case <-poll.C:
			if err := s.faults(s.now(), obs.Stats()); err != nil {
				return writeErr(env, err, report.CodeOK)
			}
		case b, ok := <-obs.Batches():
			if !ok {
				// The observer stopped, so there is nothing further to
				// report and the stream ends where its producer did.
				return report.CodeOK
			}
			if err := s.batch(b); err != nil {
				return writeErr(env, err, report.CodeOK)
			}
			if s.restack(b) {
				s.track()
			}
		}
	}
}

// batch writes every record one batch produces, condition first: a consumer
// that learns of a resync before it reads the changes knows to rebuild rather
// than to apply them.
func (s *streamer) batch(b watch.Batch) error {
	if b.RootLost {
		if err := s.status(eventRootLost, b.At, b.Stats); err != nil {
			return err
		}
	}
	if b.RootRecovered {
		if err := s.status(eventRootRecovered, b.At, b.Stats); err != nil {
			return err
		}
	}
	if b.Resync {
		if err := s.status(eventResync, b.At, b.Stats); err != nil {
			return err
		}
	}
	if err := s.faults(b.At, b.Stats); err != nil {
		return err
	}
	for _, c := range b.Changes {
		payload := changeRecord{Path: c.Path, Op: c.Op.String(), IsDir: c.IsDir}
		if err := s.out.Write(report.RecordChange, c.DedupKey(), c.At, payload); err != nil {
			return err
		}
	}
	return nil
}

// faults writes an error record for a source fault the counter has risen past.
// The counter is cumulative, so a stream that wrote a record for every reading
// of it would report one fault for as long as the count stood.
func (s *streamer) faults(at time.Time, st watch.Stats) error {
	if st.Errors <= s.reported {
		return nil
	}
	s.reported = st.Errors
	payload := errorRecord{Message: st.LastError, Count: st.Errors}
	return s.out.Write(report.RecordError, eventKey(at, st.LastError), at, payload)
}

// status writes one status record.
func (s *streamer) status(event string, at time.Time, st watch.Stats) error {
	return s.out.Write(report.RecordStatus, eventKey(at, event), at, statusRecord{
		Event:          event,
		Watching:       s.root.Name(),
		Mode:           st.Mode.String(),
		Filesystem:     st.Filesystem.Type,
		Tracked:        st.Tracked,
		Watches:        st.Watches,
		WatchesRefused: st.WatchesRefused,
		Coalesced:      st.Coalesced,
		Deduplicated:   st.Deduplicated,
		Dropped:        st.Dropped,
		Errors:         st.Errors,
	})
}

// eventKey identifies an event that is not a change by the instant it happened
// and what it was, which is what lets a consumer reading two producers of one
// workspace discard the repeat.
func eventKey(at time.Time, name string) string {
	return at.Format(time.RFC3339Nano) + "\x00" + name
}

// restack reports whether the batch can have changed the set of directories
// the sweep covers: a directory appeared, one that was tracked went away, or
// the account is being rebuilt from the filesystem.
//
// A removal cannot state that it was a directory, so a tracked path that
// vanishes is recognized from the set rather than from the change. Leaving it
// in would spend a read on it every cycle and report the failure every cycle.
func (s *streamer) restack(b watch.Batch) bool {
	if !s.sweeps {
		return false
	}
	if b.Seeded || b.Resync || b.RootRecovered {
		return true
	}
	for _, c := range b.Changes {
		if c.IsDir {
			return true
		}
		if _, held := s.tracked[c.Path]; held {
			return true
		}
	}
	return false
}

// track hands the observer the directories to sweep and returns how many there
// are. It is a no-op under a mode that does not sweep, where the tracked set
// buys nothing and reading the workspace to compute it would cost for nothing.
func (s *streamer) track() int {
	if !s.sweeps {
		return 0
	}
	dirs := sweptDirs(s.root, s.entries)
	s.tracked = make(map[string]struct{}, len(dirs))
	for _, d := range dirs {
		s.tracked[d] = struct{}{}
	}
	s.obs.Track(dirs)
	return len(dirs)
}

// sweptDirs returns the directories a headless stream sweeps: the workspace
// root, every agent directory below it, and the conventional subdirectories
// those declare.
//
// A state document is written in one of those, so this is the set a consumer
// of the stream is told about. It costs one directory read per agent rather
// than a walk, which is what keeps the cost a function of the agents in a
// workspace rather than of the files in it — a change deeper than a
// conventional subdirectory is observed by kernel notification and, on a
// filesystem where notification is incomplete, when the directory holding it
// is next read.
//
// entries is the ceiling on the agent directories read, which is
// [config.Config.MaxEntriesPerDir]: the number of directories under a root is
// the workspace's to choose, and one refresh is bounded work.
func sweptDirs(root *fsx.Root, entries int) []string {
	fsys := root.FS()
	if fsys == nil {
		return nil
	}
	out := []string{"."}
	members, err := fsys.ReadDir(".")
	if err != nil {
		return out
	}
	for _, e := range members {
		if len(out) > entries {
			break
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
		below, err := fsys.ReadDir(e.Name())
		if err != nil {
			continue
		}
		for _, m := range below {
			if m.IsDir() && agentstate.IsSignalDir(m.Name()) {
				out = append(out, path.Join(e.Name(), m.Name()))
			}
		}
	}
	return out
}

// sweeps reports whether a resolved mode reads the tracked set.
func sweeps(m config.Mode) bool { return m == config.ModeSweep || m == config.ModeHybrid }
