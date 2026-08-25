package fsx_test

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/fsx"
)

func TestCleanRejectsEscapingPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"a/b.json", "a/b.json", true},
		{"./a/b.json", "a/b.json", true},
		{".", ".", true},
		{"a/./b", "a/b", true},
		{"a/../b", "b", true},
		{"..", "", false},
		{"../etc/passwd", "", false},
		{"a/../../etc/passwd", "", false},
		{"/etc/passwd", "", false},
		{`a\b`, "", false},
		{"", ".", true},
	}
	for _, tc := range cases {
		got, ok := fsx.Clean(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("Clean(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func FuzzClean(f *testing.F) {
	f.Add("a/b")
	f.Add("../escape")
	f.Add("/absolute")
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		got, ok := fsx.Clean(in)
		if !ok {
			return
		}
		if !fs.ValidPath(got) {
			t.Fatalf("Clean(%q) accepted invalid fs path %q", in, got)
		}
		if strings.HasPrefix(got, "/") || got == ".." || strings.HasPrefix(got, "../") {
			t.Fatalf("Clean(%q) accepted escaping path %q", in, got)
		}
	})
}

// The read-only property is structural: a caller cannot write to an observed
// workspace because the seam declares no method that writes.
func TestFSSeamHasNoWriteMethod(t *testing.T) {
	t.Parallel()
	banned := []string{"Create", "Write", "WriteFile", "Remove", "RemoveAll",
		"Mkdir", "MkdirAll", "Rename", "Chmod", "Chown", "Truncate", "Symlink", "Link"}
	typ := reflect.TypeOf((*fsx.FS)(nil)).Elem()
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		for _, b := range banned {
			if name == b {
				t.Errorf("fsx.FS exposes the write method %q", name)
			}
		}
	}
}

func TestCountingTalliesEveryOperation(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{
		"a.json":   {Data: []byte(`{}`)},
		"d/b.json": {Data: []byte(`{}`)},
	}
	c := fsx.NewCounting(base)

	if _, err := c.Stat("a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadDir("d"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile("a.json"); err != nil {
		t.Fatal(err)
	}

	got := c.Ops()
	if got.Stat != 1 || got.ReadDir != 1 || got.ReadFile != 1 {
		t.Fatalf("Ops = %+v, want one of each", got)
	}
	if got.Total() != 3 {
		t.Fatalf("Total = %d, want 3", got.Total())
	}

	c.Reset()
	if c.Ops().Total() != 0 {
		t.Fatal("Reset left a non-zero tally")
	}
}

func TestFaultyInjectsAfterN(t *testing.T) {
	t.Parallel()
	boom := errors.New("input/output error")
	base := fstest.MapFS{"a.json": {Data: []byte(`{}`)}}
	f := fsx.NewFaulty(base, fsx.Fault{Path: "a.json", Op: fsx.OpReadFile, Err: boom, AfterN: 1})

	if _, err := f.ReadFile("a.json"); err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if _, err := f.ReadFile("a.json"); !errors.Is(err, boom) {
		t.Fatalf("second read returned %v, want %v", err, boom)
	}
}

func TestFaultyMatchesEveryPathWhenPathIsEmpty(t *testing.T) {
	t.Parallel()
	boom := errors.New("stale NFS file handle")
	base := fstest.MapFS{"a.json": {Data: []byte(`{}`)}, "b.json": {Data: []byte(`{}`)}}
	f := fsx.NewFaulty(base, fsx.Fault{Err: boom})

	for _, name := range []string{"a.json", "b.json"} {
		if _, err := f.Stat(name); !errors.Is(err, boom) {
			t.Errorf("Stat(%q) returned %v, want %v", name, err, boom)
		}
	}
}

func TestHealthReportsRootLoss(t *testing.T) {
	t.Parallel()
	gone := errors.New("stale NFS file handle")
	base := fstest.MapFS{"a.json": {Data: []byte(`{}`)}}

	healthy := fsx.New("ws", base)
	if err := healthy.Health(); err != nil {
		t.Fatalf("healthy root reported %v", err)
	}

	lost := fsx.New("ws", fsx.NewFaulty(base, fsx.Fault{Op: fsx.OpStat, Err: gone}))
	err := lost.Health()
	if !errors.Is(err, fsx.ErrRootLost) {
		t.Fatalf("lost root reported %v, want ErrRootLost", err)
	}
	if !errors.Is(err, gone) {
		t.Fatalf("lost root lost the underlying cause: %v", err)
	}
}

func TestReadRangeReadsOnlyTheRequestedWindow(t *testing.T) {
	t.Parallel()
	data := []byte(strings.Repeat("0123456789", 100))
	root := fsx.New("ws", fstest.MapFS{"log": {Data: data}})

	buf := make([]byte, 10)
	n, size, err := root.ReadRange("log", 500, buf)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) {
		t.Fatalf("size = %d, want %d", size, len(data))
	}
	if n != 10 || string(buf[:n]) != "0123456789" {
		t.Fatalf("read %d bytes %q", n, buf[:n])
	}
}

func TestReadRangePastEndIsEmpty(t *testing.T) {
	t.Parallel()
	root := fsx.New("ws", fstest.MapFS{"log": {Data: []byte("abc")}})
	buf := make([]byte, 8)
	n, size, err := root.ReadRange("log", 100, buf)
	if err != nil || n != 0 || size != 3 {
		t.Fatalf("ReadRange past end = (%d,%d,%v)", n, size, err)
	}
}

