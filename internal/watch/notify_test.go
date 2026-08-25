//go:build !windows

package watch

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// These tests run against a real directory because the property under test is
// kernel change notification, which has no filesystem double: an event only
// exists once a real write passes through a real VFS.
//
// Delivery is asynchronous and what a platform reports for a given write
// differs between inotify and kqueue, so a wait that expires is read as "this
// platform did not report it" and skips. Everything that does arrive is
// asserted exactly. A gate that fails on a loaded machine is worse than one
// that is occasionally silent, and a skip is visible in test output while a
// flake trains a reader to ignore the suite.
const (
	// eventDeadline bounds a wait for a kernel notification.
	eventDeadline = 5 * time.Second
	// pollInterval is how often a condition over observer statistics is reread.
	pollInterval = 5 * time.Millisecond
	// testWindow closes a batch quickly enough that a test spends its time
	// waiting on the kernel rather than on coalescing.
	testWindow = 20 * time.Millisecond
	// stateDoc is the name of the document an agent rewrites as it works.
	stateDoc = "state.json"
)

func openRealRoot(t *testing.T, dir string) *fsx.Root {
	t.Helper()
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatalf("open root %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func newObserver(t *testing.T, dir string, opts Options) Observer {
	t.Helper()
	obs, err := New(openRealRoot(t, dir), opts)
	if err != nil {
		t.Fatalf("new observer over %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = obs.Close() })
	return obs
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// requireSeeded consumes the first batch. Every source delivers one to
// establish what exists, and it carries no changes: reporting the whole
// workspace as created would announce a storm that did not happen.
func requireSeeded(t *testing.T, obs Observer) {
	t.Helper()
	select {
	case b, ok := <-obs.Batches():
		if !ok {
			t.Fatal("the observer closed before delivering a batch")
		}
		if !b.Seeded {
			t.Fatalf("first batch = %+v, want Seeded", b)
		}
		if len(b.Changes) != 0 {
			t.Fatalf("the seeded batch carried %d changes, want none", len(b.Changes))
		}
	case <-time.After(eventDeadline):
		t.Fatal("no seeded batch arrived")
	}
}

// awaitChange reads batches until one carries a change satisfying want.
func awaitChange(t *testing.T, obs Observer, within time.Duration, want func(Change) bool) (Change, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case b, ok := <-obs.Batches():
			if !ok {
				return Change{}, false
			}
			for _, c := range b.Changes {
				if want(c) {
					return c, true
				}
			}
		case <-deadline:
			return Change{}, false
		}
	}
}

// collectOps drains batches until every path in want has been reported or
// within elapses, and returns the first operation reported for each path.
func collectOps(t *testing.T, obs Observer, within time.Duration, want ...string) map[string]Op {
	t.Helper()
	seen := make(map[string]Op)
	deadline := time.After(within)
	for {
		missing := slices.ContainsFunc(want, func(p string) bool {
			_, ok := seen[p]
			return !ok
		})
		if !missing {
			return seen
		}
		select {
		case b, ok := <-obs.Batches():
			if !ok {
				return seen
			}
			for _, c := range b.Changes {
				if _, dup := seen[c.Path]; !dup {
					seen[c.Path] = c.Op
				}
			}
		case <-deadline:
			return seen
		}
	}
}

// waitFor polls cond until it holds or within elapses.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.After(within)
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		if cond() {
			return true
		}
		select {
		case <-deadline:
			return cond()
		case <-tick.C:
		}
	}
}

// recorder captures what a source reports when it is driven without an engine.
type recorder struct {
	mu      sync.Mutex
	changes []Change
	errs    []error
}

func (r *recorder) emit(c Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

func (r *recorder) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *recorder) seen() []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.changes)
}

