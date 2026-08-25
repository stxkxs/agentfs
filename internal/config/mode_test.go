package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/fsx"
)

func TestModeString(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{ModeAuto, "auto"},
		{ModeNotify, "notify"},
		{ModeSweep, "sweep"},
		{ModeHybrid, "hybrid"},
		{Mode(9), "mode(9)"},
		{Mode(-1), "mode(-1)"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("Mode(%d).String() = %q, want %q", int(tt.mode), got, tt.want)
		}
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Mode
		wantErr bool
	}{
		{name: "canonical", in: "notify", want: ModeNotify},
		{name: "auto", in: "auto", want: ModeAuto},
		{name: "sweep", in: "sweep", want: ModeSweep},
		{name: "hybrid", in: "hybrid", want: ModeHybrid},
		{name: "case is not significant", in: "HyBrId", want: ModeHybrid},
		{name: "surrounding space is trimmed", in: "  sweep\n", want: ModeSweep},
		{name: "empty", in: "", wantErr: true},
		{name: "misspelling", in: "notfiy", wantErr: true},
		{name: "interior space", in: "sw eep", wantErr: true},
		{name: "the rendered form of an undefined mode", in: "mode(9)", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %v, want an error", tt.in, got)
				}
				if !errors.Is(err, ErrUnknownMode) {
					t.Errorf("ParseMode(%q) error = %v, want it to wrap ErrUnknownMode", tt.in, err)
				}
				if got != ModeAuto {
					t.Errorf("ParseMode(%q) = %v on error, want the zero mode", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseMode(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// The error has to name what the operator may write instead, because the flag
// it comes from decides whether changes are observed at all.
func TestParseModeErrorNamesThePermittedValues(t *testing.T) {
	_, err := ParseMode("poll")
	if err == nil {
		t.Fatal("ParseMode(\"poll\") returned no error")
	}
	for _, name := range modeNames {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %q", err, name)
		}
	}
	if !strings.Contains(err.Error(), `"poll"`) {
		t.Errorf("error %q does not quote the rejected spelling", err)
	}
}

func FuzzParseMode(f *testing.F) {
	seeds := []string{"", "auto", "NOTIFY", " sweep ", "hybrid\x00", "mode(2)", "autö", "\x1b[31mauto"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseMode(s)
		if err != nil {
			if !errors.Is(err, ErrUnknownMode) {
				t.Fatalf("ParseMode(%q) error = %v, want it to wrap ErrUnknownMode", s, err)
			}
			if m != ModeAuto {
				t.Fatalf("ParseMode(%q) = %v on error, want the zero mode", s, m)
			}
			return
		}
		if m < ModeAuto || m > ModeHybrid {
			t.Fatalf("ParseMode(%q) = %v, outside the defined set", s, m)
		}
		// Accepting a spelling means the rendered form of the result parses
		// back to it, so a mode survives a round trip through a config file.
		back, err := ParseMode(m.String())
		if err != nil || back != m {
			t.Fatalf("ParseMode(%q).String() = %q, which parses as (%v, %v)", s, m.String(), back, err)
		}
	})
}

func TestFilesystemModeResolvesAuto(t *testing.T) {
	tests := []struct {
		name string
		kind fsx.Kind
		want Mode
	}{
		{"local filesystems deliver every event", fsx.KindLocal, ModeNotify},
		{"a network export is written by other clients", fsx.KindNetwork, ModeHybrid},
		{"a fuse mount hides changes made behind it", fsx.KindFuse, ModeHybrid},
		{"an unrecognized filesystem is treated as incomplete", fsx.KindUnknown, ModeHybrid},
		{"a kind this build does not define", fsx.Kind(99), ModeHybrid},
	}
	c := Defaults()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.FilesystemMode(tt.kind); got != tt.want {
				t.Errorf("FilesystemMode(%v) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestFilesystemModeKeepsAnExplicitChoice(t *testing.T) {
	kinds := []fsx.Kind{fsx.KindLocal, fsx.KindNetwork, fsx.KindFuse, fsx.KindUnknown}
	for _, want := range []Mode{ModeNotify, ModeSweep, ModeHybrid} {
		c := Defaults()
		c.Watch = want
		for _, k := range kinds {
			if got := c.FilesystemMode(k); got != want {
				t.Errorf("Watch=%v on %v resolved to %v, want it unchanged", want, k, got)
			}
		}
	}
}

// Whatever the probe reports, the strategy handed to the watcher is one that
// can be run: auto is a request, not a mechanism.
func TestFilesystemModeNeverResolvesToAuto(t *testing.T) {
	c := Defaults()
	for k := fsx.Kind(0); k < 8; k++ {
		if got := c.FilesystemMode(k); got == ModeAuto {
			t.Errorf("FilesystemMode(%v) = %v", k, got)
		}
	}
}
