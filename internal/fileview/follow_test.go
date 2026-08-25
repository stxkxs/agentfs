package fileview

import (
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/fsx"
)

// hookFS runs a hook before the first open, which is how a file that changes
// between the stat that sizes a read and the read itself is expressed.
type hookFS struct {
	fsx.FS
	before func()
}

func (h *hookFS) Open(name string) (fs.File, error) {
	if h.before != nil {
		h.before()
		h.before = nil
	}
	return h.FS.Open(name)
}

func appendTo(m fstest.MapFS, name, data string) {
	m[name].Data = append(m[name].Data, data...)
}

func numberedLines(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestFollowReadsOnlyTheAppendedRange(t *testing.T) {
	t.Parallel()
	body := numberedLines(500)
	m := mapFS(map[string]string{logName: body})
	rec := &recordFS{FS: m}
	root, ops := countingRoot(rec)

	w := Load(root, logName, Options{})
	if err := w.Err(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	before := len(w.Lines())
	splitBefore := w.split
	ops.Reset()
	rec.reads = nil

	added := "line 500\nline 501\n"
	appendTo(m, logName, added)
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}

	if got := ops.Ops(); got.Stat != 1 || got.Open != 1 || got.Total() != 2 {
		t.Errorf("ops = %+v, want one stat and one open", got)
	}
	if len(rec.reads) != 1 {
		t.Fatalf("reads = %+v, want one ranged read", rec.reads)
	}
	read := rec.reads[0]
	if want := int64(len(body) - anchorBytes); read.off != want {
		t.Errorf("read offset = %d, want %d", read.off, want)
	}
	if want := anchorBytes + len(added); read.n != want {
		t.Errorf("read length = %d, want %d: the anchor plus the appended bytes", read.n, want)
	}

	lines := w.Lines()
	if len(lines) != before+2 {
		t.Fatalf("Lines() = %d, want %d", len(lines), before+2)
	}
	if lines[0].Text != "line 0" || lines[len(lines)-1].Text != "line 501" {
		t.Errorf("lines run %q..%q, want line 0..line 501", lines[0].Text, lines[len(lines)-1].Text)
	}
	if w.split != splitBefore+len(added) {
		t.Errorf("split = %d, want %d: the lines before the append were rebuilt", w.split, splitBefore+len(added))
	}
	if w.Size() != int64(len(body)+len(added)) {
		t.Errorf("Size() = %d, want %d", w.Size(), len(body)+len(added))
	}
}

func TestFollowUnchangedFileKeepsItsLines(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "one\ntwo\n"})
	root, ops := countingRoot(m)
	w := Load(root, logName, Options{})
	ops.Reset()

	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"one", "two"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if got := ops.Ops(); got.Total() != 2 {
		t.Errorf("ops = %+v, want one stat and one anchor read", got)
	}
}

func TestFollowCompletesAPartialLine(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "done\npart"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})
	if got, want := texts(w), []string{"done", "part"}; !equalStrings(got, want) {
		t.Fatalf("Lines() = %q, want %q", got, want)
	}

	appendTo(m, logName, "ial\nnext\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"done", "partial", "next"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowReloadsAfterTruncation(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "one\ntwo\nthree\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})

	m[logName].Data = []byte("fresh\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"fresh"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
	if w.Size() != 6 {
		t.Errorf("Size() = %d, want 6", w.Size())
	}
}