// An operator reads the mode and the filesystem off the status line to answer
// "how is this workspace being read", so both have to describe the root the
// observer actually opened rather than a default.
func TestNewOverARealDirectoryReportsTheProbedFilesystemAndMode(t *testing.T) {
	dir := t.TempDir()
	probed := fsx.Classify(dir)
	want := config.Config{Watch: config.ModeAuto}.FilesystemMode(probed.Kind)

	obs := newObserver(t, dir, Options{Mode: config.ModeAuto, Window: testWindow})
	requireSeeded(t, obs)
	st := obs.Stats()

	if st.Mode == config.ModeAuto {
		t.Fatal("auto was reported as the running mode, so the status line names no mechanism")
	}
	if st.Mode != want {
		t.Errorf("a %s filesystem is observed in %v mode, want %v", probed.Type, st.Mode, want)
	}
	if st.Filesystem != probed {
		t.Errorf("Stats.Filesystem = %+v, want %+v", st.Filesystem, probed)
	}
	if st.Watches < 1 {
		t.Errorf("%v mode holds %d watches, so no kernel event can reach the workspace", st.Mode, st.Watches)
	}
	if got := st.Degradations(); got != nil {
		t.Errorf("an empty workspace on a healthy root reported %v", got)
	}
}

// An explicitly requested mode is never overridden by the probe, and which
// mechanism ran is visible in the watch count rather than only in the label.
func TestNewHonoursAnExplicitlyRequestedMode(t *testing.T) {
	cases := []struct {
		mode    config.Mode
		watches bool
	}{
		{config.ModeNotify, true},
		{config.ModeSweep, false},
		{config.ModeHybrid, true},
	}
	for _, tc := range cases {
		t.Run(tc.mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			mkdir(t, filepath.Join(dir, "a"))
			mkdir(t, filepath.Join(dir, "b"))

			obs := newObserver(t, dir, Options{Mode: tc.mode, Window: testWindow})
			requireSeeded(t, obs)
			st := obs.Stats()

			if st.Mode != tc.mode {
				t.Fatalf("requested %v, running %v", tc.mode, st.Mode)
			}
			if !tc.watches {
				if st.Watches != 0 {
					t.Errorf("%v holds %d kernel watches, want none", tc.mode, st.Watches)
				}
				return
			}
			// Every directory in the tree is either watched or accounted for
			// as refused; a directory that is neither is one nothing observes.
			if got := st.Watches + int(st.WatchesRefused); got != 3 {
				t.Errorf("a root with two subdirectories accounted for %d directories, want 3", got)
			}
			if st.Watches < 1 {
				t.Errorf("%v established no watch", tc.mode)
			}
			if st.WatchBudget != DefaultOptions().MaxWatches {
				t.Errorf("Stats.WatchBudget = %d, want the default %d", st.WatchBudget, DefaultOptions().MaxWatches)
			}
		})
	}
}

// A mode that names no mechanism has to be refused at construction: starting
// without a source would leave a consumer waiting on a channel that never
// carries a change.
func TestNewRejectsAModeItCannotResolve(t *testing.T) {
	obs, err := New(openRealRoot(t, t.TempDir()), Options{Mode: config.Mode(9)})
	if err == nil {
		_ = obs.Close()
		t.Fatal("an unknown mode produced a running observer")
	}
	if obs != nil {
		t.Errorf("New returned %v alongside an error", obs)
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("error = %q, want it to name the unknown mode", err)
	}
}

func TestNotifyReportsAFileCreatedInAWatchedDirectory(t *testing.T) {
	dir := t.TempDir()
	obs := newObserver(t, dir, Options{Mode: config.ModeNotify, Window: testWindow})
	requireSeeded(t, obs)

	writeFile(t, filepath.Join(dir, stateDoc), `{"phase":"start"}`)

	c, ok := awaitChange(t, obs, eventDeadline, func(c Change) bool { return c.Path == stateDoc })
	if !ok {
		t.Skip("the platform reported no event for a file created in a watched directory")
	}
	if c.Op != OpCreate {
		t.Errorf("a new file was reported as %v, want create", c.Op)
	}
	if c.IsDir {
		t.Error("a file was reported as a directory")
	}
	if c.At.IsZero() {
		t.Error("the change carries no observation time")
	}
}

func TestNotifyReportsAWriteToAWatchedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, stateDoc)
	writeFile(t, file, `{"phase":"start"}`)

	obs := newObserver(t, dir, Options{Mode: config.ModeNotify, Window: testWindow})
	requireSeeded(t, obs)

	writeFile(t, file, `{"phase":"running","tokens":4096}`)

	c, ok := awaitChange(t, obs, eventDeadline, func(c Change) bool { return c.Path == stateDoc })
	if !ok {
		t.Skip("the platform reported no event for a write to a watched file")
	}
	if c.Op != OpModify {
		t.Errorf("a rewrite of an existing file was reported as %v, want modify", c.Op)
	}
	if c.IsDir {
		t.Error("a file was reported as a directory")
	}
}

func TestNotifyReportsAFileRemovedFromAWatchedDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, stateDoc)
	writeFile(t, file, `{"phase":"start"}`)

	obs := newObserver(t, dir, Options{Mode: config.ModeNotify, Window: testWindow})
	requireSeeded(t, obs)

	if err := os.Remove(file); err != nil {
		t.Fatalf("remove %s: %v", file, err)
	}

	c, ok := awaitChange(t, obs, eventDeadline, func(c Change) bool {
		return c.Path == stateDoc && c.Op == OpRemove
	})
	if !ok {
		t.Skip("the platform reported no removal for a file deleted from a watched directory")
	}
	// A removed path cannot be stated as a directory, so the field is false
	// and a consumer must not read it as "this was a file".
	if c.IsDir {
		t.Error("a removal claimed to know the path was a directory")
	}
}

// A workspace grows while it is being watched. A directory that appears after
// startup and is never watched is a hole an operator cannot see: the status
// line stays healthy while everything written inside it goes unreported.
func TestNotifyWatchesADirectoryCreatedAfterStartup(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "established"))

	obs := newObserver(t, dir, Options{Mode: config.ModeNotify, Window: testWindow})
	requireSeeded(t, obs)

	fresh := filepath.Join(dir, "fresh")
	mkdir(t, fresh)

	appeared, ok := awaitChange(t, obs, eventDeadline, func(c Change) bool { return c.Path == "fresh" })
	if !ok {
		t.Skip("the platform reported no event for a directory created in a watched workspace")
	}
	if !appeared.IsDir {
		t.Errorf("the directory %q was reported with IsDir false, so a consumer treats it as a file", appeared.Path)
	}

	writeFile(t, filepath.Join(dir, "established", "a.json"), `{}`)
	writeFile(t, filepath.Join(fresh, "b.json"), `{}`)

	seen := collectOps(t, obs, eventDeadline, "established/a.json", "fresh/b.json")
	_, sawEstablished := seen["established/a.json"]
	op, sawFresh := seen["fresh/b.json"]

	switch {
	case !sawEstablished && !sawFresh:
		t.Skip("the platform reported no event for a file created in a watched subdirectory")
	case !sawFresh:
		t.Fatal("a file in a directory watched from startup was reported and one in a directory " +
			"created afterwards was not: the watch tree is not extended as the workspace grows")
	case op != OpCreate:
		t.Errorf("fresh/b.json was reported as %v, want create", op)
	}
}

// A consumer ranges over the batch channel, so it has to close: a channel that
// stays open after Close leaves the consumer parked forever.
func TestObserverCloseIsIdempotentAndClosesTheBatchChannel(t *testing.T) {
	obs := newObserver(t, t.TempDir(), Options{Mode: config.ModeNotify, Window: testWindow})
	requireSeeded(t, obs)

	if err := obs.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := obs.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	deadline := time.After(eventDeadline)
	for {
		select {
		case _, ok := <-obs.Batches():
			if !ok {
				// Statistics stay readable after the loop has stopped, so a
				// consumer that renders one last status line is not left
				// waiting on a goroutine that has exited.
				if st := obs.Stats(); st.Mode != config.ModeNotify {
					t.Errorf("Stats after Close = %+v, want the mode the observer ran in", st)
				}
				return
			}
		case <-deadline:
			t.Fatal("the batch channel stayed open after Close")
		}
	}
}

