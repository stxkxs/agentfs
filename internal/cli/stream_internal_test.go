package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/report"
	"github.com/stxkxs/agentfs/internal/watch"
)

var batchInstant = time.Date(2026, time.April, 8, 13, 0, 0, 0, time.UTC)

// newStreamer returns a streamer writing into buf over an empty workspace.
func newStreamer(t *testing.T, buf *bytes.Buffer) *streamer {
	t.Helper()
	root, err := fsx.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	return &streamer{
		out:     report.NewStream(buf),
		root:    root,
		obs:     watch.NewManual(),
		sweeps:  true,
		entries: config.Defaults().MaxEntriesPerDir,
		tracked: make(map[string]struct{}),
	}
}

// decode reads the records a streamer wrote.
func decode(t *testing.T, buf *bytes.Buffer) []report.Record {
	t.Helper()
	var got []report.Record
	for line := range strings.Lines(buf.String()) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		var rec report.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %q is not a record: %v", line, err)
		}
		got = append(got, rec)
	}
	return got
}

// events returns the event each status record reports, in order.
func events(t *testing.T, recs []report.Record) []string {
	t.Helper()
	var out []string
	for _, rec := range recs {
		if rec.Kind != report.RecordStatus {
			continue
		}
		data, ok := rec.Data.(map[string]any)
		if !ok {
			t.Fatalf("a status record carries %T, want an object", rec.Data)
		}
		event, ok := data["event"].(string)
		if !ok {
			t.Fatalf("a status record reports the event as %T", data["event"])
		}
		out = append(out, event)
	}
	return out
}

