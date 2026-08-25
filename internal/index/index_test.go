package index_test

import (
	"fmt"
	"io/fs"
	"math/rand/v2"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/watch"
)

func workspace() fstest.MapFS {
	return fstest.MapFS{
		"agent-a/state.json":            {Data: []byte(`{"status":"running"}`)},
		"agent-a/logs/run.log":          {Data: []byte("line\n")},
		"agent-a/memory/context.json":   {Data: []byte(`{}`)},
		"agent-a/runs/run-001/out.json": {Data: []byte(`{}`)},
		"agent-b/state.json":            {Data: []byte(`{"status":"idle"}`)},
		"agent-b/artifacts/draft.md":    {Data: []byte("# draft\n")},
	}
}

func newIndex(t *testing.T, fsys fsx.FS) (*index.Index, *fsx.Counting) {
	t.Helper()
	c := fsx.NewCounting(fsys)
	ix := index.New(fsx.New("ws", c), index.Limits{})
	return ix, c
}

// Startup cost must not depend on workspace size: the root is read, nothing
// below it is.
func TestNewReadsOnlyTheRoot(t *testing.T) {
	t.Parallel()
	_, c := newIndex(t, workspace())
	if got := c.Ops().ReadDir; got != 1 {
		t.Fatalf("startup performed %d directory reads, want 1", got)
	}
}