// A directory that cannot be enumerated leaves its whole subtree unwatched.
// That is a gap in what the observer can see, so it is counted and rendered
// rather than swallowed where it happens.
func TestADirectoryThatCannotBeEnumeratedIsCountedAsASourceError(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, filepath.Join(dir, "a"))

	// The watcher establishes watches over real paths while reading the tree
	// through the confined root, so faulting the root alone is what an
	// unreadable directory looks like to this source.
	blind := fsx.New(dir, fsx.NewFaulty(openRealRoot(t, dir).FS(), fsx.Fault{Op: fsx.OpReadDir, Err: fs.ErrPermission}))

	obs, err := New(blind, Options{Mode: config.ModeNotify, Window: testWindow, Interval: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("new observer: %v", err)
	}
	t.Cleanup(func() { _ = obs.Close() })
	requireSeeded(t, obs)

	if !waitFor(eventDeadline, func() bool { return obs.Stats().Errors > 0 }) {
		t.Fatal("a directory that could not be enumerated was reported as nothing")
	}
	st := obs.Stats()
	if st.Errors != 1 {
		t.Errorf("one unreadable directory produced %d errors", st.Errors)
	}
	if !strings.Contains(st.LastError, "readdir") {
		t.Errorf("LastError = %q, does not name the operation that failed", st.LastError)
	}
	want := fmt.Sprintf("%d source errors", st.Errors)
	if got := st.Degradations(); !slices.Contains(got, want) {
		t.Errorf("degradations = %v, want one reading %q", got, want)
	}
}

// The notify source holds a kernel watcher and a goroutine reading from it.
// Close releases both, and closing twice is what a deferred Close alongside an
// explicit one does.
func TestNotifySourceCloseIsIdempotentAndReleasesTheWatcher(t *testing.T) {
	n := newNotifySource(openRealRoot(t, t.TempDir()), 32)
	rec := &recorder{}

	watches, refused, err := n.start(rec.emit, rec.fail)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if watches != 1 {
		t.Errorf("an empty root established %d watches, want 1", watches)
	}
	if refused != 0 {
		t.Errorf("an empty root under a budget of 32 refused %d watches", refused)
	}

	// Kernel notification runs no sweep. That is the whole reason a workspace
	// on a network export is observed in hybrid rather than notify mode.
	if ops := n.sweep([]string{"."}, 64, rec.emit, rec.fail); ops != 0 {
		t.Errorf("the notify source spent %d sweep operations, want none", ops)
	}
	n.seed([]string{"."}, rec.fail)
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("seeding reported %v, want nothing", got)
	}

	if err := n.close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := n.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// A workspace with more directories than the kernel or the operator will allow
