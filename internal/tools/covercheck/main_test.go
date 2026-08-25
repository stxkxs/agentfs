package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// mapFiler serves reads from an in-memory tree and keeps what was written to
// it, so the gate is exercised without a temporary directory.
type mapFiler struct {
	tree     fstest.MapFS
	writeErr error
}

// ReadFile implements filer.
func (m *mapFiler) ReadFile(name string) ([]byte, error) { return fs.ReadFile(m.tree, name) }

// WriteFile implements filer.
func (m *mapFiler) WriteFile(name string, data []byte) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.tree[name] = &fstest.MapFile{Data: data}
	return nil
}

// tree builds a filer holding a floors file and a coverage profile.
func tree(config, profileText string) *mapFiler {
	return &mapFiler{tree: fstest.MapFS{
		"coverage.yaml": &fstest.MapFile{Data: []byte(config)},
		"cover.out":     &fstest.MapFile{Data: []byte(profileText)},
	}}
}

// exercise runs covercheck and returns its status and both streams.
func exercise(args []string, files filer) (status int, stdout, stderr string) {
	var out, errs bytes.Buffer
	status = run(args, files, &out, &errs)
	return status, out.String(), errs.String()
}

func TestOSFilerRoundTrips(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "coverage.yaml")
	want := []byte("global: 0\n")
	if err := (osFiler{}).WriteFile(path, want); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := osFiler{}.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("read back %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The floors file is data a build reads, so no platform gives it an
	// execute bit. Its exact permission bits are the process umask's to
	// narrow, which is why only the executable ones are asserted.
	if mode := info.Mode(); mode&0o111 != 0 || !mode.IsRegular() {
		t.Errorf("mode = %v, want a regular file with no execute bit", mode)
	}
}

func TestRunPasses(t *testing.T) {
	t.Parallel()
	files := tree("module: github.com/stxkxs/agentfs\nglobal: 50\npackages:\n  internal/diag: 60\n", sampleProfile)
	status, stdout, stderr := exercise(nil, files)
	if status != exitOK {
		t.Errorf("status = %d, want %d", status, exitOK)
	}
	if !strings.Contains(stdout, "internal/diag") || !strings.Contains(stdout, "total") {
		t.Errorf("stdout =\n%s\nwant a row for internal/diag and the total", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want it empty", stderr)
	}
}

func TestRunFailsBelowFloor(t *testing.T) {
	t.Parallel()
	files := tree("module: github.com/stxkxs/agentfs\nglobal: 0\npackages:\n  internal/diag: 90\n", sampleProfile)
	status, stdout, stderr := exercise([]string{"-profile", "cover.out", "-config", "coverage.yaml"}, files)
	if status != exitBelow {
		t.Errorf("status = %d, want %d", status, exitBelow)
	}
	if !strings.Contains(stdout, "below floor") {
		t.Errorf("stdout =\n%s\nwant the row marked below floor", stdout)
	}
	if !strings.Contains(stderr, "internal/diag: 66.7% is below the 90.0% floor") {
		t.Errorf("stderr =\n%s\nwant the failure named", stderr)
	}
}

func TestRunRatchetRecordsImprovements(t *testing.T) {
	t.Parallel()
	files := tree("module: github.com/stxkxs/agentfs\nglobal: 0\nrecorded:\n  internal/diag: 10\n", sampleProfile)
	status, _, stderr := exercise([]string{"-ratchet"}, files)
	if status != exitOK {
		t.Errorf("status = %d, want %d", status, exitOK)
	}
	if !strings.Contains(stderr, "recorded 2 raised value(s)") {
		t.Errorf("stderr = %q, want the recorded count", stderr)
	}
	written, err := files.ReadFile("coverage.yaml")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, want := range []string{"internal/diag: 66.7", "internal/textx: 100.0"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("floors file =\n%s\nwant %q", written, want)
		}
	}
}

func TestRunRatchetFailsARegression(t *testing.T) {
	t.Parallel()
	const config = "module: github.com/stxkxs/agentfs\nglobal: 0\nrecorded:\n  internal/diag: 90\n  internal/textx: 100\n"
	files := tree(config, sampleProfile)
	status, _, stderr := exercise([]string{"-ratchet"}, files)
	if status != exitBelow {
		t.Errorf("status = %d, want %d", status, exitBelow)
	}
	if !strings.Contains(stderr, "fell from the recorded 90.0%") {
		t.Errorf("stderr = %q, want the regression named", stderr)
	}
	written, err := files.ReadFile("coverage.yaml")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(written) != config {
		t.Errorf("floors file =\n%s\nwant it untouched, a regression is never recorded", written)
	}
}