func TestNewReadCountIsIndependentOfWorkspaceSize(t *testing.T) {
	t.Parallel()
	big := fstest.MapFS{}
	for i := range 5000 {
		big[fmt.Sprintf("agent-%04d/state.json", i)] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	_, c := newIndex(t, big)
	if got := c.Ops().ReadDir; got != 1 {
		t.Fatalf("startup over 5000 agents performed %d directory reads, want 1", got)
	}
}

func TestExpandReadsExactlyOneDirectory(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	c.Reset()

	ix.Expand("agent-a")
	if got := c.Ops().ReadDir; got != 0 {
		t.Fatalf("Expand itself performed %d directory reads, want 0: a read belongs off the caller's update path", got)
	}
	if got := ix.Pending(); len(got) != 1 || got[0] != "agent-a" {
		t.Fatalf("Pending = %v, want [agent-a]", got)
	}
	ix.Drain()
	if got := c.Ops().ReadDir; got != 1 {
		t.Fatalf("draining one expansion performed %d directory reads, want 1", got)
	}
	if _, ok := ix.Lookup("agent-a/logs"); !ok {
		t.Fatal("Expand did not load the directory's members")
	}
	if _, ok := ix.Lookup("agent-a/logs/run.log"); ok {
		t.Fatal("Expand loaded a grandchild it was not asked for")
	}
}

func TestCollapseKeepsWhatWasRead(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	ix.Expand("agent-a")
	ix.Drain()
	ix.Collapse("agent-a")
	c.Reset()

	ix.Expand("agent-a")
	ix.Drain()
	if got := c.Ops().ReadDir; got != 0 {
		t.Fatalf("reopening a directory performed %d reads, want 0", got)
	}
}

// One agent writing one file must cost one directory read, whatever else the
// workspace holds.
func TestApplyIsBoundedByTheBatch(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	ix.Expand("agent-a")
	ix.Expand("agent-b")
	ix.Drain()
	c.Reset()

	ix.Apply(watch.Batch{
		At:      time.Now(),
		Changes: []watch.Change{{Path: "agent-a/state.json", Op: watch.OpModify}},
	})
	ix.Drain()
	if got := c.Ops().ReadDir; got != 1 {
		t.Fatalf("applying one change performed %d directory reads, want 1", got)
	}
}

// A write burst on one file coalesces into one batch, and one batch naming one
// directory costs one read of it.
func TestBurstOnOneDirectoryCostsOneRead(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	ix.Expand("agent-a")
	ix.Drain()
	c.Reset()

	changes := make([]watch.Change, 0, 50)
	for i := range 50 {
		changes = append(changes, watch.Change{
			Path: "agent-a/state.json",
			Op:   watch.OpModify,
		})
		_ = i
	}
	ix.Apply(watch.Batch{At: time.Now(), Changes: changes})
	ix.Drain()

	if got := c.Ops().ReadDir; got != 1 {
		t.Fatalf("a 50-change burst performed %d directory reads, want 1", got)
	}
}

func TestApplyIgnoresChangesUnderUnloadedDirectories(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	c.Reset()

	ix.Apply(watch.Batch{
		At:      time.Now(),
		Changes: []watch.Change{{Path: "agent-a/logs/run.log", Op: watch.OpModify}},
	})
	ix.Drain()
	if got := c.Ops().ReadDir; got != 0 {
		t.Fatalf("a change below an unopened directory performed %d reads, want 0", got)
	}
}

// The incremental path and the rebuild path must agree, or the operator's view
// silently diverges from the filesystem.
func TestApplyEqualsFullRebuild(t *testing.T) {
	t.Parallel()
	rng := rand.New(rng64(1))

	for round := range 200 {
		base := workspace()
		ix, _ := newIndex(t, base)
		ix.Expand("agent-a")
		ix.Expand("agent-b")
		ix.Drain()

		var changes []watch.Change
		for range 1 + rng.IntN(4) {
			switch rng.IntN(3) {
			case 0:
				name := fmt.Sprintf("agent-a/new-%d.json", rng.IntN(5))
				base[name] = &fstest.MapFile{Data: []byte(`{}`)}
				changes = append(changes, watch.Change{Path: name, Op: watch.OpCreate})
			case 1:
				delete(base, "agent-b/state.json")
				changes = append(changes, watch.Change{Path: "agent-b/state.json", Op: watch.OpRemove})
			case 2:
				base["agent-a/state.json"] = &fstest.MapFile{Data: []byte(`{"status":"done"}`)}
				changes = append(changes, watch.Change{Path: "agent-a/state.json", Op: watch.OpModify})
			}
		}

		ix.Apply(watch.Batch{At: time.Now(), Changes: changes})
		ix.Drain()
		incremental := paths(ix.Rows())

		fresh := index.New(fsx.New("ws", base), index.Limits{})
		fresh.Expand("agent-a")
		fresh.Expand("agent-b")
		fresh.Drain()
		full := paths(fresh.Rows())

		if !slices.Equal(incremental, full) {
			t.Fatalf("round %d: incremental %v, rebuild %v", round, incremental, full)
		}
	}
}

func TestResyncRebuildsAndPreservesOpenDirectories(t *testing.T) {
	t.Parallel()
	base := workspace()
	ix, _ := newIndex(t, base)
	ix.Expand("agent-a")
	ix.Drain()
	ix.Expand("agent-a/logs")
	ix.Drain()

	base["agent-a/logs/second.log"] = &fstest.MapFile{Data: []byte("x")}
	ix.Apply(watch.Batch{At: time.Now(), Resync: true})
	ix.Drain()

	if !slices.Contains(paths(ix.Rows()), "agent-a/logs/second.log") {
		t.Fatalf("resync did not pick up the new file: %v", paths(ix.Rows()))
	}
	n, ok := ix.Lookup("agent-a/logs")
	if !ok || !n.Expanded() {
		t.Fatal("resync closed a directory the operator had open")
	}
}

// A rebuild reopens what was open when it started. That set describes the tree
// at one moment, so a directory closed after the rebuild must stay closed when
// its parent is reloaded.
func TestARebuildDoesNotReopenADirectoryClosedAfterIt(t *testing.T) {
	t.Parallel()
	base := workspace()
	ix, _ := newIndex(t, base)
	ix.Expand("agent-a")
	ix.Drain()
	ix.Expand("agent-a/logs")
	ix.Drain()

	ix.Apply(watch.Batch{At: time.Now(), Resync: true})
	ix.Drain()

	ix.Collapse("agent-a/logs")
	base["agent-a/note.md"] = &fstest.MapFile{Data: []byte("x")}
	ix.Apply(watch.Batch{At: time.Now(), Changes: []watch.Change{
		{Path: "agent-a/note.md", Op: watch.OpCreate},
	}})
	ix.Drain()

	n, ok := ix.Lookup("agent-a/logs")
	if !ok {
		t.Fatal("reloading agent-a lost agent-a/logs")
	}
	if n.Expanded() {
		t.Fatal("reloading agent-a reopened a directory closed after the rebuild")
	}
}

func TestSeededBatchChangesNothing(t *testing.T) {
	t.Parallel()
	ix, c := newIndex(t, workspace())
	c.Reset()
	ix.Apply(watch.Batch{Seeded: true, At: time.Now()})
	ix.Drain()
	if got := c.Ops().Total(); got != 0 {
		t.Fatalf("a seeded batch performed %d operations, want 0", got)
	}
}

// A link is a leaf whatever it points at, so a link cycle cannot be built.
func TestSymlinkIsNeverFollowed(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(`{}`)},
		"agent-a/loop":       {Mode: fs.ModeSymlink, Data: []byte("..")},
	}
	ix := index.New(fsx.New("ws", fsys), index.Limits{})
	ix.Expand("agent-a")
	ix.Drain()

	n, ok := ix.Lookup("agent-a/loop")
	if !ok {
		t.Fatal("the link was not recorded")
	}
	if !n.IsLink {
		t.Error("the link was not marked as one")
	}
	if n.IsDir {
		t.Error("the link was treated as a directory")
	}

	ix.Expand("agent-a/loop")
	ix.Drain()
	if len(ix.Rows()) > 16 {
		t.Fatalf("expanding a link produced %d rows", len(ix.Rows()))
	}
}

