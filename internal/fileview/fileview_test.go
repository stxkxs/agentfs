package fileview

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/textx"
)

const (
	logName   = "runs/agent.log"
	stateName = "state.json"
)

func mapFS(files map[string]string) fstest.MapFS {
	m := make(fstest.MapFS, len(files))
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}

func countingRoot(fsys fsx.FS) (*fsx.Root, *fsx.Counting) {
	c := fsx.NewCounting(fsys)
	return fsx.New("test", c), c
}

func texts(w *Window) []string {
	out := make([]string, 0, len(w.Lines()))
	for _, l := range w.Lines() {
		out = append(out, l.Text)
	}
	return out
}

// recordFS records the offset and length of every ranged read, so a claim that
// a follow read only the appended range is checked against the read itself.
type recordFS struct {
	fsx.FS
	reads []readRange
}

type readRange struct {
	off int64
	n   int
}

func (r *recordFS) Open(name string) (fs.File, error) {
	f, err := r.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &recordFile{File: f, rec: r}, nil
}

type recordFile struct {
	fs.File
	rec *recordFS
}

func (f *recordFile) ReadAt(b []byte, off int64) (int, error) {
	ra, ok := f.File.(io.ReaderAt)
	if !ok {
		return 0, errors.New("fileview: fixture file does not support ranged reads")
	}
	f.rec.reads = append(f.rec.reads, readRange{off: off, n: len(b)})
	return ra.ReadAt(b, off)
}

func TestKindString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind Kind
		want string
	}{
		{KindPlain, "plain"},
		{KindJSON, "json"},
		{KindYAML, "yaml"},
		{KindNDJSON, "ndjson"},
		{KindLog, "log"},
		{KindBinary, "binary"},
		{Kind(99), "plain"},
	} {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestRoleString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		role Role
		want string
	}{
		{RolePlain, "plain"},
		{RoleKey, "key"},
		{RoleString, "string"},
		{RoleNumber, "number"},
		{RoleBool, "bool"},
		{RoleNull, "null"},
		{RolePunct, "punct"},
		{RoleTrace, "trace"},
		{RoleDebug, "debug"},
		{RoleInfo, "info"},
		{RoleWarn, "warn"},
		{RoleError, "error"},
		{Role(99), "plain"},
	} {
		if got := tc.role.String(); got != tc.want {
			t.Errorf("Role(%d).String() = %q, want %q", tc.role, got, tc.want)
		}
	}
}

func TestSpanLen(t *testing.T) {
	t.Parallel()
	if got := (Span{Start: 2, End: 7}).Len(); got != 5 {
		t.Errorf("Len() = %d, want 5", got)
	}
}

func TestDetectKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		file string
		head string
		want Kind
	}{
		{"nul makes binary", "notes.txt", "abc\x00def", KindBinary},
		{"nul beats extension", "state.json", "{\x00}", KindBinary},
		{"json extension", "state.json", `{"status":"running"}`, KindJSON},
		{"pretty json extension", "state.json", "{\n  \"a\": 1\n}\n", KindJSON},
		{"ndjson extension", "events.ndjson", "", KindNDJSON},
		{"jsonl extension", "events.jsonl", "", KindNDJSON},
		{"ndjson content under json name", "events.json", "{\"a\":1}\n{\"a\":2}\n", KindNDJSON},
		{"yaml extension", "config.yaml", "", KindYAML},
		{"yml extension", "config.yml", "", KindYAML},
		{"log extension", "agent.log", "", KindLog},
		{"txt extension", "notes.txt", "{\"a\":1}", KindPlain},
		{"md extension", "README.md", "# title", KindPlain},
		{"empty content", "unnamed", "", KindPlain},
		{"blank content", "unnamed", "\n\n  \n", KindPlain},
		{"sniffed json object", "unnamed", "{\n \"a\": 1\n}", KindJSON},
		{"sniffed json array", "unnamed", "[1, 2, 3]", KindJSON},
		{"sniffed ndjson", "unnamed", "{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n", KindNDJSON},
		{"sniffed yaml document", "unnamed", "---\nfoo: bar\n", KindYAML},
		{"sniffed yaml mapping", "unnamed", "name: agent\nsteps: 3\n", KindYAML},
		{"sniffed yaml sequence", "unnamed", "- one\n- two\n", KindYAML},
		{"sniffed log level", "unnamed", "INFO starting up\nWARN retrying\n", KindLog},
		{"sniffed log timestamp", "unnamed", "2026-08-24T10:00:00Z started\n2026-08-24T10:00:01Z done\n", KindLog},
		{"sniffed prose", "unnamed", "the quick brown fox\njumped over it\n", KindPlain},
		{"ndjson needs two objects", "unnamed", "{\"a\":1}\n", KindJSON},
		{"mixed lines are not ndjson", "unnamed", "{\"a\":1}\nplain\n", KindJSON},
		{"trailing partial ndjson line", "unnamed", "{\"a\":1}\n{\"b\":2}\n{\"c\":", KindNDJSON},
		{"dash that is not a marker", "unnamed", "-x\n-y\n", KindPlain},
	} {
		if got := DetectKind(tc.file, []byte(tc.head)); got != tc.want {
			t.Errorf("%s: DetectKind(%q, %q) = %v, want %v", tc.name, tc.file, tc.head, got, tc.want)
		}
	}
}

