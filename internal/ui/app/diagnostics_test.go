package app_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/index"
	"github.com/stxkxs/agentfs/internal/ui/app"
	"github.com/stxkxs/agentfs/internal/ui/pane"
	"github.com/stxkxs/agentfs/internal/watch"
)

// A ceiling the observer reached is a machine-readable finding, not only a
// phrase on the status line. A caller reading the report surface branches on
// the code; without one it would have to parse prose written to fit a terminal.
func TestEveryObserverConditionRaisesItsCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		ws         watch.Stats
		ix         index.Stats
		recoveries uint64
		want       diag.Code
	}{
		{"a recovered root", watch.Stats{}, index.Stats{}, 1, diag.CodeRootRecovered},
		{"a truncated batch", watch.Stats{Dropped: 12}, index.Stats{}, 0, diag.CodeBatchTruncated},
		{"an exhausted watch budget", watch.Stats{WatchesRefused: 3}, index.Stats{}, 0, diag.CodeWatchBudget},
		{"the node ceiling", watch.Stats{}, index.Stats{Nodes: 5, NodeCeilingHit: true}, 0, diag.CodeNodeCeiling},
		{"a capped directory", watch.Stats{}, index.Stats{TruncatedDirs: 1}, 0, diag.CodeEntriesTruncated},
		{"a truncated subtree", watch.Stats{}, index.Stats{DepthTruncated: 4}, 0, diag.CodeDepthTruncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ds := app.ObserverDiagnosticsForTest(tc.ws, tc.ix, tc.recoveries)
			if len(ds) != 1 {
				t.Fatalf("%s raised %d diagnostics, want 1: %v", tc.name, len(ds), ds)
			}
			d := ds[0]
			if d.Code != tc.want {
				t.Fatalf("%s raised %s, want %s", tc.name, d.Code, tc.want)
			}
			info, registered := diag.Lookup(d.Code)
			if !registered {
				t.Fatalf("%s is not in the registry", d.Code)
			}
			if d.Severity != info.Severity {
				t.Errorf("%s raised at %v, want the registry's %v", d.Code, d.Severity, info.Severity)
			}
			if d.Message == "" || d.Hint == "" || d.Value == "" {
				t.Errorf("%s carries message %q, hint %q, value %q; a ceiling names what it is, "+
					"what to do, and how far past it the workspace is", d.Code, d.Message, d.Hint, d.Value)
			}
		})
	}
}

// An observer that reached no ceiling reports nothing, so a clean report is
// empty rather than a list of zeroes a reader has to scan.
func TestAHealthyObserverRaisesNothing(t *testing.T) {
	t.Parallel()
	if got := app.ObserverDiagnosticsForTest(watch.Stats{}, index.Stats{}, 0); len(got) != 0 {
		t.Fatalf("a healthy observer raised %v", got)
	}
}

// A root that came back is reported for as long as the process runs: the tree
// was rebuilt, so nothing on screen shows that changes during the outage were
// never observed. A root that is lost again withdraws it — the outage is the
// condition to report, not the recovery before it.
func TestARecoveredRootIsWithdrawnWhenTheRootIsLostAgain(t *testing.T) {
	t.Parallel()
	if got := app.ObserverDiagnosticsForTest(watch.Stats{RootLost: true}, index.Stats{}, 1); len(got) != 0 {
		t.Fatalf("a lost root still reported a recovery: %v", got)
	}
}

// The status line and the report surface answer different questions about one
// condition, so they word it differently. Repeating the line's phrase in a
// diagnostic would report the same condition twice in the same words.
func TestObserverDiagnosticsDoNotRepeatTheStatusLine(t *testing.T) {
	t.Parallel()
	ws := watch.Stats{Dropped: 4, WatchesRefused: 9}
	ix := index.Stats{Nodes: 7, NodeCeilingHit: true, TruncatedDirs: 2, DepthTruncated: 3}

	conditions := pane.Conditions(ws, ix, 0)
	if len(conditions) == 0 {
		t.Fatal("the status line ranked no condition for a degraded observer")
	}
	ds := app.ObserverDiagnosticsForTest(ws, ix, 0)

	raised := make(map[diag.Code]bool, len(ds))
	for _, d := range ds {
		raised[d.Code] = true
		for _, c := range conditions {
			if d.Message == c.Text {
				t.Errorf("%s reports %q, which is the status line's own words", d.Code, d.Message)
			}
		}
	}
	for _, want := range []diag.Code{
		diag.CodeBatchTruncated, diag.CodeWatchBudget,
		diag.CodeNodeCeiling, diag.CodeEntriesTruncated, diag.CodeDepthTruncated,
	} {
		if !raised[want] {
			t.Errorf("the status line ranks a condition the report surface omits: %s", want)
		}
	}
}