func TestDepthCeilingStopsDescent(t *testing.T) {
	t.Parallel()
	deep := fstest.MapFS{"a/b/c/d/e/f.json": {Data: []byte(`{}`)}}
	ix := index.New(fsx.New("ws", deep), index.Limits{MaxDepth: 2})

	ix.Expand("a")
	ix.Drain()
	ix.Expand("a/b")
	ix.Drain()
	ix.Expand("a/b/c")
	ix.Drain()
	if _, ok := ix.Lookup("a/b/c/d"); ok {
		t.Fatal("descent continued past the depth ceiling")
	}
	if ix.Stats().DepthTruncated == 0 {
		t.Fatal("the depth ceiling was reached without being reported")
	}
}

func TestEntryCeilingMarksTheDirectoryTruncated(t *testing.T) {
	t.Parallel()
	wide := fstest.MapFS{}
	for i := range 100 {
		wide[fmt.Sprintf("d/f%03d.json", i)] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	ix := index.New(fsx.New("ws", wide), index.Limits{MaxEntriesPerDir: 10})
	ix.Expand("d")
	ix.Drain()

	n, _ := ix.Lookup("d")
	if !n.Truncated {
		t.Fatal("a directory held to the cap is not marked truncated")
	}
	if got, _ := n.ChildCount(); got != 10 {
		t.Fatalf("held %d members, want 10", got)
	}
	if ix.Stats().TruncatedDirs == 0 {
		t.Fatal("the truncation was not reported in Stats")
	}
}

// The swept set follows the viewport, which is what keeps sweep cost
// independent of workspace size.
func TestVisibleDirsFollowsTheViewport(t *testing.T) {
	t.Parallel()
	ix, _ := newIndex(t, workspace())

	if got := ix.VisibleDirs(); !slices.Equal(got, []string{"."}) {
		t.Fatalf("with nothing open VisibleDirs = %v, want [.]", got)
	}

	ix.Expand("agent-a")
	ix.Drain()
	got := ix.VisibleDirs()
	if !slices.Contains(got, "agent-a") {
		t.Fatalf("VisibleDirs = %v, want it to include agent-a", got)
	}
	if slices.Contains(got, "agent-a/logs") {
		t.Fatalf("VisibleDirs = %v, want it to exclude an unopened directory", got)
	}

	ix.Collapse("agent-a")
	if slices.Contains(ix.VisibleDirs(), "agent-a") {
		t.Fatal("VisibleDirs kept a collapsed directory")
	}
}

func TestUnreadableDirectoryIsRecordedNotFatal(t *testing.T) {
	t.Parallel()
	base := workspace()
	faulty := fsx.NewFaulty(base, fsx.Fault{Path: "agent-a", Op: fsx.OpReadDir, Err: fs.ErrPermission})
	ix := index.New(fsx.New("ws", faulty), index.Limits{})
	ix.Expand("agent-a")
	ix.Drain()

	n, _ := ix.Lookup("agent-a")
	if n.Unreadable == nil {
		t.Fatal("an unreadable directory carries no error")
	}
	if ix.Stats().UnreadableDirs == 0 {
		t.Fatal("the unreadable directory was not counted")
	}
	if _, ok := ix.Lookup("agent-b"); !ok {
		t.Fatal("one unreadable directory prevented the rest of the tree being read")
	}
}

func paths(rows []index.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Node.Path)
	}
	return out
}

func rng64(seed uint64) *rand.PCG { return rand.NewPCG(seed, seed*2+1) }
