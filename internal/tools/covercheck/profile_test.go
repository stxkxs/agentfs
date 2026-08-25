package main

import (
	"math"
	"strings"
	"testing"
)

// sampleProfile covers two packages: one partly covered, one fully.
const sampleProfile = `mode: set
github.com/stxkxs/agentfs/internal/diag/diag.go:10.2,12.3 2 1
github.com/stxkxs/agentfs/internal/diag/diag.go:14.2,15.3 1 0
github.com/stxkxs/agentfs/internal/textx/fit.go:3.1,4.2 3 1
`

func TestParseProfileAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		mode    string
		regions int
	}{
		{name: "set mode", input: sampleProfile, mode: "set", regions: 3},
		{name: "count mode", input: "mode: count\na/b.go:1.1,2.2 4 7\n", mode: "count", regions: 1},
		{name: "atomic mode", input: "mode: atomic\na/b.go:1.1,2.2 4 7\n", mode: "atomic", regions: 1},
		{name: "blank lines ignored", input: "\nmode: set\n\na/b.go:1.1,2.2 1 1\n\n", mode: "set", regions: 1},
		{name: "header only", input: "mode: set\n", mode: "set", regions: 0},
		{name: "colon in path", input: "mode: set\na/b:c.go:1.1,2.2 1 1\n", mode: "set", regions: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := parseProfile(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("parseProfile: %v", err)
			}
			if p.mode != tt.mode {
				t.Errorf("mode = %q, want %q", p.mode, tt.mode)
			}
			if len(p.regions) != tt.regions {
				t.Errorf("regions = %d, want %d", len(p.regions), tt.regions)
			}
		})
	}
}

func TestParseProfileRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: "no mode header"},
		{name: "no header", input: "a/b.go:1.1,2.2 1 1\n", want: "\"mode: \" header"},
		{name: "unknown mode", input: "mode: fraction\n", want: "unknown coverage mode"},
		{name: "missing counts", input: "mode: set\na/b.go:1.1,2.2\n", want: "line 2"},
		{name: "one count", input: "mode: set\na/b.go:1.1,2.2 1\n", want: "file.go:line.col"},
		{name: "no colon", input: "mode: set\nab.go 1.1,2.2 1 1\n", want: "file.go:line.col"},
		{name: "empty file name", input: "mode: set\n:1.1,2.2 1 1\n", want: "file.go:line.col"},
		{name: "no range comma", input: "mode: set\na/b.go:1.1 1 1\n", want: "file.go:line.col"},
		{name: "no column", input: "mode: set\na/b.go:1,2.2 1 1\n", want: "line.col"},
		{name: "zero line", input: "mode: set\na/b.go:0.1,2.2 1 1\n", want: "positive line number"},
		{name: "zero column", input: "mode: set\na/b.go:1.0,2.2 1 1\n", want: "positive column number"},
		{name: "bad end position", input: "mode: set\na/b.go:1.1,x.2 1 1\n", want: "positive line number"},
		{name: "negative statements", input: "mode: set\na/b.go:1.1,2.2 -1 1\n", want: "statement count"},
		{name: "bad execution count", input: "mode: set\na/b.go:1.1,2.2 1 x\n", want: "execution count"},
		{name: "long line", input: "mode: set\n" + strings.Repeat("x", maxProfileLine+1) + "\n", want: "read profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseProfile(strings.NewReader(tt.input))
			if err == nil {
				t.Fatalf("parseProfile: want an error naming %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to name %q", err, tt.want)
			}
		})
	}
}

func TestParseProfileMergesRepeatedRegions(t *testing.T) {
	t.Parallel()
	input := "mode: count\n" +
		"a/b.go:1.1,2.2 3 0\n" +
		"a/b.go:1.1,2.2 3 5\n"
	p, err := parseProfile(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}
	if len(p.regions) != 1 {
		t.Fatalf("regions = %d, want the repeated region folded into 1", len(p.regions))
	}
	got := p.total()
	want := tally{covered: 3, total: 3}
	if got != want {
		t.Errorf("total = %+v, want %+v", got, want)
	}
}

func TestParseProfileSaturatesARepeatedCount(t *testing.T) {
	t.Parallel()
	const huge = "9223372036854775807"
	input := "mode: count\n" +
		"a/b.go:1.1,2.2 4 " + huge + "\n" +
		"a/b.go:1.1,2.2 4 " + huge + "\n"
	p, err := parseProfile(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}
	if got := p.total(); got != (tally{covered: 4, total: 4}) {
		t.Errorf("total = %+v, want the region covered; a wrapped count reads as never executed", got)
	}
}

func TestProfilePackages(t *testing.T) {
	t.Parallel()
	p, err := parseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}
	got := p.packages()
	want := map[string]tally{
		"github.com/stxkxs/agentfs/internal/diag":  {covered: 2, total: 3},
		"github.com/stxkxs/agentfs/internal/textx": {covered: 3, total: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("packages = %v, want %v", got, want)
	}
	for pkg, wantTally := range want {
		if got[pkg] != wantTally {
			t.Errorf("packages[%q] = %+v, want %+v", pkg, got[pkg], wantTally)
		}
	}
	if total := p.total(); total != (tally{covered: 5, total: 6}) {
		t.Errorf("total = %+v, want covered 5 of 6", total)
	}
}

func TestTallyPercent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   tally
		want float64
	}{
		{name: "no statements", in: tally{}, want: 100},
		{name: "none covered", in: tally{total: 4}, want: 0},
		{name: "half covered", in: tally{covered: 2, total: 4}, want: 50},
		{name: "all covered", in: tally{covered: 7, total: 7}, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.percent(); got != tt.want {
				t.Errorf("percent = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAbbrev(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 100)
	got := abbrev(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("abbrev = %q, want an elision mark", got)
	}
	if runes := len([]rune(got)); runes != 63 {
		t.Errorf("abbrev length = %d runes, want 63", runes)
	}
	if short := abbrev("short"); short != "short" {
		t.Errorf("abbrev = %q, want it unchanged", short)
	}
}

func FuzzParseProfile(f *testing.F) {
	f.Add(sampleProfile)
	f.Add("mode: set\n")
	f.Add("mode: atomic\na/b.go:1.1,2.2 1 1\na/b.go:1.1,2.2 1 1\n")
	f.Add("mode: count\n\x00:1.1,1.1 0 0\n")
	f.Add("mode: count\na/b.go:1.1,2.2 4 9223372036854775807\na/b.go:1.1,2.2 4 9223372036854775807\n")
	f.Fuzz(func(t *testing.T, input string) {
		p, err := parseProfile(strings.NewReader(input))
		if err != nil {
			return
		}
		for key, rec := range p.regions {
			if rec.stmts < 0 || rec.count < 0 {
				t.Fatalf("regions[%+v] = %+v, want non-negative counts; a wrapped one reads as never executed", key, rec)
			}
		}
		total := p.total()
		if total.covered > total.total {
			t.Fatalf("total = %+v, want covered within total", total)
		}
		var sum tally
		for pkg, t2 := range p.packages() {
			if t2.covered > t2.total {
				t.Fatalf("packages[%q] = %+v, want covered within total", pkg, t2)
			}
			percent := t2.percent()
			if math.IsNaN(percent) || percent < 0 || percent > 100 {
				t.Fatalf("packages[%q] percent = %v, want it within 0 to 100", pkg, percent)
			}
			sum.add(t2)
		}
		if sum != total {
			t.Fatalf("packages sum to %+v, want the total %+v", sum, total)
		}
	})
}