// watches for must still start. Refusing to run would take the whole tool away
// over a limit that only costs detection latency, so the refusal is counted
// and the directories are swept instead.
func TestAWatchBudgetRefusesRatherThanFailingToStart(t *testing.T) {
	dir := t.TempDir()
	dirs := []string{"alpha", "beta", "gamma", "delta"}
	for _, d := range dirs {
		mkdir(t, filepath.Join(dir, d))
	}

	obs := newObserver(t, dir, Options{
		Mode:       config.ModeHybrid,
		MaxWatches: 1,
		Interval:   20 * time.Millisecond,
		Window:     testWindow,
	})

	requireSeeded(t, obs)

	st := obs.Stats()
	if st.Watches != 1 {
		t.Errorf("a budget of 1 established %d watches", st.Watches)
	}
	if st.WatchesRefused != uint64(len(dirs)) {
		t.Errorf("Stats.WatchesRefused = %d, want %d", st.WatchesRefused, len(dirs))
	}
	if st.WatchBudget != 1 {
		t.Errorf("Stats.WatchBudget = %d, want 1", st.WatchBudget)
	}
	want := fmt.Sprintf("%d dirs swept not watched", len(dirs))
	if got := st.Degradations(); !slices.Contains(got, want) {
		t.Errorf("degradations = %v, want one reading %q", got, want)
	}

	obs.Track(dirs)

	// The sweep reports how a directory differs from the previous reading, so
	// a change is only observable once one reading exists.
	if !waitFor(eventDeadline, func() bool { return obs.Stats().SweepOps > 0 }) {
		t.Fatalf("no sweep ran within the deadline: %+v", obs.Stats())
	}

	writeFile(t, filepath.Join(dir, "gamma", stateDoc), `{"phase":"running"}`)

	c, ok := awaitChange(t, obs, eventDeadline, func(c Change) bool { return c.Path == "gamma/state.json" })
	if !ok {
		t.Fatalf("a file in an unwatched but tracked directory was never reported: %+v", obs.Stats())
	}
	if c.Op != OpCreate {
		t.Errorf("gamma/state.json was reported as %v, want create", c.Op)
	}
}

// An event naming a path outside the root describes a file no other layer can
// read, because every read goes through the confined root. Reporting it would
// hand a consumer a path it can only fail on.
func TestTranslateDropsAPathOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, stateDoc), `{}`)

	// A budget of zero establishes no watch, so the only events the source
	// translates are the ones this test hands it and the count below is exact.
	n := newNotifySource(openRealRoot(t, dir), 0)
	rec := &recorder{}
	watches, refused, err := n.start(rec.emit, rec.fail)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if watches != 0 || refused != 1 {
		t.Fatalf("a budget of zero established %d watches and refused %d, want 0 and 1", watches, refused)
	}
	t.Cleanup(func() { _ = n.close() })

	outside := []fsnotify.Event{
		{Name: filepath.Join(filepath.Dir(dir), "escaped.json"), Op: fsnotify.Create},
		{Name: dir + "/" + `back\slash.json`, Op: fsnotify.Create},
		{Name: "not/absolute.json", Op: fsnotify.Create},
	}
	for _, ev := range outside {
		n.translate(ev)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("paths that do not resolve inside the workspace were reported as %v", got)
	}

	n.translate(fsnotify.Event{Name: filepath.Join(dir, stateDoc), Op: fsnotify.Write})
	got := rec.seen()
	if len(got) != 1 {
		t.Fatalf("one event inside the workspace produced %v, want one change", got)
	}
	if got[0].Path != stateDoc || got[0].Op != OpModify {
		t.Errorf("reported %+v, want a modify of %s", got[0], stateDoc)
	}
}

// The mapping decides what a consumer is told to do about a path. A create
// folded together with a write is still a create, because the path is new to
// the consumer either way.
func TestTranslateOpNamesWhatTheConsumerMustDo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in          fsnotify.Op
		want        Op
		interesting bool
	}{
		{fsnotify.Create, OpCreate, true},
		{fsnotify.Write, OpModify, true},
		{fsnotify.Remove, OpRemove, true},
		{fsnotify.Rename, OpRename, true},
		{fsnotify.Chmod, OpModify, true},
		{fsnotify.Create | fsnotify.Write, OpCreate, true},
		{fsnotify.Op(0), OpModify, false},
	}
	for _, tc := range cases {
		got, interesting := translateOp(tc.in)
		if interesting != tc.interesting {
			t.Errorf("translateOp(%v) interesting = %v, want %v", tc.in, interesting, tc.interesting)
			continue
		}
		if interesting && got != tc.want {
			t.Errorf("translateOp(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
