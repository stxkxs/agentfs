package main

import (
	"strings"
	"testing"
)

// sampleFloors exercises every construct the format allows: a comment, a
// module prefix, a global floor, both sections, and a blank line.
const sampleFloors = `# floors
module: github.com/stxkxs/agentfs

global: 60

packages:
  internal/diag: 80
  internal/textx: 75

recorded:
  internal/diag: 91.4
`

func TestParseFloorsAccepts(t *testing.T) {
	t.Parallel()
	f, err := parseFloors([]byte(sampleFloors))
	if err != nil {
		t.Fatalf("parseFloors: %v", err)
	}
	if f.module != "github.com/stxkxs/agentfs" {
		t.Errorf("module = %q, want the agentfs import path", f.module)
	}
	if f.global != 60 {
		t.Errorf("global = %v, want 60", f.global)
	}
	if f.pkg["internal/diag"] != 80 || f.pkg["internal/textx"] != 75 {
		t.Errorf("packages = %v, want floors of 80 and 75", f.pkg)
	}
	if f.recorded["internal/diag"] != 91.4 {
		t.Errorf("recorded = %v, want 91.4 for internal/diag", f.recorded)
	}
	if f.indent != "  " {
		t.Errorf("indent = %q, want two spaces", f.indent)
	}
}

func TestParseFloorsRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no colon", input: "global\n", want: "key: value"},
		{name: "empty key", input: ": 5\n", want: "want a key"},
		{name: "unknown key", input: "ceiling: 5\n", want: "unknown key"},
		{name: "module without value", input: "module:\n", want: "wants an import path"},
		{name: "section with value", input: "packages: 5\n", want: "takes no value"},
		{name: "entry without section", input: "  internal/diag: 5\n", want: "under no section"},
		{name: "entry after scalar", input: "global: 5\n  internal/diag: 5\n", want: "under no section"},
		{name: "duplicate top key", input: "global: 5\nglobal: 6\n", want: "duplicate key"},
		{name: "duplicate entry", input: "packages:\n  a: 1\n  a: 2\n", want: "duplicate key"},
		{name: "entry is not a percent", input: "packages:\n  a: high\n", want: "percent from 0 to 100"},
		{name: "percent too high", input: "global: 101\n", want: "percent from 0 to 100"},
		{name: "percent negative", input: "global: -1\n", want: "percent from 0 to 100"},
		{name: "percent not a number", input: "global: high\n", want: "percent from 0 to 100"},
		{name: "percent not finite", input: "global: NaN\n", want: "percent from 0 to 100"},
		{name: "line number reported", input: "global: 5\n\nceiling: 1\n", want: "line 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFloors([]byte(tt.input))
			if err == nil {
				t.Fatalf("parseFloors: want an error naming %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to name %q", err, tt.want)
			}
		})
	}
}

func TestParseFloorsSameKeyInBothSections(t *testing.T) {
	t.Parallel()
	f, err := parseFloors([]byte("packages:\n  a: 1\nrecorded:\n  a: 2\n"))
	if err != nil {
		t.Fatalf("parseFloors: %v", err)
	}
	if f.pkg["a"] != 1 || f.recorded["a"] != 2 {
		t.Errorf("floors = %v / %v, want a floor of 1 and a record of 2", f.pkg, f.recorded)
	}
}

func TestFloorsKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		module     string
		importPath string
		want       string
	}{
		{name: "inside module", module: "example.com/m", importPath: "example.com/m/internal/a", want: "internal/a"},
		{name: "module root", module: "example.com/m", importPath: "example.com/m", want: "."},
		{name: "outside module", module: "example.com/m", importPath: "example.com/other/a", want: "example.com/other/a"},
		{name: "prefix is not a path boundary", module: "example.com/m", importPath: "example.com/mm/a", want: "example.com/mm/a"},
		{name: "no module declared", module: "", importPath: "example.com/m/a", want: "example.com/m/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &floors{module: tt.module}
			if got := f.key(tt.importPath); got != tt.want {
				t.Errorf("key(%q) = %q, want %q", tt.importPath, got, tt.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		updates map[string]float64
		want    string
	}{
		{
			name:    "no updates leaves the file alone",
			input:   sampleFloors,
			updates: nil,
			want:    sampleFloors,
		},
		{
			name:    "an existing entry is rewritten in place",
			input:   "recorded:\n  a: 1\n  b: 2\n",
			updates: map[string]float64{"a": 3},
			want:    "recorded:\n  a: 3.0\n  b: 2\n",
		},
		{
			name:    "a missing entry joins the section",
			input:   "recorded:\n  a: 1\n\nglobal: 5\n",
			updates: map[string]float64{"b": 2},
			want:    "recorded:\n  a: 1\n  b: 2.0\n\nglobal: 5\n",
		},
		{
			name:    "an empty section takes the first entry",
			input:   "global: 5\nrecorded:\n",
			updates: map[string]float64{"a": 12.25},
			want:    "global: 5\nrecorded:\n  a: 12.2\n",
		},
		{
			name:    "an absent section is created",
			input:   "global: 5\n",
			updates: map[string]float64{"b": 2, "a": 1},
			want:    "global: 5\nrecorded:\n  a: 1.0\n  b: 2.0\n",
		},
		{
			name:    "the file's own indent is kept",
			input:   "recorded:\n    a: 1\n",
			updates: map[string]float64{"b": 2},
			want:    "recorded:\n    a: 1\n    b: 2.0\n",
		},
		{
			name:    "comments survive a rewrite",
			input:   "# keep me\nrecorded:\n  a: 1\n# and me\n",
			updates: map[string]float64{"a": 9},
			want:    "# keep me\nrecorded:\n  a: 9.0\n# and me\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := parseFloors([]byte(tt.input))
			if err != nil {
				t.Fatalf("parseFloors: %v", err)
			}
			if got := string(f.render(tt.updates)); got != tt.want {
				t.Errorf("render =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "one line", in: "a\n", want: []string{"a"}},
		{name: "no terminator", in: "a", want: []string{"a"}},
		{name: "carriage returns", in: "a\r\nb\r\n", want: []string{"a", "b"}},
		{name: "blank line kept", in: "a\n\nb\n", want: []string{"a", "", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitLines([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("splitLines = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitLines = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestFormatPercent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "0.0"},
		{in: 100, want: "100.0"},
		{in: 66.66666, want: "66.7"},
	}
	for _, tt := range tests {
		if got := formatPercent(tt.in); got != tt.want {
			t.Errorf("formatPercent(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// normalize is what a lossless render returns: the file with its line
// terminators settled and exactly one at the end.
func normalize(input string) string {
	text := strings.ReplaceAll(input, "\r\n", "\n")
	return strings.TrimSuffix(text, "\n") + "\n"
}

func FuzzParseFloors(f *testing.F) {
	f.Add(sampleFloors)
	f.Add("global: 5\n")
	f.Add("recorded:\n  a: 1\n")
	f.Add("packages:\n  a: 1\nrecorded:\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := parseFloors([]byte(input))
		if err != nil {
			return
		}
		if got := string(parsed.render(nil)); got != normalize(input) {
			t.Fatalf("render without updates = %q, want the file back as %q", got, normalize(input))
		}

		const key = "fuzz/pkg"
		const value = 12.5
		updates := map[string]float64{key: value}
		for k := range parsed.recorded {
			updates[k] = value
		}
		again, err := parseFloors(parsed.render(updates))
		if err != nil {
			t.Fatalf("a rendered file must parse: %v", err)
		}
		for k := range updates {
			if again.recorded[k] != value {
				t.Fatalf("recorded[%q] = %v, want %v", k, again.recorded[k], value)
			}
		}
		if len(again.pkg) != len(parsed.pkg) || again.global != parsed.global || again.module != parsed.module {
			t.Fatalf("a rendered file changed the gate it declares")
		}
	})
}