// The test doubles are how partial failure is exercised everywhere else, so
// their own behaviour is checked rather than assumed.
func TestCountingAndFaultyCoverTheWholeSeam(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{
		"a.json": {Data: []byte(`{}`)},
		"link":   {Mode: fs.ModeSymlink, Data: []byte("a.json")},
	}

	c := fsx.NewCounting(base)
	if _, err := c.Lstat("link"); err != nil {
		t.Errorf("Lstat: %v", err)
	}
	if _, err := c.ReadLink("link"); err != nil {
		t.Errorf("ReadLink: %v", err)
	}
	if _, err := c.Open("a.json"); err != nil {
		t.Errorf("Open: %v", err)
	}
	got := c.Ops()
	if got.Lstat != 1 || got.ReadLink != 1 || got.Open != 1 {
		t.Fatalf("Ops = %+v, want one of each", got)
	}

	boom := errors.New("input/output error")
	f := fsx.NewFaulty(base, fsx.Fault{Err: boom})
	for name, call := range map[string]func() error{
		"Open":     func() error { _, err := f.Open("a.json"); return err },
		"Stat":     func() error { _, err := f.Stat("a.json"); return err },
		"Lstat":    func() error { _, err := f.Lstat("link"); return err },
		"ReadDir":  func() error { _, err := f.ReadDir("."); return err },
		"ReadFile": func() error { _, err := f.ReadFile("a.json"); return err },
		"ReadLink": func() error { _, err := f.ReadLink("link"); return err },
	} {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s returned %v, want the injected fault", name, err)
		}
	}
}

// A kind names itself so a probe result can be rendered and compared.
func TestEveryFilesystemKindNamesItself(t *testing.T) {
	t.Parallel()
	seen := map[string]fsx.Kind{}
	for _, k := range []fsx.Kind{fsx.KindUnknown, fsx.KindLocal, fsx.KindNetwork, fsx.KindFuse} {
		name := k.String()
		if name == "" {
			t.Errorf("kind %d renders as an empty string", k)
		}
		if prev, dup := seen[name]; dup && prev != k {
			t.Errorf("%v and %v both render as %q", prev, k, name)
		}
		seen[name] = k
	}
	if fsx.Kind(99).String() != "unknown" {
		t.Errorf("an unrecognized kind renders as %q, want unknown", fsx.Kind(99).String())
	}
	// Only a filesystem known to be local is trusted to raise an event for
	// every write; anything else is swept as well.
	if !fsx.KindLocal.EventsAreComplete() {
		t.Error("a local filesystem is not trusted for kernel events")
	}
	for _, k := range []fsx.Kind{fsx.KindNetwork, fsx.KindFuse, fsx.KindUnknown} {
		if k.EventsAreComplete() {
			t.Errorf("%v is trusted for kernel events, so a remote write would go unreported", k)
		}
	}
}

// A root whose filesystem was replaced reads through the new one, which is what
// a caller holding a root across a remount depends on.
func TestReopenOnASyntheticRootIsANoOp(t *testing.T) {
	t.Parallel()
	root := fsx.New("ws", fstest.MapFS{"a.json": {Data: []byte(`{}`)}})
	if err := root.Reopen(); err != nil {
		t.Fatalf("Reopen on a synthetic root: %v", err)
	}
	if err := root.Health(); err != nil {
		t.Fatalf("Health after Reopen: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("Close on a synthetic root: %v", err)
	}
}

// A read against a root with no filesystem reports the loss rather than
// panicking, which is the state a caller reaches between a loss and a reopen.
func TestReadsAgainstAnEmptyRootReportTheLoss(t *testing.T) {
	t.Parallel()
	var root fsx.Root

	if err := root.Health(); !errors.Is(err, fsx.ErrRootLost) {
		t.Errorf("Health = %v, want ErrRootLost", err)
	}
	if _, _, err := root.ReadRange("a", 0, make([]byte, 4)); !errors.Is(err, fsx.ErrRootLost) {
		t.Errorf("ReadRange = %v, want ErrRootLost", err)
	}
	if root.FS() != nil {
		t.Error("an empty root reports a filesystem")
	}
}

// A file the seam cannot read a range from reports it rather than returning
// bytes it did not read.
func TestReadRangeReportsAFailedOpen(t *testing.T) {
	t.Parallel()
	boom := errors.New("permission denied")
	base := fstest.MapFS{"a.log": {Data: []byte("abc")}}
	root := fsx.New("ws", fsx.NewFaulty(base, fsx.Fault{Op: fsx.OpOpen, Err: boom}))

	if _, _, err := root.ReadRange("a.log", 0, make([]byte, 4)); !errors.Is(err, boom) {
		t.Fatalf("ReadRange = %v, want the injected fault", err)
	}
}

func TestReadRangeWithNoBufferReadsNothing(t *testing.T) {
	t.Parallel()
	root := fsx.New("ws", fstest.MapFS{"a.log": {Data: []byte("abc")}})
	n, size, err := root.ReadRange("a.log", 0, nil)
	if err != nil || n != 0 || size != 3 {
		t.Fatalf("ReadRange with no buffer = (%d,%d,%v)", n, size, err)
	}
}