func TestRunRatchetLeavesASettledFileAlone(t *testing.T) {
	t.Parallel()
	const config = "module: github.com/stxkxs/agentfs\nglobal: 0\nrecorded:\n  internal/diag: 66.7\n  internal/textx: 100.0\n"
	files := tree(config, sampleProfile)
	status, _, stderr := exercise([]string{"-ratchet"}, files)
	if status != exitOK {
		t.Errorf("status = %d, want %d", status, exitOK)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want it empty", stderr)
	}
	written, err := files.ReadFile("coverage.yaml")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(written) != config {
		t.Errorf("floors file =\n%s\nwant it untouched", written)
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		files func() *mapFiler
		want  string
	}{
		{
			name:  "unknown flag",
			args:  []string{"-ceiling"},
			files: func() *mapFiler { return tree("global: 0\n", sampleProfile) },
			want:  "flag provided but not defined",
		},
		{
			name:  "help",
			args:  []string{"-h"},
			files: func() *mapFiler { return tree("global: 0\n", sampleProfile) },
			want:  "usage: covercheck",
		},
		{
			name:  "positional argument",
			args:  []string{"cover.out"},
			files: func() *mapFiler { return tree("global: 0\n", sampleProfile) },
			want:  "unexpected argument",
		},
		{
			name:  "missing floors file",
			args:  []string{"-config", "absent.yaml"},
			files: func() *mapFiler { return tree("global: 0\n", sampleProfile) },
			want:  "read absent.yaml",
		},
		{
			name:  "missing profile",
			args:  []string{"-profile", "absent.out"},
			files: func() *mapFiler { return tree("global: 0\n", sampleProfile) },
			want:  "read absent.out",
		},
		{
			name:  "unparsable floors file",
			args:  nil,
			files: func() *mapFiler { return tree("ceiling: 1\n", sampleProfile) },
			want:  "coverage.yaml: line 1",
		},
		{
			name:  "unparsable profile",
			args:  nil,
			files: func() *mapFiler { return tree("global: 0\n", "not a profile\n") },
			want:  "cover.out: line 1",
		},
		{
			name: "unwritable floors file",
			args: []string{"-ratchet"},
			files: func() *mapFiler {
				files := tree("global: 0\n", sampleProfile)
				files.writeErr = errClosed
				return files
			},
			want: "write coverage.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, _, stderr := exercise(tt.args, tt.files())
			if status != exitUsage {
				t.Errorf("status = %d, want %d", status, exitUsage)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr =\n%s\nwant it to name %q", stderr, tt.want)
			}
		})
	}
}

func TestRunReportsAnUnwritableStream(t *testing.T) {
	t.Parallel()
	files := tree("global: 0\n", sampleProfile)
	var stderr bytes.Buffer
	if status := run(nil, files, errWriter{}, &stderr); status != exitUsage {
		t.Errorf("status = %d, want %d", status, exitUsage)
	}
	if !strings.Contains(stderr.String(), errClosed.Error()) {
		t.Errorf("stderr = %q, want the write failure named", stderr.String())
	}
}

func TestRunReportsAnUnwritableDiagnosticStream(t *testing.T) {
	t.Parallel()
	files := tree("module: github.com/stxkxs/agentfs\nglobal: 90\n", sampleProfile)
	var stdout bytes.Buffer
	if status := run(nil, files, &stdout, errWriter{}); status != exitUsage {
		t.Errorf("status = %d, want %d", status, exitUsage)
	}
}

func TestOutStopsAfterAFailure(t *testing.T) {
	t.Parallel()
	o := &out{w: errWriter{}}
	o.printf("first")
	o.printf("second")
	if o.err == nil {
		t.Fatal("out.err = nil, want the writer's error")
	}
	var sink bytes.Buffer
	quiet := &out{w: &sink, err: errClosed}
	quiet.printf("ignored")
	if sink.String() != "" {
		t.Errorf("wrote %q after a failure, want nothing", sink.String())
	}
}
