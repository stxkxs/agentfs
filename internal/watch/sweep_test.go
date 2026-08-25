package watch

import (
	"errors"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/fsx"
)

func collect(emit *[]Change) func(Change) {
	return func(c Change) { *emit = append(*emit, c) }
}

func ignore(error) {}

// The first pass establishes what exists. Reporting it as change would announce
// a storm that did not happen, and on the workspace class the sweep exists for
// that storm is the first thing an operator would see.
func TestSweepFirstPassEmitsNoChanges(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(`{"status":"running"}`)},
		"agent-b/state.json": {Data: []byte(`{"status":"idle"}`)},
	}
	s := newSweepSource(fsx.New("ws", base))

	var got []Change
	s.seed([]string{".", "agent-a", "agent-b"}, ignore)
	s.sweep([]string{".", "agent-a", "agent-b"}, 100, collect(&got), ignore)

	if len(got) != 0 {
		t.Fatalf("the first sweep after seeding reported %v", got)
	}
}

// A write by another client of a network export raises no kernel event. The
// sweep is what makes it observable.
func TestSweepDetectsAChangeNoKernelEventReported(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"agent-a/state.json": {Data: []byte(`{"status":"running"}`)}}
	s := newSweepSource(fsx.New("ws", base))
	s.seed([]string{"agent-a"}, ignore)

	base["agent-a/state.json"] = &fstest.MapFile{Data: []byte(`{"status":"done","problem":"x"}`)}

	var got []Change
	s.sweep([]string{"agent-a"}, 100, collect(&got), ignore)
	if len(got) != 1 || got[0].Op != OpModify || got[0].Path != "agent-a/state.json" {
		t.Fatalf("sweep reported %v, want one modify of agent-a/state.json", got)
	}
}

// Filesystems report modification times as coarsely as one second, so an agent
// rewriting its status within a second at the same length is invisible to a
// size-and-time comparison.
func TestDigestDetectsASameSizeSameTimeRewrite(t *testing.T) {
	t.Parallel()
	stamp := time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)
	base := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(`{"status":"running"}`), ModTime: stamp},
	}
	s := newSweepSource(fsx.New("ws", base))
	s.seed([]string{"agent-a"}, ignore)

	// Same length, same modification time, different content.
	base["agent-a/state.json"] = &fstest.MapFile{Data: []byte(`{"status":"blocked"}`), ModTime: stamp}

	var got []Change
	s.sweep([]string{"agent-a"}, 100, collect(&got), ignore)
	if len(got) != 1 {
		t.Fatalf("a same-size same-time rewrite reported %v, want one modify", got)
	}
}

func TestSweepDetectsCreationAndRemoval(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"agent-a/state.json": {Data: []byte(`{}`)}}
	s := newSweepSource(fsx.New("ws", base))
	s.seed([]string{"agent-a"}, ignore)

	base["agent-a/result.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	delete(base, "agent-a/state.json")

	var got []Change
	s.sweep([]string{"agent-a"}, 100, collect(&got), ignore)
	sortChanges(got)

	want := []Change{
		{Path: "agent-a/result.json", Op: OpCreate},
		{Path: "agent-a/state.json", Op: OpRemove},
	}
	if len(got) != 2 {
		t.Fatalf("sweep reported %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Path != want[i].Path || got[i].Op != want[i].Op {
			t.Errorf("change %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// Sweep cost is a function of the tracked set, which the consumer binds to what
// it can display.
func TestSweepCostIsIndependentOfWorkspaceSize(t *testing.T) {
	t.Parallel()
	small := fstest.MapFS{"a/x.json": {Data: []byte(`{}`)}}
	large := fstest.MapFS{"a/x.json": {Data: []byte(`{}`)}}
	for i := range 20000 {
		large[string(rune('b'+i%20))+"/"+itoa(i)+".json"] = &fstest.MapFile{Data: []byte(`{}`)}
	}

	costOf := func(base fstest.MapFS) uint64 {
		c := fsx.NewCounting(base)
		s := newSweepSource(fsx.New("ws", c))
		s.seed([]string{"a"}, ignore)
		c.Reset()
		return s.sweep([]string{"a"}, 100, func(Change) {}, ignore)
	}

	if a, b := costOf(small), costOf(large); a != b {
		t.Fatalf("sweeping the same tracked set cost %d ops in a small workspace and %d in a large one", a, b)
	}
}

func TestSweepBudgetBoundsOneCycle(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{}
	dirs := make([]string, 0, 20)
	for i := range 20 {
		d := "d" + itoa(i)
		base[d+"/f.json"] = &fstest.MapFile{Data: []byte(`{}`)}
		dirs = append(dirs, d)
	}
	s := newSweepSource(fsx.New("ws", base))
	s.seed(dirs, ignore)

	if ops := s.sweep(dirs, 5, func(Change) {}, ignore); ops > 5 {
		t.Fatalf("one cycle spent %d operations against a budget of 5", ops)
	}
}

func TestSweepReportsRootLossDistinctly(t *testing.T) {
	t.Parallel()
	gone := errors.New("stale NFS file handle")
	base := fstest.MapFS{"agent-a/state.json": {Data: []byte(`{}`)}}
	faulty := fsx.NewFaulty(base, fsx.Fault{Path: ".", Op: fsx.OpReadDir, Err: gone})
	s := newSweepSource(fsx.New("ws", faulty))

	var failures []error
	s.sweep([]string{"."}, 10, func(Change) {}, func(err error) { failures = append(failures, err) })

	if len(failures) == 0 {
		t.Fatal("a root read failure was not reported")
	}
	if !errors.Is(failures[0], fsx.ErrRootLost) {
		t.Fatalf("root failure reported as %v, want ErrRootLost", failures[0])
	}
}

func TestSweepReportsAVanishedDirectoryAsRemovals(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"agent-a/state.json": {Data: []byte(`{}`)}}
	root := fsx.New("ws", base)
	s := newSweepSource(root)
	s.seed([]string{"agent-a"}, ignore)

	delete(base, "agent-a/state.json")

	var got []Change
	s.sweep([]string{"agent-a"}, 10, collect(&got), ignore)
	if len(got) != 1 || got[0].Op != OpRemove {
		t.Fatalf("a vanished directory reported %v, want a removal", got)
	}
}

func TestSweepForgetsUntrackedDirectories(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"a/x.json": {Data: []byte(`{}`)}, "b/y.json": {Data: []byte(`{}`)}}
	s := newSweepSource(fsx.New("ws", base))
	s.seed([]string{"a", "b"}, ignore)

	s.forget([]string{"a"})
	s.mu.Lock()
	_, keptB := s.dirs["b"]
	_, keptA := s.dirs["a"]
	s.mu.Unlock()

	if keptB {
		t.Error("an untracked directory was kept in memory")
	}
	if !keptA {
		t.Error("a tracked directory was forgotten")
	}
}

func TestPermissionDeniedOnOneDirectoryDoesNotStopTheCycle(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"a/x.json": {Data: []byte(`{}`)}, "b/y.json": {Data: []byte(`{}`)}}
	faulty := fsx.NewFaulty(base, fsx.Fault{Path: "a", Op: fsx.OpReadDir, Err: fs.ErrPermission})
	s := newSweepSource(fsx.New("ws", faulty))
	s.seed([]string{"a", "b"}, ignore)

	base["b/z.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	var got []Change
	s.sweep([]string{"a", "b"}, 10, collect(&got), ignore)

	if !slices.ContainsFunc(got, func(c Change) bool { return c.Path == "b/z.json" }) {
		t.Fatalf("a permission failure on one directory hid a change in another: %v", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