func TestDetectKindIgnoresNULBeyondSniffPrefix(t *testing.T) {
	t.Parallel()
	head := append(bytes.Repeat([]byte("a"), sniffBytes), 0)
	if got := DetectKind("notes.txt", head); got != KindPlain {
		t.Errorf("DetectKind = %v, want %v", got, KindPlain)
	}
}

func TestLoadReadsWholeSmallFile(t *testing.T) {
	t.Parallel()
	root, ops := countingRoot(mapFS(map[string]string{logName: "one\ntwo\nthree\n"}))
	w := Load(root, logName, Options{})
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if got, want := texts(w), []string{"one", "two", "three"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if head, tail := w.Truncated(); head || tail {
		t.Errorf("Truncated() = (%t, %t), want (false, false)", head, tail)
	}
	if w.Size() != 14 || w.Offset() != 0 {
		t.Errorf("Size()/Offset() = %d/%d, want 14/0", w.Size(), w.Offset())
	}
	if w.Kind() != KindLog {
		t.Errorf("Kind() = %v, want %v", w.Kind(), KindLog)
	}
	if got := ops.Ops(); got.Open != 1 || got.Stat != 1 || got.Total() != 2 {
		t.Errorf("ops = %+v, want one stat and one open", got)
	}
}

func TestLoadKeepsFinalLineWithoutNewline(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: "one\ntwo"}))
	w := Load(root, logName, Options{})
	if got, want := texts(w), []string{"one", "two"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestLoadStripsCarriageReturn(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: "one\r\ntwo\r\n"}))
	w := Load(root, logName, Options{})
	if got, want := texts(w), []string{"one", "two"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: ""}))
	w := Load(root, logName, Options{})
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if len(w.Lines()) != 0 {
		t.Errorf("Lines() = %q, want none", texts(w))
	}
	if w.Size() != 0 {
		t.Errorf("Size() = %d, want 0", w.Size())
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: "x\n"}))
	w := Load(root, "absent.log", Options{})
	if !errors.Is(w.Err(), fs.ErrNotExist) {
		t.Errorf("Err() = %v, want fs.ErrNotExist", w.Err())
	}
	if len(w.Lines()) != 0 {
		t.Errorf("Lines() = %q, want none", texts(w))
	}
}

func TestLoadDirectory(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: "x\n"}))
	w := Load(root, "runs", Options{})
	if !errors.Is(w.Err(), ErrNotRegular) {
		t.Errorf("Err() = %v, want ErrNotRegular", w.Err())
	}
}

func TestLoadWithoutRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		root *fsx.Root
	}{
		{"nil root", nil},
		{"root with no filesystem", fsx.New("test", nil)},
	} {
		w := Load(tc.root, logName, Options{})
		if !errors.Is(w.Err(), fsx.ErrRootLost) {
			t.Errorf("%s: Err() = %v, want fsx.ErrRootLost", tc.name, w.Err())
		}
		if err := w.Follow(tc.root, logName); !errors.Is(err, fsx.ErrRootLost) {
			t.Errorf("%s: Follow() = %v, want fsx.ErrRootLost", tc.name, err)
		}
	}
}

func TestLoadStatFailure(t *testing.T) {
	t.Parallel()
	faulty := fsx.NewFaulty(mapFS(map[string]string{logName: "x\n"}), fsx.Fault{Op: fsx.OpStat, Err: fs.ErrPermission})
	w := Load(fsx.New("test", faulty), logName, Options{})
	if !errors.Is(w.Err(), fs.ErrPermission) {
		t.Errorf("Err() = %v, want fs.ErrPermission", w.Err())
	}
}

func TestLoadReadFailure(t *testing.T) {
	t.Parallel()
	faulty := fsx.NewFaulty(mapFS(map[string]string{logName: "x\n"}), fsx.Fault{Op: fsx.OpOpen, Err: fs.ErrPermission})
	w := Load(fsx.New("test", faulty), logName, Options{})
	if !errors.Is(w.Err(), fs.ErrPermission) {
		t.Errorf("Err() = %v, want fs.ErrPermission", w.Err())
	}
}