// A batch names one condition per record, so a consumer branches on the event
// rather than decomposing a record that reports several facts at once. A
// recovered root always brings a resync with it, because the changes during
// the outage were not observed.
func TestABatchWritesOneRecordPerCondition(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newStreamer(t, &buf)

	batches := []watch.Batch{
		{RootLost: true, At: batchInstant},
		{RootRecovered: true, Resync: true, At: batchInstant.Add(time.Second)},
		{Resync: true, Truncated: true, At: batchInstant.Add(2 * time.Second)},
	}
	for _, b := range batches {
		if err := s.batch(b); err != nil {
			t.Fatalf("batch: %v", err)
		}
	}

	got := events(t, decode(t, &buf))
	want := []string{eventRootLost, eventRootRecovered, eventResync, eventResync}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Two records reporting the same fact carry the same key, and two reporting
// different facts do not: a consumer reading a stream twice discards the
// repeats by key and keeps everything else.
func TestEveryRecordCarriesTheIdentityAConsumerDeduplicatesOn(t *testing.T) {
	t.Parallel()
	var first, second bytes.Buffer
	b := watch.Batch{
		At:     batchInstant,
		Resync: true,
		Changes: []watch.Change{
			{Path: "agent-a/state.json", Op: watch.OpModify, At: batchInstant},
			{Path: "agent-b", Op: watch.OpCreate, IsDir: true, At: batchInstant},
		},
		Stats: watch.Stats{Errors: 1, LastError: "read agent-c: permission denied"},
	}
	if err := newStreamer(t, &first).batch(b); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := newStreamer(t, &second).batch(b); err != nil {
		t.Fatalf("batch: %v", err)
	}

	one, two := decode(t, &first), decode(t, &second)
	if len(one) != 4 {
		t.Fatalf("the batch wrote %d records, want a status, an error and two changes", len(one))
	}
	seen := make(map[string]bool, len(one))
	for i := range one {
		if one[i].DedupKey == "" {
			t.Errorf("the %s record carries no dedup key", one[i].Kind)
		}
		if one[i].DedupKey != two[i].DedupKey {
			t.Errorf("record %d: two producers of the same event keyed it %q and %q",
				i, one[i].DedupKey, two[i].DedupKey)
		}
		if seen[one[i].DedupKey] {
			t.Errorf("two records of different facts share the key %q", one[i].DedupKey)
		}
		seen[one[i].DedupKey] = true
		if one[i].Time == "" {
			t.Errorf("the %s record carries no time", one[i].Kind)
		}
	}
}

// A counter reports faults cumulatively, so a stream that wrote one record per
// batch carrying it would report the same fault for as long as the counter
// stood.
func TestAFaultIsReportedOnceRatherThanEveryBatch(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newStreamer(t, &buf)

	raised := watch.Stats{Errors: 1, LastError: "read agent-a: no such file or directory"}
	for i := range 3 {
		if err := s.batch(watch.Batch{At: batchInstant.Add(time.Duration(i) * time.Second), Stats: raised}); err != nil {
			t.Fatalf("batch: %v", err)
		}
	}
	if got := len(decode(t, &buf)); got != 1 {
		t.Fatalf("three batches carrying one fault wrote %d records, want 1", got)
	}

	next := watch.Stats{Errors: 2, LastError: "read agent-b: permission denied"}
	if err := s.batch(watch.Batch{At: batchInstant.Add(4 * time.Second), Stats: next}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	recs := decode(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("a further fault wrote %d records in total, want 2", len(recs))
	}
	data, ok := recs[1].Data.(map[string]any)
	if !ok {
		t.Fatalf("an error record carries %T, want an object", recs[1].Data)
	}
	if data["message"] != next.LastError {
		t.Errorf("message = %v, want %q", data["message"], next.LastError)
	}
}

// The tracked set is what the sweep covers, so it has to follow the
// directories the workspace has. A removal cannot state that it was a
// directory, so a tracked path that vanishes is recognized from the set:
// leaving it in spends a read on it every cycle and reports the failure every
// cycle.
func TestTheTrackedSetFollowsTheDirectoriesTheWorkspaceHas(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	s := newStreamer(t, &buf)
	s.tracked = map[string]struct{}{".": {}, "agent-a": {}}

	cases := []struct {
		name  string
		batch watch.Batch
		want  bool
	}{
		{"a seeded batch", watch.Batch{Seeded: true}, true},
		{"a resync", watch.Batch{Resync: true}, true},
		{"a recovered root", watch.Batch{RootRecovered: true, Resync: true}, true},
		{"a directory appearing", watch.Batch{Changes: []watch.Change{
			{Path: "agent-b", Op: watch.OpCreate, IsDir: true}}}, true},
		{"a tracked directory going away", watch.Batch{Changes: []watch.Change{
			{Path: "agent-a", Op: watch.OpRemove}}}, true},
		{"a document being written", watch.Batch{Changes: []watch.Change{
			{Path: "agent-a/state.json", Op: watch.OpModify}}}, false},
	}
	for _, tc := range cases {
		if got := s.restack(tc.batch); got != tc.want {
			t.Errorf("%s: restack = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A mode that does not sweep reads no tracked set, so computing one would
	// spend directory reads on nothing.
	s.sweeps = false
	if s.restack(watch.Batch{Resync: true}) {
		t.Error("a mode that does not sweep recomputed the tracked set")
	}
	if got := s.track(); got != 0 {
		t.Errorf("a mode that does not sweep tracked %d directories", got)
	}
}

// The swept set is where a state document is written: the root, the agent
// directories, and the conventional subdirectories those declare. Everything
// else in a workspace is content, and sweeping it would make cost a function
// of the files rather than of the agents.
func TestTheSweptSetIsTheRootTheAgentsAndTheirConventionalDirectories(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, p := range []string{
		"agent-a/runs/run-001",
		"agent-a/logs",
		"agent-a/artifacts/build/deep",
		"agent-b",
		".hidden",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(p)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	got := sweptDirs(root, config.Defaults().MaxEntriesPerDir)
	want := map[string]bool{
		".": true, "agent-a": true, "agent-b": true,
		"agent-a/runs": true, "agent-a/logs": true, "agent-a/artifacts": true,
	}
	if len(got) != len(want) {
		t.Fatalf("swept %v, want %d directories", got, len(want))
	}
	for _, d := range got {
		if !want[d] {
			t.Errorf("the sweep covers %q, which holds no state document", d)
		}
	}
}

// The number of directories under a root is the workspace's to choose, so one
// refresh of the tracked set is bounded by the same ceiling the tree applies
// to one directory.
func TestTheSweptSetIsBoundedByTheEntryCeiling(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for i := range 20 {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("agent-%02d", i)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	root, err := fsx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	const ceiling = 5
	if got := len(sweptDirs(root, ceiling)); got > ceiling+1 {
		t.Errorf("swept %d directories under a ceiling of %d", got, ceiling)
	}
}

// A mode is resolved before the stream starts, and only the two that read the
// tracked set need one computed.
func TestOnlyASweepingModeReadsTheTrackedSet(t *testing.T) {
	t.Parallel()
	cases := map[config.Mode]bool{
		config.ModeNotify: false,
		config.ModeSweep:  true,
		config.ModeHybrid: true,
	}
	for mode, want := range cases {
		if got := sweeps(mode); got != want {
			t.Errorf("sweeps(%s) = %v, want %v", mode, got, want)
		}
	}
}
