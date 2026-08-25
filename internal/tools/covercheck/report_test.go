package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// errWriter fails every write, standing in for a closed pipe.
type errWriter struct{}

var errClosed = errors.New("closed")

// noData is the verdict a row carries when the profile does not cover it.
const noData = "no data"

// Write implements io.Writer.
func (errWriter) Write([]byte) (int, error) { return 0, errClosed }

// buildReport evaluates one profile against one floors file.
func buildReport(t *testing.T, config, profileText string, ratchet bool) *report {
	t.Helper()
	f, err := parseFloors([]byte(config))
	if err != nil {
		t.Fatalf("parseFloors: %v", err)
	}
	p, err := parseProfile(strings.NewReader(profileText))
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}
	rep := evaluate(f, p, ratchet)
	return &rep
}

// find returns the row for a package key.
func find(t *testing.T, rep *report, key string) row {
	t.Helper()
	for _, r := range rep.rows {
		if r.key == key {
			return r
		}
	}
	t.Fatalf("rows = %+v, want one for %q", rep.rows, key)
	return row{}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	const profileText = "mode: set\n" +
		"m/a/x.go:1.1,2.2 4 1\n" + // a: 4 of 4
		"m/b/x.go:1.1,2.2 1 1\n" + // b: 1 of 4
		"m/b/x.go:3.1,4.2 3 0\n"

	tests := []struct {
		name      string
		config    string
		ratchet   bool
		key       string
		verdict   string
		failed    bool
		wantExit  int
		wantUpdat bool
	}{
		{
			name:     "a met floor passes",
			config:   "module: m\nglobal: 0\npackages:\n  a: 100\n",
			key:      "a",
			verdict:  "ok",
			wantExit: exitOK,
		},
		{
			name:     "an unmet floor fails",
			config:   "module: m\nglobal: 0\npackages:\n  b: 50\n",
			key:      "b",
			verdict:  "below floor",
			failed:   true,
			wantExit: exitBelow,
		},
		{
			name:     "an unlisted package carries no floor",
			config:   "module: m\nglobal: 0\n",
			key:      "b",
			verdict:  "ok",
			wantExit: exitOK,
		},
		{
			name:     "a listed package absent from the profile has no data",
			config:   "module: m\nglobal: 0\npackages:\n  c: 0\n",
			key:      "c",
			verdict:  noData,
			wantExit: exitOK,
		},
		{
			name:     "a listed package absent from the profile misses a real floor",
			config:   "module: m\nglobal: 0\npackages:\n  c: 10\n",
			key:      "c",
			verdict:  "below floor",
			failed:   true,
			wantExit: exitBelow,
		},
		{
			name:     "the global floor gates the total",
			config:   "module: m\nglobal: 90\n",
			key:      "a",
			verdict:  "ok",
			wantExit: exitBelow,
		},
		{
			name:      "a ratchet records an improvement",
			config:    "module: m\nglobal: 0\nrecorded:\n  a: 50\n",
			ratchet:   true,
			key:       "a",
			verdict:   "ok",
			wantExit:  exitOK,
			wantUpdat: true,
		},
		{
			name:     "a ratchet fails a drop below the record",
			config:   "module: m\nglobal: 0\nrecorded:\n  b: 90\n",
			ratchet:  true,
			key:      "b",
			verdict:  "regressed",
			failed:   true,
			wantExit: exitBelow,
		},
		{
			name:     "a record is inert without the ratchet",
			config:   "module: m\nglobal: 0\nrecorded:\n  b: 90\n",
			key:      "b",
			verdict:  "ok",
			wantExit: exitOK,
		},
		{
			name:     "rounding is not a regression",
			config:   "module: m\nglobal: 0\nrecorded:\n  b: 25.02\n",
			ratchet:  true,
			key:      "b",
			verdict:  "ok",
			wantExit: exitOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rep := buildReport(t, tt.config, profileText, tt.ratchet)
			r := find(t, rep, tt.key)
			if got := r.verdict(); got != tt.verdict {
				t.Errorf("verdict = %q, want %q", got, tt.verdict)
			}
			if r.failed() != tt.failed {
				t.Errorf("failed = %v, want %v", r.failed(), tt.failed)
			}
			if got := rep.exit(); got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if _, ok := rep.updates[tt.key]; ok != tt.wantUpdat {
				t.Errorf("updates[%q] present = %v, want %v", tt.key, ok, tt.wantUpdat)
			}
		})
	}
}

func TestEvaluateRatchetRecordsEveryMeasuredPackage(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 0\n", "mode: set\nm/a/x.go:1.1,2.2 4 1\n", true)
	if got, ok := rep.updates["a"]; !ok || got != 100 {
		t.Errorf("updates = %v, want a first record of 100 for a", rep.updates)
	}
}

func TestEvaluateWithoutRatchetRecordsNothing(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 0\n", "mode: set\nm/a/x.go:1.1,2.2 4 1\n", false)
	if len(rep.updates) != 0 {
		t.Errorf("updates = %v, want none without the ratchet", rep.updates)
	}
}

func TestEvaluateEmptyProfile(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "global: 0\n", "mode: set\n", true)
	if len(rep.rows) != 0 {
		t.Errorf("rows = %+v, want none", rep.rows)
	}
	if got := rep.total.verdict(); got != noData {
		t.Errorf("total verdict = %q, want %q", got, noData)
	}
	if got := rep.exit(); got != exitOK {
		t.Errorf("exit = %d, want %d", got, exitOK)
	}
}

