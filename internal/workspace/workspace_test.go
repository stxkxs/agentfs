package workspace_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/workspace"
)

var clock = time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)

func scannerOver(fsys fsx.FS, opts workspace.Options) *workspace.Scanner {
	if opts.Now == nil {
		opts.Now = func() time.Time { return clock }
	}
	return workspace.New(fsx.New("ws", fsys), opts)
}

func agentNamed(t *testing.T, res workspace.Result, name string) workspace.Agent {
	t.Helper()
	for _, a := range res.Agents {
		if a.Name == name || a.Dir == name {
			return a
		}
	}
	t.Fatalf("no agent %q in %v", name, names(res))
	return workspace.Agent{}
}

func names(res workspace.Result) []string {
	out := make([]string, 0, len(res.Agents))
	for _, a := range res.Agents {
		out = append(out, a.Name)
	}
	return out
}

func TestScanFindsAgentsByDocumentAndByRole(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"declares-state/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"declares-role/logs/a.log":  {Data: []byte("x")},
		"not-an-agent/readme.txt":   {Data: []byte("x")},
	}
	res := scannerOver(fsys, workspace.Options{}).Scan()

	if got := names(res); !slices.Equal(got, []string{"declares-role", "declares-state"}) {
		t.Fatalf("Scan found %v", got)
	}
	if a := agentNamed(t, res, "declares-role"); a.Presence != workspace.PresenceAbsent {
		t.Errorf("a workspace with no document has presence %v, want absent", a.Presence)
	}
	if a := agentNamed(t, res, "declares-role"); !a.HasRole(agentstate.DirLogs) {
		t.Error("the logs role was not recorded")
	}
}

func TestAgentNameComesFromTheDocumentWhenItDeclaresOne(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"dir-name/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"idle","agent":"researcher"}`)},
	}
	res := scannerOver(fsys, workspace.Options{}).Scan()
	if got := names(res); !slices.Equal(got, []string{"researcher"}) {
		t.Fatalf("Scan found %v, want the declared name", got)
	}
}

// A document rewritten in place is observed torn. Reporting that as invalid
// makes the bar flicker between the agent's real state and a parse error on
// every cycle of a normally behaving agent.
func TestTornWriteDoesNotFlapTheStatus(t *testing.T) {
	t.Parallel()
	good := []byte(`{"schema":"agentfs/v1","status":"running","task":"research"}`)
	fsys := fstest.MapFS{"a/state.json": {Data: good, ModTime: clock}}
	s := scannerOver(fsys, workspace.Options{})

	if a := agentNamed(t, s.Scan(), "a"); a.Status() != agentstate.StatusRunning {
		t.Fatalf("first scan status = %v", a.Status())
	}

	// The writer is mid-rewrite: a different size and time, and truncated.
	fsys["a/state.json"] = &fstest.MapFile{Data: []byte(`{"schema":"agentfs/v1","stat`), ModTime: clock.Add(time.Second)}

	a := agentNamed(t, s.Scan(), "a")
	if a.Presence != workspace.PresenceSettling {
		t.Fatalf("a torn reading has presence %v, want settling", a.Presence)
	}
	if a.Status() != agentstate.StatusRunning {
		t.Errorf("the last good reading was discarded: status = %v", a.Status())
	}
	if !hasCode(a.Diagnostics, diag.CodeSettling) {
		t.Errorf("the settling state raised no diagnostic: %v", codes(a.Diagnostics))
	}
}

// A document that stops changing and still does not decode is broken, not torn.
func TestASettledSyntaxErrorIsReported(t *testing.T) {
	t.Parallel()
	broken := []byte(`{"schema":"agentfs/v1","stat`)
	fsys := fstest.MapFS{"a/state.json": {Data: broken, ModTime: clock}}
	s := scannerOver(fsys, workspace.Options{})

	if a := agentNamed(t, s.Scan(), "a"); a.Presence != workspace.PresenceSettling {
		t.Fatalf("first reading of a broken document has presence %v, want settling", a.Presence)
	}
	a := agentNamed(t, s.Scan(), "a")
	if a.Presence != workspace.PresenceInvalid {
		t.Fatalf("an unchanged broken document has presence %v, want invalid", a.Presence)
	}
	if !hasCode(a.Diagnostics, diag.CodeNotJSON) {
		t.Errorf("codes = %v, want AFS1001", codes(a.Diagnostics))
	}
}

