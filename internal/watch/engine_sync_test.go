package watch

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// stubSource lets an engine test drive discovery without a real filesystem or
// real elapsed time.
type stubSource struct {
	emit     func(Change)
	fail     func(error)
	sweeps   int
	onSweep  func(emit func(Change))
	watches  int
	refused  uint64
	startErr error
}

func (s *stubSource) start(emit func(Change), fail func(error)) (watches int, refused uint64, err error) {
	s.emit, s.fail = emit, fail
	return s.watches, s.refused, s.startErr
}

func (s *stubSource) sweep(_ []string, _ int, emit func(Change), _ func(error)) uint64 {
	s.sweeps++
	if s.onSweep != nil {
		s.onSweep(emit)
	}
	return 1
}

func (s *stubSource) seed([]string, func(error)) {}
func (s *stubSource) close() error               { return nil }

func (s *stubSource) watchCounts() (watches int, refused uint64) { return s.watches, s.refused }

func newTestEngine(t *testing.T, root *fsx.Root, src source, opts Options) *engine {
	t.Helper()
	e := newEngine(root, src, opts, Stats{Mode: config.ModeHybrid})
	if err := e.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestEngineDeliversASeededBatchFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := fsx.New("ws", fstest.MapFS{})
		e := newTestEngine(t, root, &stubSource{}, Options{})

		first := <-e.Batches()
		if !first.Seeded {
			t.Fatalf("first batch = %+v, want Seeded", first)
		}
		if len(first.Changes) != 0 {
			t.Errorf("the seeded batch carried %d changes, want none", len(first.Changes))
		}
	})
}

// A write burst inside one window is one batch, which is what stops a consumer
// rebuilding its view once per raw event.
func TestEngineCoalescesABurstIntoOneBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{Window: 50 * time.Millisecond})
		<-e.Batches()

		for range 50 {
			src.emit(Change{Path: "a/state.json", Op: OpModify, At: time.Now()})
		}
		synctest.Wait()
		time.Sleep(60 * time.Millisecond)

		b := <-e.Batches()
		if len(b.Changes) != 1 {
			t.Fatalf("a 50-write burst produced %d changes, want 1", len(b.Changes))
		}
		if b.Stats.Coalesced != 49 {
			t.Errorf("Stats.Coalesced = %d, want 49", b.Stats.Coalesced)
		}
	})
}

// A slow consumer must cause merging, not loss.
func TestEngineAccumulatesWhileTheConsumerIsBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{Window: 10 * time.Millisecond})
		<-e.Batches()

		for _, p := range []string{"a", "b", "c"} {
			src.emit(Change{Path: p, Op: OpCreate, At: time.Now()})
			synctest.Wait()
			time.Sleep(20 * time.Millisecond)
		}

		b := <-e.Batches()
		if len(b.Changes) != 3 {
			t.Fatalf("three windows closed while the consumer was busy produced %d changes, want 3", len(b.Changes))
		}
		if b.Truncated || b.Resync {
			t.Error("accumulation was reported as loss")
		}
	})
}

func TestEngineSweepsOnTheInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{Interval: 100 * time.Millisecond})
		<-e.Batches()

		synctest.Wait()
		time.Sleep(350 * time.Millisecond)
		synctest.Wait()

		if src.sweeps < 3 {
			t.Fatalf("%d sweeps in three and a half intervals, want at least 3", src.sweeps)
		}
	})
}

func TestEngineReportsRootLossAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := fstest.MapFS{"a/state.json": {Data: []byte(`{}`)}}
		gone := errors.New("stale NFS file handle")
		faulty := fsx.NewFaulty(base, fsx.Fault{Op: fsx.OpStat, Err: gone})
		root := fsx.New("ws", faulty)

		src := &stubSource{}
		e := newTestEngine(t, root, src, Options{
			Interval:     50 * time.Millisecond,
			Window:       10 * time.Millisecond,
			RootRetryMin: 50 * time.Millisecond,
			RootRetryMax: 100 * time.Millisecond,
		})
		<-e.Batches()

		synctest.Wait()
		time.Sleep(200 * time.Millisecond)

		b := <-e.Batches()
		if !b.RootLost {
			t.Fatalf("batch = %+v, want RootLost", b)
		}
		if !b.Stats.RootLost {
			t.Error("Stats does not report the lost root")
		}
		if len(b.Stats.Degradations()) == 0 {
			t.Error("a lost root produced no degradation to render")
		}

		// A root that never comes back stays lost rather than flapping between
		// reopened and unreadable.
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		if !e.Stats().RootLost {
			t.Error("a permanently unreadable root was declared recovered")
		}
	})
}

