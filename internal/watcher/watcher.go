package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event represents a file system change.
type Event struct {
	Path    string
	RelPath string
	Op      Op
	Time    time.Time
	IsDir   bool
}

type Op int

const (
	Create Op = iota
	Write
	Remove
	Rename
)

func (o Op) String() string {
	switch o {
	case Create:
		return "created"
	case Write:
		return "modified"
	case Remove:
		return "removed"
	case Rename:
		return "renamed"
	default:
		return "unknown"
	}
}

// Watcher watches a directory tree for changes.
type Watcher struct {
	root    string
	fsw     *fsnotify.Watcher
	events  chan Event
	done    chan struct{}
	mu      sync.Mutex
}

// New creates a watcher rooted at the given directory.
func New(root string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:   root,
		fsw:    fsw,
		events: make(chan Event, 256),
		done:   make(chan struct{}),
	}

	if err := w.addRecursive(root); err != nil {
		fsw.Close()
		return nil, err
	}

	go w.loop()

	return w, nil
}

// Events returns the channel of file system events.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	close(w.done)
	return w.fsw.Close()
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return w.fsw.Add(path)
		}
		return nil
	})
}

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			e := w.translate(ev)
			if e == nil {
				continue
			}
			select {
			case w.events <- *e:
			default:
				// drop if consumer is slow
			}

			// If a new directory was created, watch it too.
			if e.IsDir && e.Op == Create {
				w.mu.Lock()
				w.addRecursive(e.Path)
				w.mu.Unlock()
			}
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *Watcher) translate(ev fsnotify.Event) *Event {
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
		return nil
	}

	rel, _ := filepath.Rel(w.root, ev.Name)
	if rel == "" {
		rel = ev.Name
	}

	e := &Event{
		Path:    ev.Name,
		RelPath: rel,
		Time:    time.Now(),
	}

	switch {
	case ev.Op&fsnotify.Create != 0:
		e.Op = Create
	case ev.Op&fsnotify.Write != 0:
		e.Op = Write
	case ev.Op&fsnotify.Remove != 0:
		e.Op = Remove
	case ev.Op&fsnotify.Rename != 0:
		e.Op = Rename
	}

	info, err := os.Stat(ev.Name)
	if err == nil {
		e.IsDir = info.IsDir()
	}

	return e
}