// A semantic error is wrong however many times it is read, so reporting it is
// never delayed.
func TestASemanticErrorIsNeverSettled(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`), ModTime: clock}}
	a := agentNamed(t, scannerOver(fsys, workspace.Options{}).Scan(), "a")

	if a.Presence == workspace.PresenceSettling {
		t.Fatal("an unknown status was withheld as if it were a torn write")
	}
	if !hasCode(a.Diagnostics, diag.CodeStatusUnknown) {
		t.Fatalf("codes = %v, want AFS3002", codes(a.Diagnostics))
	}
}

func TestStrictSettlingReportsOnTheFirstReading(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(`{`), ModTime: clock}}
	a := agentNamed(t, scannerOver(fsys, workspace.Options{SettleReads: 1}).Scan(), "a")
	if a.Presence != workspace.PresenceInvalid {
		t.Fatalf("with SettleReads 1 the presence is %v, want invalid", a.Presence)
	}
}

func TestStaleDocumentIsReportedAgainstItsDeclaredHeartbeat(t *testing.T) {
	t.Parallel()
	doc := `{"schema":"agentfs/v1","status":"running","heartbeat_seconds":10,"updated_at":"2026-04-08T12:59:00Z"}`
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(doc), ModTime: clock}}
	a := agentNamed(t, scannerOver(fsys, workspace.Options{}).Scan(), "a")

	if a.Presence != workspace.PresenceStale {
		t.Fatalf("presence = %v, want stale", a.Presence)
	}
	if !hasCode(a.Diagnostics, diag.CodeStale) {
		t.Errorf("codes = %v, want AFS4002", codes(a.Diagnostics))
	}
	if a.Status() != agentstate.StatusRunning {
		t.Errorf("a stale document lost its declared status: %v", a.Status())
	}
}

func TestAFreshDocumentIsNotStale(t *testing.T) {
	t.Parallel()
	doc := `{"schema":"agentfs/v1","status":"running","heartbeat_seconds":300,"updated_at":"2026-04-08T12:59:00Z"}`
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(doc), ModTime: clock}}
	if a := agentNamed(t, scannerOver(fsys, workspace.Options{}).Scan(), "a"); a.Presence != workspace.PresenceDeclared {
		t.Fatalf("presence = %v, want declared", a.Presence)
	}
}

// A workspace on a shared mount is written by hosts whose clocks are not this
// one, and rendering a negative age is worse than rendering zero.
func TestSmallClockLeadIsClampedRatherThanRenderedNegative(t *testing.T) {
	t.Parallel()
	doc := `{"schema":"agentfs/v1","status":"running","updated_at":"2026-04-08T13:00:03Z"}`
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(doc), ModTime: clock}}
	a := agentNamed(t, scannerOver(fsys, workspace.Options{SkewTolerance: 5 * time.Second}).Scan(), "a")
	if a.Age < 0 {
		t.Fatalf("Age = %v, want it clamped to zero", a.Age)
	}
}

func TestUnreadableDocumentIsDistinctFromInvalid(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)}}
	faulty := fsx.NewFaulty(base, fsx.Fault{Path: "a/state.json", Op: fsx.OpReadFile, Err: errors.New("input/output error")})
	a := agentNamed(t, scannerOver(faulty, workspace.Options{}).Scan(), "a")

	if a.Presence != workspace.PresenceUnreadable {
		t.Fatalf("presence = %v, want unreadable", a.Presence)
	}
	if !hasCode(a.Diagnostics, diag.CodeUnreadable) {
		t.Errorf("codes = %v, want AFS4001", codes(a.Diagnostics))
	}
}

func TestOversizedDocumentIsReportedRatherThanRead(t *testing.T) {
	t.Parallel()
	big := make([]byte, 4096)
	for i := range big {
		big[i] = ' '
	}
	fsys := fstest.MapFS{"a/state.json": {Data: big}}
	a := agentNamed(t, scannerOver(fsys, workspace.Options{MaxDocumentBytes: 1024}).Scan(), "a")

	if !hasCode(a.Diagnostics, diag.CodeDocumentTooLarge) {
		t.Fatalf("codes = %v, want AFS1008", codes(a.Diagnostics))
	}
}

// The retention ceiling on undefined members belongs to the contract, and the
// scanner is what hands the operator's value to it. Without that hand-off the
// setting is a flag with no effect on what a document costs to hold.
func TestPreservedMembersAreHeldToTheScannersCeiling(t *testing.T) {
	t.Parallel()
	doc := `{"schema":"agentfs/v1","status":"running","note":"` + strings.Repeat("x", 512) + `"}`
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(doc)}}

	within := agentNamed(t, scannerOver(fsys, workspace.Options{MaxExtraBytes: 4096}).Scan(), "a")
	if hasCode(within.Diagnostics, diag.CodeExtraTooLarge) {
		t.Errorf("codes = %v, want the member preserved under a ceiling that holds it", codes(within.Diagnostics))
	}

	beyond := agentNamed(t, scannerOver(fsys, workspace.Options{MaxExtraBytes: 16}).Scan(), "a")
	if !hasCode(beyond.Diagnostics, diag.CodeExtraTooLarge) {
		t.Errorf("codes = %v, want AFS1006", codes(beyond.Diagnostics))
	}
}

func TestFivePresenceStatesAreDistinguishable(t *testing.T) {
	t.Parallel()
	all := []workspace.Presence{
		workspace.PresenceAbsent, workspace.PresenceDeclared, workspace.PresenceStale,
		workspace.PresenceSettling, workspace.PresenceInvalid, workspace.PresenceUnreadable,
	}
	seen := map[string]workspace.Presence{}
	for _, p := range all {
		if prev, dup := seen[p.String()]; dup {
			t.Fatalf("%v and %v render identically as %q", prev, p, p.String())
		}
		seen[p.String()] = p
	}
	if !workspace.PresenceDeclared.Trustworthy() {
		t.Error("a declared reading is not trustworthy")
	}
	for _, p := range all {
		if p != workspace.PresenceDeclared && p.Trustworthy() {
			t.Errorf("%v claims to be trustworthy", p)
		}
	}
}

// A run's identity comes from what it declares; only a directory that declares
// nothing is identified by the shape of its name.
func TestDeclaredRunIdentityBeatsTheDirectoryName(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"a/runs/run-001/state.json": {Data: []byte(
			`{"schema":"agentfs/v1","status":"done","run_id":"eval-2026-04-08-a"}`)},
		"a/runs/run-002/out.json": {Data: []byte(`{}`)},
	}
	runs := scannerOver(fsys, workspace.Options{}).Runs("a")

	byDir := map[string]workspace.Run{}
	for _, r := range runs {
		byDir[r.Dir] = r
	}
	declared := byDir["a/runs/run-001"]
	if !declared.Declared || declared.ID != "eval-2026-04-08-a" {
		t.Errorf("declared run = %+v, want the declared id", declared)
	}
	if declared.Status != agentstate.StatusDone {
		t.Errorf("declared run status = %v, want done", declared.Status)
	}
	inferred := byDir["a/runs/run-002"]
	if inferred.Declared || inferred.ID != "run-002" {
		t.Errorf("inferred run = %+v, want an undeclared identity from the directory name", inferred)
	}
}

func TestRunsAreFoundBesideTheConventionalDirectories(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/state.json":       {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)},
		"a/run-007/out.json": {Data: []byte(`{}`)},
		"a/logs/run.log":     {Data: []byte("x")},
		"a/notes/todo.md":    {Data: []byte("x")},
	}
	runs := scannerOver(fsys, workspace.Options{}).Runs("a")

	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	if !slices.Contains(ids, "run-007") {
		t.Errorf("runs = %v, want run-007", ids)
	}
	if slices.Contains(ids, "logs") || slices.Contains(ids, "notes") {
		t.Errorf("runs = %v, want conventional and unmatched directories excluded", ids)
	}
}

// A long-running session watches a workspace whose agents come and go. The
// settle memory holds one reading per document, so it has to shrink with the
// workspace or it grows with the session instead.
func TestSettleMemoryFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a/state.json": {Data: []byte(`{"status":"running"}`), ModTime: clock}}
	s := scannerOver(fsys, workspace.Options{})
	s.Scan()

	for i := range 50 {
		doc := fmt.Sprintf("transient-%02d/state.json", i)
		fsys[doc] = &fstest.MapFile{Data: []byte(`{"status":"running"}`), ModTime: clock}
		s.Scan()
		delete(fsys, doc)
		s.Scan()
	}

	if got := s.SettleMemory(); got != 1 {
		t.Fatalf("the scanner holds %d readings after 50 agents came and went, want 1", got)
	}
}

// A document that leaves and returns is one the scanner knows nothing about, so
// the reading that comes back is a first reading and the settle rule starts
// over.
func TestAReturningDocumentIsReadAsANewOne(t *testing.T) {
	t.Parallel()
	torn := &fstest.MapFile{Data: []byte(`{`), ModTime: clock}
	fsys := fstest.MapFS{"a/state.json": torn}
	s := scannerOver(fsys, workspace.Options{})

	if a := agentNamed(t, s.Scan(), "a"); a.Presence != workspace.PresenceSettling {
		t.Fatalf("first reading = %v, want settling", a.Presence)
	}
	if a := agentNamed(t, s.Scan(), "a"); a.Presence != workspace.PresenceInvalid {
		t.Fatalf("second reading of an unchanged document = %v, want invalid", a.Presence)
	}

	delete(fsys, "a/state.json")
	s.Scan()
	fsys["a/state.json"] = torn

	if a := agentNamed(t, s.Scan(), "a"); a.Presence != workspace.PresenceSettling {
		t.Fatalf("reading after the document returned = %v, want settling", a.Presence)
	}
}

func TestRootLossIsReportedAsAWorkspaceDiagnostic(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{"a/state.json": {Data: []byte(`{}`)}}
	faulty := fsx.NewFaulty(base, fsx.Fault{Path: ".", Op: fsx.OpReadDir, Err: errors.New("stale NFS file handle")})
	res := scannerOver(faulty, workspace.Options{}).Scan()

	if len(res.Agents) != 0 {
		t.Errorf("a lost root produced %d agents", len(res.Agents))
	}
	if !hasCode(res.Diagnostics, diag.CodeRootLost) {
		t.Fatalf("workspace diagnostics = %v, want AFS5010", codes(res.Diagnostics))
	}
}

func codes(ds []diag.Diagnostic) []diag.Code {
	out := make([]diag.Code, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func hasCode(ds []diag.Diagnostic, c diag.Code) bool { return slices.Contains(codes(ds), c) }

// A gate that checked only an agent's own document would pass a workspace whose
// recorded runs are malformed, and a run's document is the one a reader
// consults after the agent that wrote it has gone.
func TestDocumentsIncludesRunStateDocuments(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/state.json":              {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"a/runs/run-001/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
		"a/runs/run-002/out.json":   {Data: []byte(`{}`)},
	}
	docs := scannerOver(fsys, workspace.Options{}).Documents()

	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	if !slices.Contains(paths, "a/runs/run-001/state.json") {
		t.Fatalf("Documents = %v, want the run's state document", paths)
	}
	if slices.Contains(paths, "a/runs/run-002/out.json") {
		t.Errorf("Documents = %v, want only state documents", paths)
	}

	for _, d := range docs {
		if d.Path != "a/runs/run-001/state.json" {
			continue
		}
		if d.Run != "run-001" {
			t.Errorf("the run document names run %q", d.Run)
		}
		if !hasCode(d.Diagnostics, diag.CodeStatusUnknown) {
			t.Errorf("a malformed run document raised %v, want AFS3002", codes(d.Diagnostics))
		}
		return
	}
	t.Fatal("the run document was not returned")
}

func TestDocumentsAreSortedByPath(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"z/state.json":              {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)},
		"a/state.json":              {Data: []byte(`{"schema":"agentfs/v1","status":"idle"}`)},
		"a/runs/run-001/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"done"}`)},
	}
	docs := scannerOver(fsys, workspace.Options{}).Documents()

	for i := 1; i < len(docs); i++ {
		if docs[i-1].Path >= docs[i].Path {
			t.Fatalf("Documents is not ordered by path: %q then %q", docs[i-1].Path, docs[i].Path)
		}
	}
}

// A caller can have two scans in flight at once — a periodic rescan and one a
// change triggered arrive on separate goroutines — so the remembered readings
// the settle rule depends on are guarded rather than left to the caller.
func TestConcurrentScansShareTheSettleMemorySafely(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running"}`)},
		"b/state.json": {Data: []byte(`{`)},
		"c/state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"not running"}`)},
	}
	s := scannerOver(fsys, workspace.Options{})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if got := s.Scan(); len(got.Agents) != 3 {
					t.Errorf("a concurrent scan found %d agents, want 3", len(got.Agents))
					return
				}
				s.Documents()
			}
		}()
	}
	wg.Wait()
}