func TestLoadTailClassifiesFromUnreadableHead(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("alpha beta gamma delta\n", 64)
	faulty := fsx.NewFaulty(mapFS(map[string]string{logName: body}), fsx.Fault{Op: fsx.OpOpen, Err: fs.ErrPermission, AfterN: 1})
	w := Load(fsx.New("test", faulty), logName, Options{MaxBytes: 64, Tail: true})
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if w.Kind() != KindLog {
		t.Errorf("Kind() = %v, want %v from the extension", w.Kind(), KindLog)
	}
}

func TestLoadTruncatesTail(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\n"}))
	w := Load(root, logName, Options{MaxBytes: 12})
	if int64(len(w.buf)) > 12 {
		t.Fatalf("held %d bytes, want at most 12", len(w.buf))
	}
	head, tail := w.Truncated()
	if head || !tail {
		t.Errorf("Truncated() = (%t, %t), want (false, true)", head, tail)
	}
	if got, want := texts(w), []string{"aaaa", "bbbb", "cc"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if w.Size() != 20 {
		t.Errorf("Size() = %d, want 20", w.Size())
	}
}

func TestLoadTailHoldsEndOfFile(t *testing.T) {
	t.Parallel()
	root, ops := countingRoot(mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\n"}))
	w := Load(root, logName, Options{MaxBytes: 12, Tail: true})
	if w.Offset() != 8 {
		t.Fatalf("Offset() = %d, want 8", w.Offset())
	}
	head, tail := w.Truncated()
	if !head || tail {
		t.Errorf("Truncated() = (%t, %t), want (true, false)", head, tail)
	}
	if got, want := texts(w), []string{"cccc", "dddd"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if got := ops.Ops(); got.Open != 2 {
		t.Errorf("opens = %d, want 2: the window and the prefix that classifies it", got.Open)
	}
}

func TestLoadTailKeepsSingleLongLine(t *testing.T) {
	t.Parallel()
	root, _ := countingRoot(mapFS(map[string]string{logName: strings.Repeat("x", 40)}))
	w := Load(root, logName, Options{MaxBytes: 10, Tail: true})
	if got, want := texts(w), []string{strings.Repeat("x", 10)}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestLoadBoundsAHugeFile(t *testing.T) {
	const size = 64 << 20
	body := bytes.Repeat(hugeLine, size/len(hugeLine))
	root, _ := countingRoot(fstest.MapFS{logName: &fstest.MapFile{Data: body}})

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	w := Load(root, logName, Options{})
	runtime.ReadMemStats(&after)

	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if int64(len(w.buf)) > DefaultMaxBytes {
		t.Errorf("held %d bytes, want at most %d", len(w.buf), DefaultMaxBytes)
	}
	if int64(cap(w.buf)) > DefaultMaxBytes {
		t.Errorf("reserved %d bytes, want at most %d", cap(w.buf), DefaultMaxBytes)
	}
	if w.Size() != size {
		t.Errorf("Size() = %d, want %d", w.Size(), size)
	}
	if head, tail := w.Truncated(); head || !tail {
		t.Errorf("Truncated() = (%t, %t), want (false, true)", head, tail)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Errorf("Load allocated %d bytes for a %d byte file, want a bound independent of the file", grew, size)
	}
}

func TestLoadBoundsAHugeFileFromTheTail(t *testing.T) {
	const size = 64 << 20
	body := bytes.Repeat(hugeLine, size/len(hugeLine))
	root, _ := countingRoot(fstest.MapFS{logName: &fstest.MapFile{Data: body}})
	w := Load(root, logName, Options{Tail: true})
	if int64(len(w.buf)) > DefaultMaxBytes {
		t.Errorf("held %d bytes, want at most %d", len(w.buf), DefaultMaxBytes)
	}
	if w.Offset() != size-DefaultMaxBytes {
		t.Errorf("Offset() = %d, want %d", w.Offset(), size-DefaultMaxBytes)
	}
	if head, tail := w.Truncated(); !head || tail {
		t.Errorf("Truncated() = (%t, %t), want (true, false)", head, tail)
	}
	lines := w.Lines()
	if len(lines) == 0 {
		t.Fatal("Lines() is empty")
	}
	if got, want := lines[len(lines)-1].Text, strings.TrimSuffix(string(hugeLine), "\n"); got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
}

func TestLoadSanitizesEveryLine(t *testing.T) {
	t.Parallel()
	const osc52 = "\x1b]52;c;YWdlbnRmcw==\x07"
	body := "before " + osc52 + "after\n\x1b[2Jcleared\n"
	root, _ := countingRoot(mapFS(map[string]string{logName: body}))
	w := Load(root, logName, Options{})
	for i, line := range w.Lines() {
		if strings.ContainsRune(line.Text, 0x1B) {
			t.Errorf("line %d = %q holds an escape byte", i, line.Text)
		}
	}
	marker := string(textx.EscapeMarker)
	want := []string{"before " + marker + "after", marker + "cleared"}
	if got := texts(w); !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", texts(w), want)
	}
}

func TestLoadDescribesBinaryContent(t *testing.T) {
	t.Parallel()
	body := "\x89PNG\x00\x1a\n" + strings.Repeat("\xff\xfe", 1024)
	root, _ := countingRoot(mapFS(map[string]string{"artifacts/image.png": body}))
	w := Load(root, "artifacts/image.png", Options{})
	if w.Kind() != KindBinary {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindBinary)
	}
	lines := w.Lines()
	if len(lines) != 1 {
		t.Fatalf("Lines() = %q, want one explanatory line", texts(w))
	}
	if !strings.HasPrefix(lines[0].Text, "binary content, 2.0 KiB") {
		t.Errorf("line = %q, want it to describe the content", lines[0].Text)
	}
	if lines[0].Spans != nil {
		t.Errorf("Spans = %v, want none", lines[0].Spans)
	}
}

func TestLoadDetectsBinaryPastTheWindowCeiling(t *testing.T) {
	t.Parallel()
	// The NUL sits beyond the window's ceiling but inside the prefix a kind is
	// claimed from, so the window has to read the prefix to find it.
	body := strings.Repeat("a", 100) + "\x00" + strings.Repeat("b", 100)
	root, ops := countingRoot(mapFS(map[string]string{"artifacts/blob": body}))
	w := Load(root, "artifacts/blob", Options{MaxBytes: 16})
	if w.Kind() != KindBinary {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindBinary)
	}
	if got, want := texts(w), []string{"binary content, 201 B, not displayed"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if got := ops.Ops(); got.Open != 2 {
		t.Errorf("opens = %d, want 2: the window and the prefix that classifies it", got.Open)
	}
}

func TestLoadClassifiesFromTheWindowWhenItHoldsTheWholeFile(t *testing.T) {
	t.Parallel()
	root, ops := countingRoot(mapFS(map[string]string{logName: "one\ntwo\n"}))
	w := Load(root, logName, Options{MaxBytes: 16})
	if w.Kind() != KindLog {
		t.Errorf("Kind() = %v, want %v", w.Kind(), KindLog)
	}
	if got := ops.Ops(); got.Open != 1 {
		t.Errorf("opens = %d, want 1: the window already holds the prefix", got.Open)
	}
}

func TestLoadTreatsANonPositiveCeilingAsTheDefault(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", DefaultMaxBytes+1024)
	root, _ := countingRoot(mapFS(map[string]string{logName: body}))
	for _, max := range []int64{0, -1} {
		w := Load(root, logName, Options{MaxBytes: max})
		if int64(len(w.buf)) != DefaultMaxBytes {
			t.Errorf("MaxBytes %d held %d bytes, want %d", max, len(w.buf), DefaultMaxBytes)
		}
	}
}

func TestLoadHoldsTheSpanCeilingPerLine(t *testing.T) {
	// A minified document is one line, and a line of punctuation yields a span
	// per byte, so the span structure is what a window of one costs.
	body := strings.Repeat("{", DefaultMaxBytes)
	root, _ := countingRoot(mapFS(map[string]string{stateName: body}))

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	w := Load(root, stateName, Options{})
	runtime.ReadMemStats(&after)

	for i, line := range w.Lines() {
		if len(line.Spans) > MaxSpans {
			t.Fatalf("line %d carries %d spans, want at most %d", i, len(line.Spans), MaxSpans)
		}
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Errorf("Load allocated %d bytes for a %d byte window of punctuation, want the span ceiling to bound it",
			grew, DefaultMaxBytes)
	}
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1 << 20, "1.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1024.0 TiB"},
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestLoadCapsLineCount(t *testing.T) {
	body := strings.Repeat("\n", MaxLines+10)
	root, _ := countingRoot(mapFS(map[string]string{logName: body}))

	head := Load(root, logName, Options{})
	if len(head.Lines()) != MaxLines {
		t.Errorf("head Lines() = %d, want %d", len(head.Lines()), MaxLines)
	}
	if _, tail := head.Truncated(); !tail {
		t.Error("head window does not report the lines it dropped")
	}

	tailed := Load(root, logName, Options{Tail: true})
	if len(tailed.Lines()) != MaxLines {
		t.Errorf("tail Lines() = %d, want %d", len(tailed.Lines()), MaxLines)
	}
	if head, _ := tailed.Truncated(); !head {
		t.Error("tail window does not report the lines it dropped")
	}
}

// hugeLine is a power-of-two length, so a file of them divides evenly into
// windows and the assertions name exact offsets.
var hugeLine = []byte("agentfs bounded window line 012\n")

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