func TestFollowReloadsAfterRotationAtTheSameSize(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})

	m[logName].Data = []byte("cccc\ndddd\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"cccc", "dddd"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowReloadsWhenTheFileShrinksDuringTheRead(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\n"})
	hooked := &hookFS{FS: m}
	root, _ := countingRoot(hooked)
	w := Load(root, logName, Options{})

	hooked.before = func() { m[logName].Data = []byte("z\n") }
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"z"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowEvictsFromTheFrontOfATailWindow(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{MaxBytes: 32, Tail: true})

	appendTo(m, logName, "gggg\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if int64(len(w.buf)) > 32 {
		t.Errorf("held %d bytes, want at most 32", len(w.buf))
	}
	if w.Offset() != 3 {
		t.Errorf("Offset() = %d, want 3", w.Offset())
	}
	if head, tail := w.Truncated(); !head || tail {
		t.Errorf("Truncated() = (%t, %t), want (true, false)", head, tail)
	}
	want := []string{"bbbb", "cccc", "dddd", "eeee", "ffff", "gggg"}
	if got := texts(w); !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowAbsorbsAnAppendOfExactlyTheWindowSize(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{MaxBytes: 32, Tail: true})

	appendTo(m, logName, "gggg\nhhhh\niiii\njjjj\nkkkk\nllll\nm\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if w.Offset() != 30 {
		t.Errorf("Offset() = %d, want 30", w.Offset())
	}
	want := []string{"hhhh", "iiii", "jjjj", "kkkk", "llll", "m"}
	if got := texts(w); !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowReloadsWhenTheAppendExceedsTheWindow(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{MaxBytes: 32, Tail: true})

	appendTo(m, logName, strings.Repeat("gggg\n", 8))
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if w.Offset() != 38 {
		t.Errorf("Offset() = %d, want 38", w.Offset())
	}
	want := []string{"gggg", "gggg", "gggg", "gggg", "gggg", "gggg"}
	if got := texts(w); !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowHoldsAFullHeadWindowStill(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "aaaa\nbbbb\ncccc\ndddd\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{MaxBytes: 16})
	before := texts(w)

	appendTo(m, logName, "eeee\nffff\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got := texts(w); !equalStrings(got, before) {
		t.Errorf("Lines() = %q, want the head window unchanged at %q", got, before)
	}
	if int64(len(w.buf)) > 16 {
		t.Errorf("held %d bytes, want at most 16", len(w.buf))
	}
	if w.Size() != 30 {
		t.Errorf("Size() = %d, want 30", w.Size())
	}
	if _, tail := w.Truncated(); !tail {
		t.Error("Truncated() does not report the bytes beyond the window")
	}
}

func TestFollowRetriesAfterAFailedLoad(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{stateName: "{}\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})
	if w.Err() == nil {
		t.Fatal("Err() = nil, want the missing file")
	}

	m[logName] = &fstest.MapFile{Data: []byte("arrived\n")}
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"arrived"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowRejectsAPathThatBecomesADirectory(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: "one\ntwo\n"})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})

	m[logName] = &fstest.MapFile{Mode: fs.ModeDir}
	if err := w.Follow(root, logName); !errors.Is(err, ErrNotRegular) {
		t.Errorf("Follow() = %v, want ErrNotRegular", err)
	}
}

func TestFollowStatFailure(t *testing.T) {
	t.Parallel()
	faulty := fsx.NewFaulty(mapFS(map[string]string{logName: "one\n"}),
		fsx.Fault{Op: fsx.OpStat, Err: fs.ErrPermission, AfterN: 1})
	root := fsx.New("test", faulty)
	w := Load(root, logName, Options{})
	if err := w.Err(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := w.Follow(root, logName); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Follow() = %v, want fs.ErrPermission", err)
	}
}

func TestFollowReadFailure(t *testing.T) {
	t.Parallel()
	faulty := fsx.NewFaulty(mapFS(map[string]string{logName: "one\n"}),
		fsx.Fault{Op: fsx.OpOpen, Err: fs.ErrPermission, AfterN: 1})
	root := fsx.New("test", faulty)
	w := Load(root, logName, Options{})
	if err := w.Err(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := w.Follow(root, logName); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Follow() = %v, want fs.ErrPermission", err)
	}
}

func TestFollowRefreshesBinaryContent(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{"artifacts/blob.bin": "\x00" + strings.Repeat("a", 1023)})
	root, _ := countingRoot(m)
	w := Load(root, "artifacts/blob.bin", Options{})
	if w.Kind() != KindBinary {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindBinary)
	}

	appendTo(m, "artifacts/blob.bin", strings.Repeat("b", 1024))
	if err := w.Follow(root, "artifacts/blob.bin"); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	lines := w.Lines()
	if len(lines) != 1 {
		t.Fatalf("Lines() = %q, want one explanatory line", texts(w))
	}
	if !strings.HasPrefix(lines[0].Text, "binary content, 2.0 KiB") {
		t.Errorf("line = %q, want the refreshed size", lines[0].Text)
	}
}