// flappingFS fails every operation while it is broken, standing in for a mount
// that goes away and comes back.
type flappingFS struct {
	fsx.FS
	mu     sync.Mutex
	broken bool
}

func (f *flappingFS) fail() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken {
		return errors.New("stale NFS file handle")
	}
	return nil
}

func (f *flappingFS) heal() {
	f.mu.Lock()
	f.broken = false
	f.mu.Unlock()
}

func (f *flappingFS) Stat(name string) (fs.FileInfo, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return f.FS.Stat(name)
}

func (f *flappingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := f.fail(); err != nil {
		return nil, err
	}
	return f.FS.ReadDir(name)
}

func TestEngineRecoversWhenTheRootReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := fstest.MapFS{"a/state.json": {Data: []byte(`{}`)}}
		mount := &flappingFS{FS: base, broken: true}
		root := fsx.New("ws", mount)

		e := newTestEngine(t, root, &stubSource{}, Options{
			Interval:     50 * time.Millisecond,
			Window:       10 * time.Millisecond,
			RootRetryMin: 50 * time.Millisecond,
			RootRetryMax: 60 * time.Millisecond,
		})
		<-e.Batches()

		synctest.Wait()
		time.Sleep(120 * time.Millisecond)
		if b := <-e.Batches(); !b.RootLost {
			t.Fatalf("batch = %+v, want RootLost", b)
		}

		mount.heal()
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)

		b := <-e.Batches()
		if !b.RootRecovered {
			t.Fatalf("batch = %+v, want RootRecovered", b)
		}
		if !b.Resync {
			t.Error("recovery did not request a resync, so changes during the outage would be missed")
		}
	})
}

func TestEngineTrackReplacesTheSweptSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{Interval: 50 * time.Millisecond})
		<-e.Batches()

		e.Track([]string{"a", "b", "c"})
		synctest.Wait()

		if got := e.Stats().Tracked; got != 3 {
			t.Fatalf("Stats.Tracked = %d, want 3", got)
		}
	})
}

func TestEngineCloseIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), &stubSource{}, Options{})
		<-e.Batches()
		if err := e.Close(); err != nil {
			t.Fatalf("first close: %v", err)
		}
		if err := e.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
	})
}

// A workspace on anything other than a filesystem known to be local must be
// swept as well as watched, or a write by another client goes unreported while
// the observer looks healthy.
func TestAutoModeSweepsAnythingNotKnownToBeLocal(t *testing.T) {
	t.Parallel()
	auto := config.Config{Watch: config.ModeAuto}

	if got := auto.FilesystemMode(fsx.KindLocal); got != config.ModeNotify {
		t.Errorf("a local filesystem resolved to %v, want notify", got)
	}
	for _, k := range []fsx.Kind{fsx.KindNetwork, fsx.KindFuse, fsx.KindUnknown} {
		if got := auto.FilesystemMode(k); got != config.ModeHybrid {
			t.Errorf("%v resolved to %v, so a remote write would go unreported", k, got)
		}
	}

	explicit := config.Config{Watch: config.ModeSweep}
	if got := explicit.FilesystemMode(fsx.KindLocal); got != config.ModeSweep {
		t.Errorf("an explicitly requested mode was overridden: %v", got)
	}
}

func TestManualObserverDeliversWhatItIsGiven(t *testing.T) {
	t.Parallel()
	m := NewManual()
	t.Cleanup(func() { _ = m.Close() })

	m.Track([]string{"a", "b"})
	if got := m.Tracked(); len(got) != 2 {
		t.Fatalf("Tracked = %v", got)
	}

	m.Emit(Change{Path: "b", Op: OpCreate}, Change{Path: "a", Op: OpModify})
	b := <-m.Batches()
	if len(b.Changes) != 2 || b.Changes[0].Path != "a" {
		t.Fatalf("batch = %+v, want changes sorted by path", b)
	}
}

func TestDegradationsAreEmptyWhenHealthy(t *testing.T) {
	t.Parallel()
	if got := (Stats{Watches: 10, Tracked: 4}).Degradations(); got != nil {
		t.Fatalf("a healthy observer reported %v", got)
	}
}

