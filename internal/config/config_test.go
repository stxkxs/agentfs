package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

func TestDefaultsAreTheDocumentedNumbers(t *testing.T) {
	d := Defaults()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Root", d.Root, "."},
		{"Watch", d.Watch, ModeAuto},
		{"MaxDepth", d.MaxDepth, 32},
		{"MaxEntriesPerDir", d.MaxEntriesPerDir, 5000},
		{"MaxNodes", d.MaxNodes, 200000},
		{"MaxWatches", d.MaxWatches, 8192},
		{"MaxBatch", d.MaxBatch, 4096},
		{"MaxQueue", d.MaxQueue, 8192},
		{"MaxPreviewBytes", d.MaxPreviewBytes, int64(1 << 20)},
		{"MaxDocumentBytes", d.MaxDocumentBytes, int64(1 << 20)},
		{"MaxExtraBytes", d.MaxExtraBytes, int64(64 << 10)},
		{"MaxFeedEntries", d.MaxFeedEntries, 2000},
		{"MaxDiagnostics", d.MaxDiagnostics, 500},
		{"SweepBudget", d.SweepBudget, 512},
		{"SweepInterval", d.SweepInterval, 2 * time.Second},
		{"DedupTTL", d.DedupTTL, 2 * time.Second},
		{"SkewTolerance", d.SkewTolerance, 5 * time.Second},
		{"StaleAfter", d.StaleAfter, 90 * time.Second},
		{"RootRetryMin", d.RootRetryMin, time.Second},
		{"RootRetryMax", d.RootRetryMax, 30 * time.Second},
		{"Strict", d.Strict, false},
		{"Color", d.Color, ColorAuto},
		{"ASCII", d.ASCII, false},
		{"RedactKeys", d.RedactKeys, []string{
			"api_key", "apikey", "authorization", "cookie",
			"credential", "password", "secret", "token",
		}},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("Defaults().%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

// The contract package reads documents under limits of its own. A workspace
// read through agentfs and one read through [agentstate] must be read under the
// same ones.
func TestDefaultsAgreeWithTheContract(t *testing.T) {
	d := Defaults()
	if d.MaxExtraBytes != agentstate.DefaultMaxExtraBytes {
		t.Errorf("MaxExtraBytes = %d, contract says %d", d.MaxExtraBytes, agentstate.DefaultMaxExtraBytes)
	}
	if d.SkewTolerance != agentstate.DefaultSkewTolerance {
		t.Errorf("SkewTolerance = %v, contract says %v", d.SkewTolerance, agentstate.DefaultSkewTolerance)
	}
}

// Two summaries state a default as a ratio of another rather than as a number,
// which keeps the reference free of a figure that goes stale. The ratios are
// held here so the prose cannot drift from the table.
func TestDefaultsHoldTheRatiosTheSummariesState(t *testing.T) {
	d := Defaults()
	if d.MaxQueue != 2*d.MaxBatch {
		t.Errorf("MaxQueue = %d, want two batches of %d", d.MaxQueue, d.MaxBatch)
	}
	if d.MaxDocumentBytes != d.MaxPreviewBytes {
		t.Errorf("MaxDocumentBytes = %d, want the preview ceiling of %d", d.MaxDocumentBytes, d.MaxPreviewBytes)
	}
}

// clobbered is the value a test writes into memory it was handed, to show the
// next caller does not read it back.
const clobbered = "clobbered"

func TestDefaultsRedactKeysAreOwnedByTheCaller(t *testing.T) {
	a := Defaults()
	a.RedactKeys[0] = clobbered
	if b := Defaults(); b.RedactKeys[0] == clobbered {
		t.Fatalf("Defaults() shares its RedactKeys backing array: %v", b.RedactKeys)
	}
	if got := Defaults().RedactKeys[0]; got != "api_key" {
		t.Errorf("Defaults().RedactKeys[0] = %q, want %q", got, "api_key")
	}
}

func TestStringReportsOnlyWhatDiffers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name:   "defaults",
			mutate: func(*Config) {},
			want:   "defaults",
		},
		{
			name:   "path",
			mutate: func(c *Config) { c.Root = "/srv/agents" },
			want:   "root=/srv/agents",
		},
		{
			name:   "enum",
			mutate: func(c *Config) { c.Watch = ModeSweep },
			want:   "watch=sweep",
		},
		{
			name:   "bytes render with a binary suffix",
			mutate: func(c *Config) { c.MaxPreviewBytes = 8 << 20 },
			want:   "max-preview-bytes=8MiB",
		},
		{
			name:   "count",
			mutate: func(c *Config) { c.MaxNodes = 10 },
			want:   "max-nodes=10",
		},
		{
			name:   "duration",
			mutate: func(c *Config) { c.SweepInterval = 5 * time.Second },
			want:   "sweep-interval=5s",
		},
		{
			name:   "bool",
			mutate: func(c *Config) { c.Strict = true },
			want:   "strict=true",
		},
		{
			name:   "list",
			mutate: func(c *Config) { c.RedactKeys = []string{"token", "pin"} },
			want:   "redact-keys=token,pin",
		},
		{
			name:   "empty list is quoted so the pair still splits",
			mutate: func(c *Config) { c.RedactKeys = nil },
			want:   `redact-keys=""`,
		},
		{
			name:   "a value with spacing is quoted",
			mutate: func(c *Config) { c.Root = "/srv/two words" },
			want:   `root="/srv/two words"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			tt.mutate(&c)
			got := c.String()
			if !strings.Contains(got, tt.want) {
				t.Errorf("String() = %q, want it to contain %q", got, tt.want)
			}
			if tt.want != "defaults" && strings.Contains(got, "defaults") {
				t.Errorf("String() = %q, want no mention of defaults", got)
			}
		})
	}
}

// The doctor line is written to a terminal, and a root path is arbitrary bytes.
func TestStringNeutralizesTerminalControls(t *testing.T) {
	c := Defaults()
	c.Root = "/srv/\x1b[31magents\x07"
	got := c.String()
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("String() = %q, want the escape and the bell removed", got)
	}
	if !strings.Contains(got, "agents") {
		t.Errorf("String() = %q, want the readable text kept", got)
	}
}

func TestStringOrdersPairsByTable(t *testing.T) {
	c := Defaults()
	c.ASCII = true
	c.Root = "/srv/agents"
	c.SweepInterval = time.Second
	got := c.String()
	want := []string{"root=", "sweep-interval=", "ascii="}
	at := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 {
			t.Fatalf("String() = %q, want it to contain %q", got, w)
		}
		if i < at {
			t.Fatalf("String() = %q, want %q after the preceding pair", got, w)
		}
		at = i
	}
}

func TestStringReportsEveryDifference(t *testing.T) {
	c := Defaults()
	c.Strict = true
	c.ASCII = true
	c.Color = ColorNever
	got := c.String()
	for _, want := range []string{"strict=true", "ascii=true", "color=never"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"", `""`},
		{"two words", `"two words"`},
		{"tab\there", `"tab\there"`},
		{`say "hi"`, `"say \"hi\""`},
		{"a,b", "a,b"},
	}
	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