func TestFollowDescribesAFileThatBecomesBinary(t *testing.T) {
	t.Parallel()
	// An artifact is classified from whatever bytes existed when it was opened,
	// and the byte that settles it can arrive afterwards.
	m := mapFS(map[string]string{"artifacts/blob": "PNG"})
	root, _ := countingRoot(m)
	w := Load(root, "artifacts/blob", Options{})
	if w.Kind() != KindPlain {
		t.Fatalf("Kind() = %v, want %v from three text bytes", w.Kind(), KindPlain)
	}

	appendTo(m, "artifacts/blob", "\x00"+strings.Repeat("\xff", 1020))
	if err := w.Follow(root, "artifacts/blob"); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if w.Kind() != KindBinary {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindBinary)
	}
	if got, want := texts(w), []string{"binary content, 1.0 KiB, not displayed"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowRespansLinesWhenTheKindChanges(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{"unnamed": "the quick brown fox\n"})
	root, _ := countingRoot(m)
	w := Load(root, "unnamed", Options{})
	if w.Kind() != KindPlain {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindPlain)
	}

	appendTo(m, "unnamed", "level: info\ncount: 3\n")
	if err := w.Follow(root, "unnamed"); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if w.Kind() != KindYAML {
		t.Fatalf("Kind() = %v, want %v", w.Kind(), KindYAML)
	}
	// The lines built under the previous kind carry the spans of the one the
	// window ended up with, rather than the ones they were lexed under.
	last := w.Lines()[len(w.Lines())-1]
	if got, want := renderSpans(last.Text, last.Spans), []string{"key:count", "punct::", "number:3"}; !equalStrings(got, want) {
		t.Errorf("last line spans = %q, want %q", got, want)
	}
	for i, line := range w.Lines() {
		checkSpans(t, w.Kind(), line.Text, line.Spans)
		if i == 0 && len(line.Spans) != 0 {
			t.Errorf("line 0 = %q keeps %v, want prose to carry no role", line.Text, line.Spans)
		}
	}
}

func TestFollowKeepsTheKindOnceThePrefixIsHeld(t *testing.T) {
	t.Parallel()
	// Past the classification prefix the kind is settled, so an append cannot
	// change it and no further read is spent asking.
	body := strings.Repeat("x\n", sniffBytes)
	m := mapFS(map[string]string{"unnamed": body})
	root, ops := countingRoot(m)
	w := Load(root, "unnamed", Options{})
	before := w.Kind()
	ops.Reset()

	appendTo(m, "unnamed", "{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n")
	if err := w.Follow(root, "unnamed"); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if w.Kind() != before {
		t.Errorf("Kind() = %v, want the settled %v", w.Kind(), before)
	}
	if got := ops.Ops(); got.Total() != 2 {
		t.Errorf("ops = %+v, want one stat and one ranged read", got)
	}
}

func TestFollowAnEmptyFileThatGains(t *testing.T) {
	t.Parallel()
	m := mapFS(map[string]string{logName: ""})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})
	if len(w.Lines()) != 0 {
		t.Fatalf("Lines() = %q, want none", texts(w))
	}

	appendTo(m, logName, "first\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if got, want := texts(w), []string{"first"}; !equalStrings(got, want) {
		t.Errorf("Lines() = %q, want %q", got, want)
	}
}

func TestFollowHoldsTheLineCeiling(t *testing.T) {
	m := mapFS(map[string]string{logName: strings.Repeat("\n", MaxLines+2)})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{})
	if len(w.Lines()) != MaxLines {
		t.Fatalf("Lines() = %d, want %d", len(w.Lines()), MaxLines)
	}

	appendTo(m, logName, "tail\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if len(w.Lines()) != MaxLines {
		t.Errorf("Lines() = %d, want %d", len(w.Lines()), MaxLines)
	}
	if _, tail := w.Truncated(); !tail {
		t.Error("Truncated() does not report the lines beyond the ceiling")
	}
}

func TestFollowHoldsTheLineCeilingFromTheTail(t *testing.T) {
	m := mapFS(map[string]string{logName: strings.Repeat("\n", MaxLines+2)})
	root, _ := countingRoot(m)
	w := Load(root, logName, Options{Tail: true})

	appendTo(m, logName, "tail\n")
	if err := w.Follow(root, logName); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	lines := w.Lines()
	if len(lines) != MaxLines {
		t.Fatalf("Lines() = %d, want %d", len(lines), MaxLines)
	}
	if lines[len(lines)-1].Text != "tail" {
		t.Errorf("last line = %q, want the appended one", lines[len(lines)-1].Text)
	}
	if head, _ := w.Truncated(); !head {
		t.Error("Truncated() does not report the lines before the ceiling")
	}
}
