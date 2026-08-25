package watch

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/stxkxs/agentfs/internal/fsx"
)

// maxWalkDepth bounds directory enumeration. A workspace deeper than this is
// watched to the limit and swept below it, which is a slower observation rather
// than a refusal to start.
const maxWalkDepth = 32

// notifySource observes kernel change notification.
//
// Establishing a watch per directory is what the kernel interface requires, and
// the count is finite: Linux enforces a per-user inotify limit that a large
// workspace exhausts. Exhausting it degrades to sweeping the unwatched
// directories, reported through [Stats.WatchesRefused], rather than failing to
// start.
type notifySource struct {
	root       *fsx.Root
	realPath   string
	maxWatches int

	mu       sync.Mutex
	fsw      *fsnotify.Watcher
	watched  map[string]struct{}
	refused  uint64
	emit     func(Change)
	fail     func(error)
	closed   bool
	stopOnce sync.Once
	done     chan struct{}
	wg       sync.WaitGroup
}

func newNotifySource(root *fsx.Root, maxWatches int) *notifySource {
	return &notifySource{
		root:       root,
		realPath:   root.Name(),
		maxWatches: maxWatches,
		watched:    make(map[string]struct{}),
		done:       make(chan struct{}),
	}
}

func (n *notifySource) start(emit func(Change), fail func(error)) (watches int, refused uint64, err error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return 0, 0, fmt.Errorf("create watcher: %w", err)
	}

	n.mu.Lock()
	n.fsw, n.emit, n.fail = fsw, emit, fail
	n.mu.Unlock()

	n.addTree(".", 0)

	n.wg.Add(1)
	go n.loop()

	n.mu.Lock()
	watches, refused = len(n.watched), n.refused
	n.mu.Unlock()
	return watches, refused, nil
}

// addTree establishes watches over dir and its descendants, stopping at the
// watch budget and at [maxWalkDepth]. It walks through the confined filesystem,
// so a symlink out of the workspace is never enumerated and therefore never
// watched.
func (n *notifySource) addTree(dir string, depth int) {
	if depth > maxWalkDepth {
		return
	}
	n.mu.Lock()
	if n.closed || n.fsw == nil {
		n.mu.Unlock()
		return
	}
	if len(n.watched) >= n.maxWatches {
		n.refused++
		n.mu.Unlock()
		return
	}
	if _, already := n.watched[dir]; !already {
		if err := n.fsw.Add(n.real(dir)); err != nil {
			n.refused++
			n.mu.Unlock()
			return
		}
		n.watched[dir] = struct{}{}
	}
	fsys := n.root.FS()
	n.mu.Unlock()

	if fsys == nil {
		return
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		n.report(err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n.addTree(path.Join(dir, e.Name()), depth+1)
	}
}

func (n *notifySource) real(rel string) string {
	if rel == "." {
		return n.realPath
	}
	return filepath.Join(n.realPath, filepath.FromSlash(rel))
}

func (n *notifySource) report(err error) {
	n.mu.Lock()
	fail := n.fail
	n.mu.Unlock()
	if fail != nil {
		fail(err)
	}
}

func (n *notifySource) loop() {
	defer n.wg.Done()
	n.mu.Lock()
	fsw := n.fsw
	n.mu.Unlock()
	if fsw == nil {
		return
	}

	for {
		select {
		case <-n.done:
			return
		case ev, ok := <-fsw.Events:
			if !ok {
				return
			}
			n.translate(ev)
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			// A watcher error is a gap in what the kernel reported. Discarding
			// it makes the gap invisible; the engine turns it into a counter
			// the status line renders.
			n.report(fmt.Errorf("kernel notification: %w", err))
		}
	}
}

func (n *notifySource) translate(ev fsnotify.Event) {
	rel, err := filepath.Rel(n.realPath, ev.Name)
	if err != nil {
		return
	}
	clean, ok := fsx.Clean(filepath.ToSlash(rel))
	if !ok {
		// A path that resolves outside the workspace is not observable through
		// the confined root, so reporting it would name a file no other layer
		// can read.
		return
	}

	op, interesting := translateOp(ev.Op)
	if !interesting {
		return
	}

	isDir := false
	if fsys := n.root.FS(); fsys != nil {
		if info, statErr := fsys.Stat(clean); statErr == nil {
			isDir = info.IsDir()
		}
	}

	n.mu.Lock()
	emit := n.emit
	n.mu.Unlock()
	if emit != nil {
		emit(Change{Path: clean, Op: op, IsDir: isDir, At: time.Now()})
	}

	if isDir && op == OpCreate {
		n.addTree(clean, 0)
	}
}

func translateOp(op fsnotify.Op) (Op, bool) {
	switch {
	case op.Has(fsnotify.Create):
		return OpCreate, true
	case op.Has(fsnotify.Write):
		return OpModify, true
	case op.Has(fsnotify.Remove):
		return OpRemove, true
	case op.Has(fsnotify.Rename):
		return OpRename, true
	case op.Has(fsnotify.Chmod):
		return OpModify, true
	default:
		return OpModify, false
	}
}

func (n *notifySource) sweep([]string, int, func(Change), func(error)) uint64 { return 0 }

func (n *notifySource) seed([]string, func(error)) {}

func (n *notifySource) close() error {
	var err error
	n.stopOnce.Do(func() {
		n.mu.Lock()
		n.closed = true
		fsw := n.fsw
		n.mu.Unlock()
		close(n.done)
		if fsw != nil {
			err = fsw.Close()
		}
		n.wg.Wait()
	})
	if err != nil && !errors.Is(err, fs.ErrClosed) {
		return fmt.Errorf("close watcher: %w", err)
	}
	return nil
}

// watchCounts reports what the source holds, which changes as directories are
// created and watched after startup.
func (n *notifySource) watchCounts() (watches int, refused uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.watched), n.refused
}
