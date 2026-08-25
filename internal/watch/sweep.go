package watch

import (
	"hash/fnv"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// digestLimit caps the state documents the sweep hashes. Filesystems report
// modification times at a granularity as coarse as a second, so a document
// rewritten within one second at the same length is invisible to a
// size-and-time comparison — which is exactly the shape of an agent updating
// its status. Hashing the small documents that carry status closes that gap;
// hashing everything would make sweep cost proportional to workspace bytes.
const digestLimit = 64 << 10

// entry is what the sweep remembers about one directory member.
type entry struct {
	size   int64
	mod    time.Time
	isDir  bool
	digest uint64
}

func (e entry) sameAs(o entry) bool {
	return e.size == o.size && e.mod.Equal(o.mod) && e.isDir == o.isDir && e.digest == o.digest
}

// sweepSource discovers change by comparing successive readings of the tracked
// directories. It is what makes agentfs report a write made by another client
// of a network export, which raises no event on this kernel.
type sweepSource struct {
	root *fsx.Root

	mu    sync.Mutex
	dirs  map[string]map[string]entry
	clock func() time.Time
}

func newSweepSource(root *fsx.Root) *sweepSource {
	return &sweepSource{root: root, dirs: make(map[string]map[string]entry), clock: time.Now}
}

func (s *sweepSource) start(func(Change), func(error)) (watches int, refused uint64, err error) {
	return 0, 0, nil
}

func (s *sweepSource) close() error { return nil }

// seed records what exists without reporting it, so the first pass does not
// announce every file in the workspace as newly created.
func (s *sweepSource) seed(tracked []string, fail func(error)) {
	s.scan(tracked, len(tracked)+1, nil, fail)
}

func (s *sweepSource) sweep(tracked []string, budget int, emit func(Change), fail func(error)) uint64 {
	return s.scan(tracked, budget, emit, fail)
}

// scan reads each tracked directory once and reports how its members differ
// from the previous reading. emit is nil during seeding.
func (s *sweepSource) scan(tracked []string, budget int, emit func(Change), fail func(error)) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	fsys := s.root.FS()
	if fsys == nil {
		fail(fsx.ErrRootLost)
		return 0
	}

	now := s.clock()
	var ops uint64
	for _, dir := range tracked {
		if int(ops) >= budget {
			break
		}
		spent, lost := s.scanDir(fsys, dir, now, emit, fail)
		ops += spent
		if lost {
			return ops
		}
	}
	return ops
}

// scanDir reads one tracked directory and reports how its members differ from
// the previous reading. emit is nil while seeding. It reports the operations it
// spent, and whether the failure it met was the root itself going away — which
// ends the cycle, because every remaining read would fail the same way.
func (s *sweepSource) scanDir(
	fsys fsx.FS, dir string, now time.Time, emit func(Change), fail func(error),
) (ops uint64, rootLost bool) {
	names, err := fsys.ReadDir(dir)
	ops++
	if err != nil {
		if isRootLoss(dir, err) {
			fail(fsx.ErrRootLost)
			return ops, true
		}
		fail(err)
		s.forgetDir(dir, now, emit)
		return ops, false
	}

	current := make(map[string]entry, len(names))
	for _, de := range names {
		e, cost := s.fingerprint(fsys, dir, de)
		ops += cost
		current[de.Name()] = e
	}

	prev, had := s.dirs[dir]
	s.dirs[dir] = current
	if had && emit != nil {
		reportDiff(dir, prev, current, now, emit)
	}
	return ops, false
}

// forgetDir reports a directory that vanished between being tracked and being
// read as removals, which is what it is rather than a failure to observe.
func (s *sweepSource) forgetDir(dir string, now time.Time, emit func(Change)) {
	if prev, had := s.dirs[dir]; had && emit != nil {
		for name := range prev {
			emit(Change{Path: path.Join(dir, name), Op: OpRemove, At: now})
		}
	}
	delete(s.dirs, dir)
}

// reportDiff emits one change per member that appeared, changed or went away.
func reportDiff(dir string, prev, current map[string]entry, now time.Time, emit func(Change)) {
	for name, cur := range current {
		old, existed := prev[name]
		switch {
		case !existed:
			emit(Change{Path: path.Join(dir, name), Op: OpCreate, IsDir: cur.isDir, At: now})
		case !old.sameAs(cur):
			emit(Change{Path: path.Join(dir, name), Op: OpModify, IsDir: cur.isDir, At: now})
		}
	}
	for name := range prev {
		if _, still := current[name]; !still {
			emit(Change{Path: path.Join(dir, name), Op: OpRemove, At: now})
		}
	}
}

// fingerprint reads what identifies a directory member, and reports the extra
// operations it spent doing so.
func (s *sweepSource) fingerprint(fsys fsx.FS, dir string, de fs.DirEntry) (e entry, ops uint64) {
	e = entry{isDir: de.IsDir()}
	info, err := de.Info()
	if err != nil {
		return e, 0
	}
	e.size = info.Size()
	e.mod = info.ModTime()

	if e.isDir || !agentstate.IsStateFile(de.Name()) || e.size > digestLimit {
		return e, 0
	}
	data, err := fsys.ReadFile(path.Join(dir, de.Name()))
	if err != nil {
		return e, 1
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	e.digest = h.Sum64()
	return e, 1
}

// forget drops remembered state for paths no longer tracked, so the sweep's
// memory follows the tracked set rather than growing with everything ever seen.
func (s *sweepSource) forget(keep []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make(map[string]struct{}, len(keep))
	for _, p := range keep {
		live[p] = struct{}{}
	}
	for dir := range s.dirs {
		if _, ok := live[dir]; !ok {
			delete(s.dirs, dir)
		}
	}
}

// isRootLoss reports whether an error reading dir means the root itself is
// gone rather than that one directory is unreadable.
func isRootLoss(dir string, err error) bool {
	return dir == "." && err != nil
}

// watchCounts reports no kernel watches: the sweep establishes none.
func (s *sweepSource) watchCounts() (watches int, refused uint64) { return 0, 0 }