// A caller that inspects the observer before reading its batches is doing
// nothing unreasonable, and blocking it would hang the terminal interface
// before it drew anything.
func TestStatsIsServiceableBeforeTheSeedIsRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), &stubSource{}, Options{})

		got := e.Stats()
		if got.Mode != config.ModeHybrid {
			t.Fatalf("Stats before the seed was read returned %+v", got)
		}
		e.Track([]string{"a"})
		synctest.Wait()

		if b := <-e.Batches(); !b.Seeded {
			t.Fatalf("the first batch = %+v, want Seeded", b)
		}
	})
}

// A change arriving before the seed is read must not be lost behind it.
func TestChangesArrivingBeforeTheSeedIsReadSurvive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{Window: 10 * time.Millisecond})

		src.emit(Change{Path: "a/state.json", Op: OpModify, At: time.Now()})
		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()

		b := <-e.Batches()
		if len(b.Changes) != 1 {
			t.Fatalf("batch = %+v, want the change that arrived before the seed was read", b)
		}
		if b.Seeded {
			t.Error("a batch carrying change is still marked as only a seed")
		}
	})
}

// A change the queue could not hold is gone, so the consumer is told to rebuild
// rather than handed a batch that is not a complete account — and the loss is
// counted, because a resync with no number beside it says nothing about how
// much was missed.
func TestAFullQueueIsReportedRatherThanAbsorbed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{
			Window:   50 * time.Millisecond,
			MaxQueue: 4,
			MaxBatch: 64,
		})
		<-e.Batches()

		// Discovery outruns delivery: nothing reads the queue while these are
		// pushed, so it fills and the rest are lost.
		for i := range 40 {
			src.emit(Change{Path: "f" + strconv.Itoa(i), Op: OpCreate, At: time.Now()})
		}
		synctest.Wait()
		time.Sleep(80 * time.Millisecond)
		synctest.Wait()

		b := <-e.Batches()
		if !b.Resync {
			t.Fatalf("batch = %+v, want a resync after the queue overflowed", b)
		}
		if !b.Truncated {
			t.Error("the batch is not marked truncated")
		}
		if b.Stats.Dropped == 0 {
			t.Error("the loss was not counted, so an operator cannot see how much was missed")
		}
		if len(b.Stats.Degradations()) == 0 {
			t.Error("a dropped change produced no degradation to render")
		}
	})
}

// One lost change is one lost change. Counting it where it happens and again
// when the window closes reports twice what was missed, which is a number an
// operator sizes a ceiling against.
func TestALostChangeIsCountedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &stubSource{}
		e := newTestEngine(t, fsx.New("ws", fstest.MapFS{}), src, Options{
			Window:   50 * time.Millisecond,
			MaxQueue: 2,
			MaxBatch: 64,
		})
		<-e.Batches()

		const pushed = 20
		for i := range pushed {
			src.emit(Change{Path: "f" + strconv.Itoa(i), Op: OpCreate, At: time.Now()})
		}
		synctest.Wait()
		time.Sleep(80 * time.Millisecond)
		synctest.Wait()

		b := <-e.Batches()
		if b.Stats.Dropped == 0 {
			t.Fatal("nothing was counted as dropped")
		}
		if delivered := uint64(len(b.Changes)); b.Stats.Dropped+delivered > pushed {
			t.Errorf("%d pushed, %d delivered and %d counted dropped — more accounted for than sent",
				pushed, delivered, b.Stats.Dropped)
		}
	})
}

// The two ceilings are raised by different flags, so an operator told only how
// much was lost cannot tell which setting to change.
func TestTheHintNamesTheCeilingThatLostTheChanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		stats Stats
		want  string
	}{
		{"queue", Stats{Dropped: 3, QueueOverflows: 1}, "--max-queue"},
		{"batch", Stats{Dropped: 3, BatchCeilingHits: 1}, "--max-batch"},
		{"both", Stats{Dropped: 6, QueueOverflows: 1, BatchCeilingHits: 1}, "--max-queue and --max-batch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, d := range tc.stats.Diagnostics(0) {
				if d.Code != diag.CodeBatchTruncated {
					continue
				}
				if !strings.Contains(d.Hint, tc.want) {
					t.Fatalf("the hint is %q, want it to name %s", d.Hint, tc.want)
				}
				return
			}
			t.Fatal("no diagnostic reported the loss")
		})
	}
}