// goldenTable is the table sampleProfile produces against sampleFloors.
const goldenTable = `package             floor     actual   recorded  status
-------------------------------------------------------
internal/diag        80.0       66.7       91.4  below floor
internal/textx       75.0      100.0          -  ok
-------------------------------------------------------
total                60.0       83.3          -  ok
`

func TestReportWrite(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, sampleFloors, sampleProfile, true)
	var table bytes.Buffer
	if err := rep.write(&table); err != nil {
		t.Fatalf("write: %v", err)
	}
	if table.String() != goldenTable {
		t.Errorf("table =\n%s\nwant\n%s", table.String(), goldenTable)
	}
}

func TestReportExplain(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 90\npackages:\n  b: 50\nrecorded:\n  a: 100\n", ""+
		"mode: set\nm/a/x.go:1.1,2.2 4 0\nm/b/x.go:1.1,2.2 1 1\nm/b/x.go:3.1,4.2 3 0\n", true)
	var notes bytes.Buffer
	if err := rep.explain(&notes); err != nil {
		t.Fatalf("explain: %v", err)
	}
	want := []string{
		"a: 0.0% fell from the recorded 100.0%",
		"b: 25.0% is below the 50.0% floor",
		"total: 12.5% is below the 90.0% floor",
	}
	for _, line := range want {
		if !strings.Contains(notes.String(), line) {
			t.Errorf("notes =\n%s\nwant a line %q", notes.String(), line)
		}
	}
}

func TestReportWriteRulesSpanTheHeader(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 0\npackages:\n  a-very-long-package-key/here: 0\n",
		"mode: set\nm/a/x.go:1.1,2.2 4 1\n", false)
	var table bytes.Buffer
	if err := rep.write(&table); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(table.String(), "\n"), "\n")
	header := lines[0]
	for _, i := range []int{1, len(lines) - 2} {
		if lines[i] != strings.Repeat("-", len(header)) {
			t.Errorf("rule at line %d is %d wide, want the header's %d", i+1, len(lines[i]), len(header))
		}
	}
}

func TestEvaluateRecordedPackageAbsentFromTheProfile(t *testing.T) {
	t.Parallel()
	const config = "module: m\nglobal: 0\nrecorded:\n  gone: 90\n"
	const profileText = "mode: set\nm/a/x.go:1.1,2.2 4 1\n"

	rep := buildReport(t, config, profileText, true)
	r := find(t, rep, "gone")
	if !r.regressed {
		t.Errorf("row = %+v, want a package that left the profile marked regressed", r)
	}
	if got := rep.exit(); got != exitBelow {
		t.Errorf("exit = %d, want %d", got, exitBelow)
	}
	if _, ok := rep.updates["gone"]; ok {
		t.Errorf("updates = %v, want no record for a package with no data", rep.updates)
	}
	var notes bytes.Buffer
	if err := rep.explain(&notes); err != nil {
		t.Fatalf("explain: %v", err)
	}
	const want = "gone: the profile covers no statements in it, and it recorded 90.0%"
	if !strings.Contains(notes.String(), want) {
		t.Errorf("notes =\n%s\nwant a line %q", notes.String(), want)
	}

	inert := buildReport(t, config, profileText, false)
	if r := find(t, inert, "gone"); r.failed() || r.verdict() != noData {
		t.Errorf("row = %+v, want an inert %q row without the ratchet", r, noData)
	}
}

func TestExplainNamesAnAbsentPackagesFloor(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 0\npackages:\n  gone: 40\n", "mode: set\nm/a/x.go:1.1,2.2 4 1\n", false)
	var notes bytes.Buffer
	if err := rep.explain(&notes); err != nil {
		t.Fatalf("explain: %v", err)
	}
	const want = "gone: the profile covers no statements in it, and its floor is 40.0%"
	if !strings.Contains(notes.String(), want) {
		t.Errorf("notes =\n%s\nwant a line %q", notes.String(), want)
	}
}

func TestEvaluateRecordsARisenPackageWhileAnotherFails(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, "module: m\nglobal: 0\nrecorded:\n  a: 10\n  b: 90\n",
		"mode: set\nm/a/x.go:1.1,2.2 4 1\nm/b/x.go:1.1,2.2 4 0\n", true)
	if got := rep.exit(); got != exitBelow {
		t.Errorf("exit = %d, want %d", got, exitBelow)
	}
	if got, ok := rep.updates["a"]; !ok || got != 100 {
		t.Errorf("updates = %v, want a raised to 100 even though b failed", rep.updates)
	}
	if _, ok := rep.updates["b"]; ok {
		t.Errorf("updates = %v, want no record for the regressed package", rep.updates)
	}
}

func TestReportWriteReportsAWriteFailure(t *testing.T) {
	t.Parallel()
	rep := buildReport(t, sampleFloors, sampleProfile, false)
	if err := rep.write(errWriter{}); !errors.Is(err, errClosed) {
		t.Errorf("write = %v, want the writer's error", err)
	}
	if err := rep.explain(errWriter{}); !errors.Is(err, errClosed) {
		t.Errorf("explain = %v, want the writer's error", err)
	}
}