// The model's report is the observer's conditions and the workspace's findings
// read together. A consumer holding only the report must see every ceiling the
// operator watching the screen sees.
func TestTheModelReportsWhatLimitsTheObserver(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), wide, func(h *harness) {
		h.send(app.BatchWithStatsForTest(watch.Stats{Dropped: 6, WatchesRefused: 2}))

		codes := raisedCodes(h)
		for _, want := range []diag.Code{diag.CodeBatchTruncated, diag.CodeWatchBudget} {
			if !codes[want] {
				t.Errorf("the model reports no %s while the status line reads:\n%s", want, h.frame())
			}
		}
	})
}

// A reopened root is reported by the model rather than only by the batch that
// carried it, which is the batch a consumer polling the report never sees.
func TestTheModelReportsARecoveredRoot(t *testing.T) {
	t.Parallel()
	run(t, workspaceFixture(), wide, func(h *harness) {
		h.send(app.BatchMsgForTest(watch.Batch{RootRecovered: true, Resync: true, At: clock}))

		if !raisedCodes(h)[diag.CodeRootRecovered] {
			t.Errorf("a reopened root raised no %s", diag.CodeRootRecovered)
		}
	})
}

// A ceiling the index reached reaches the report from the index's own count,
// so a caller does not have to walk the tree to discover it holds a prefix.
func TestTheModelReportsTheIndexCeilings(t *testing.T) {
	t.Parallel()
	narrowLimits := func(c *config.Config) {
		c.MaxEntriesPerDir = 2
		c.MaxDepth = 1
	}
	runWithConfig(t, workspaceFixture(), wide, narrowLimits, func(h *harness) {
		h.model.Index().Expand("agent-researcher")
		h.send(app.BatchWithStatsForTest(watch.Stats{}))

		codes := raisedCodes(h)
		if !codes[diag.CodeEntriesTruncated] {
			t.Errorf("a directory held to two entries raised no %s", diag.CodeEntriesTruncated)
		}
		if !codes[diag.CodeDepthTruncated] {
			t.Errorf("a subtree below the depth ceiling raised no %s", diag.CodeDepthTruncated)
		}
	})
}

// The report is bounded. A workspace wrong in one way is wrong in that way once
// per document, so beyond the ceiling the remainder is counted rather than
// listed, and the count is itself a finding rather than a silent elision.
func TestRetainedFindingsAreBounded(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"agent-a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
		"agent-b/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
		"agent-c/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
		"agent-d/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
	}
	const ceiling = 2
	runWithConfig(t, fsys, wide, func(c *config.Config) { c.MaxDiagnostics = ceiling }, func(h *harness) {
		ds := h.model.Diagnostics()
		if len(ds) != ceiling {
			t.Fatalf("the report holds %d findings, want the ceiling of %d: %v", len(ds), ceiling, ds)
		}
		last := ds[len(ds)-1]
		if last.Code != diag.CodeDiagnosticsDropped {
			t.Fatalf("the report ends with %s, want %s naming what it could not hold",
				last.Code, diag.CodeDiagnosticsDropped)
		}
		if !strings.Contains(last.Value, "findings not listed") {
			t.Errorf("%s reports %q, which does not count what was shed", last.Code, last.Value)
		}
	})
}

// raisedCodes indexes the model's report by code.
func raisedCodes(h *harness) map[diag.Code]bool {
	h.t.Helper()
	out := make(map[diag.Code]bool)
	for _, d := range h.model.Diagnostics() {
		out[d.Code] = true
	}
	return out
}
