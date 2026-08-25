package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/textx"
)

const testGo = "go1.26.3"

// embedded builds the information the toolchain embeds in a binary. Settings
// are key and value pairs, in the order the toolchain records them.
func embedded(mainVersion, goVersion string, settings ...string) *debug.BuildInfo {
	bi := &debug.BuildInfo{
		GoVersion: goVersion,
		Main:      debug.Module{Path: "github.com/stxkxs/agentfs", Version: mainVersion},
	}
	bi.Settings = make([]debug.BuildSetting, 0, len(settings)/2)
	for i := 0; i+1 < len(settings); i += 2 {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return bi
}

func TestResolve(t *testing.T) {
	t.Parallel()

	const fullRevision = "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		bi      *debug.BuildInfo
		want    Info
	}{
		{
			name:    "link-time stamps outrank embedded information",
			version: "v1.4.0",
			commit:  fullRevision,
			date:    "2026-08-24T13:00:00Z",
			bi: embedded("v0.9.0", testGo,
				"vcs.revision", "ffffffffffffffffffffffffffffffffffffffff",
				"vcs.time", "2020-01-01T00:00:00Z"),
			want: Info{
				Version:   "1.4.0",
				Commit:    "0123456789ab",
				BuildDate: "2026-08-24T13:00:00Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "module version of an installed binary",
			bi:   embedded("v1.4.0", testGo),
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "pseudo-version carries its own revision and time",
			bi:   embedded("v0.0.0-20260824130000-abcdef123456", testGo),
			want: Info{
				Version:   "0.0.0-20260824130000-abcdef123456",
				Commit:    "abcdef123456",
				BuildDate: "2026-08-24T13:00:00Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "pseudo-version after a tagged release",
			bi:   embedded("v1.2.4-0.20191109021931-daa7c04131f5", testGo),
			want: Info{
				Version:   "1.2.4-0.20191109021931-daa7c04131f5",
				Commit:    "daa7c04131f5",
				BuildDate: "2019-11-09T02:19:31Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "pseudo-version before a prerelease",
			bi:   embedded("v1.2.4-pre.0.20191109021931-daa7c04131f5", testGo),
			want: Info{
				Version:   "1.2.4-pre.0.20191109021931-daa7c04131f5",
				Commit:    "daa7c04131f5",
				BuildDate: "2019-11-09T02:19:31Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "checkout build reports its revision",
			bi: embedded(develVersion, testGo,
				"vcs.revision", fullRevision,
				"vcs.time", "2026-08-24T12:00:00Z",
				"vcs.modified", "false"),
			want: Info{
				Version:   DevVersion,
				Commit:    "0123456789ab",
				BuildDate: "2026-08-24T12:00:00Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "uncommitted changes mark the revision",
			bi: embedded(develVersion, testGo,
				"vcs.revision", fullRevision,
				"vcs.time", "2026-08-24T12:00:00Z",
				"vcs.modified", "true"),
			want: Info{
				Version:   DevVersion,
				Commit:    "0123456789ab" + dirtySuffix,
				BuildDate: "2026-08-24T12:00:00Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "no build information at all",
			want: Info{
				Version:   DevVersion,
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "embedded information without a toolchain version",
			bi:   embedded("v1.4.0", ""),
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "control sequences in a stamp are neutralized",
			version: "1.4.0\x1b[31m\nrm -rf",
			commit:  "\tabc1234\t",
			date:    " 2026-08-24T13:00:00Z ",
			want: Info{
				Version:   "1.4.0" + string(textx.EscapeMarker) + string(textx.Replacement) + "rm -rf",
				Commit:    "abc1234",
				BuildDate: "2026-08-24T13:00:00Z",
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "a date stamped as seconds since the epoch",
			version: "1.4.0",
			date:    "1756040400",
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: "2025-08-24T13:00:00Z",
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "a zoned date reports as UTC",
			version: "1.4.0",
			date:    "2026-08-24T15:00:00+02:00",
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: "2026-08-24T13:00:00Z",
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "an unreadable date falls through to the embedded stamp",
			version: "1.4.0",
			date:    "last tuesday",
			bi:      embedded(develVersion, testGo, "vcs.time", "2026-08-24T12:00:00Z"),
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: "2026-08-24T12:00:00Z",
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "an unreadable date with nothing to fall back to",
			version: "1.4.0",
			date:    "last tuesday",
			want: Info{
				Version:   "1.4.0",
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "a describe-style revision survives intact",
			version: "1.4.0",
			commit:  "v1.4.0-2-gabc1234",
			want: Info{
				Version:   "1.4.0",
				Commit:    "v1.4.0-2-gabc1234",
				BuildDate: Unknown,
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:    "an oversized stamp is bounded",
			version: strings.Repeat("9", maxField*2),
			want: Info{
				Version:   strings.Repeat("9", maxField-1) + textx.Ellipsis,
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: runtime.Version(),
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "an oversized dirty revision keeps its marker",
			bi: embedded(develVersion, testGo,
				"vcs.revision", strings.Repeat("z", maxField*2),
				"vcs.modified", "true"),
			want: Info{
				Version:   DevVersion,
				Commit:    strings.Repeat("z", maxField-len(dirtySuffix)-1) + textx.Ellipsis + dirtySuffix,
				BuildDate: Unknown,
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name:   "a stamped revision is reported as stamped over a modified tree",
			commit: fullRevision,
			bi: embedded("v1.4.0", testGo,
				"vcs.revision", "ffffffffffffffffffffffffffffffffffffffff",
				"vcs.modified", "true"),
			want: Info{
				Version:   "1.4.0",
				Commit:    "0123456789ab",
				BuildDate: Unknown,
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
		{
			name: "a blank module version reads as a development build",
			bi:   embedded("   ", testGo),
			want: Info{
				Version:   DevVersion,
				Commit:    Unknown,
				BuildDate: Unknown,
				GoVersion: testGo,
				Schema:    agentstate.SchemaVersion,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolve(tc.version, tc.commit, tc.date, tc.bi)
			if got != tc.want {
				t.Errorf("resolve()\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestGetReportsTheRunningBinary(t *testing.T) {
	t.Parallel()

	got := Get()
	if got.Schema != agentstate.SchemaVersion {
		t.Errorf("Schema = %q, want the contract version %q", got.Schema, agentstate.SchemaVersion)
	}
	if !strings.HasPrefix(got.GoVersion, "go") {
		t.Errorf("GoVersion = %q, want the toolchain that compiled the binary", got.GoVersion)
	}
	if got.Commit == "" || got.BuildDate == "" {
		t.Errorf("Commit = %q, BuildDate = %q: an absent fact reports %q", got.Commit, got.BuildDate, Unknown)
	}
	if again := Get(); again != got {
		t.Errorf("Get() = %+v on the second call, want the resolved answer %+v", again, got)
	}
}

// TestGetFallsBackToEmbeddedBuildInfo covers the path a binary takes when the
// build stamps nothing: every fact comes from what the toolchain embedded, and
// the version reported is a real answer rather than a blank.
func TestGetFallsBackToEmbeddedBuildInfo(t *testing.T) {
	t.Parallel()

	if version != "" || commit != "" || buildDate != "" {
		t.Skipf("binary stamped with version %q, commit %q, date %q", version, commit, buildDate)
	}

	got := Get()
	if got.Version == "" {
		t.Fatal("Version is empty: the embedded build information yielded nothing")
	}
	if got.Version != DevVersion {
		t.Errorf("Version = %q, want %q: a test binary records %q as its module version",
			got.Version, DevVersion, develVersion)
	}
}

func TestNormalizeTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "empty", in: ""},
		{name: "rfc 3339", in: "2026-08-24T13:00:00Z", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "rfc 3339 with fractional seconds", in: "2026-08-24T13:00:00.5Z", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "offset converts to utc", in: "2026-08-24T08:00:00-05:00", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "zoneless timestamp reads as utc", in: "2026-08-24T13:00:00", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "space separated timestamp", in: "2026-08-24 13:00:00", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "compact stamp", in: "20260824130000", want: "2026-08-24T13:00:00Z", wantOK: true},
		{name: "date only", in: "2026-08-24", want: "2026-08-24T00:00:00Z", wantOK: true},
		{name: "epoch seconds", in: "1756040400", want: "2025-08-24T13:00:00Z", wantOK: true},
		{name: "epoch zero", in: "0", want: "1970-01-01T00:00:00Z", wantOK: true},
		{name: "prose", in: "last tuesday"},
		{name: "epoch beyond the representable year range", in: "999999999999"},
		{name: "epoch that overflows an int64", in: "99999999999999999999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := normalizeTime(tc.in)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("normalizeTime(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPseudo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		in           string
		wantRevision string
		wantBuilt    string
	}{
		{name: "base pseudo-version", in: "0.0.0-20260824130000-abcdef123456", wantRevision: "abcdef123456", wantBuilt: "2026-08-24T13:00:00Z"},
		{name: "after a tag", in: "1.2.4-0.20191109021931-daa7c04131f5", wantRevision: "daa7c04131f5", wantBuilt: "2019-11-09T02:19:31Z"},
		{name: "release version", in: "1.4.0"},
		{name: "development version", in: DevVersion},
		{name: "revision of the wrong length", in: "0.0.0-20260824130000-abcdef12345"},
		{name: "revision that is not hexadecimal", in: "0.0.0-20260824130000-zzzzzzzzzzzz"},
		{name: "unreadable timestamp", in: "0.0.0-20261324130000-abcdef123456"},
		{name: "prerelease that only looks like one", in: "1.0.0-rc1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			revision, built := pseudo(tc.in)
			if revision != tc.wantRevision || built != tc.wantBuilt {
				t.Errorf("pseudo(%q) = %q, %q; want %q, %q", tc.in, revision, built, tc.wantRevision, tc.wantBuilt)
			}
		})
	}
}

func TestShortenRevision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "full revision", in: "0123456789abcdef0123456789abcdef01234567", want: "0123456789ab"},
		{name: "uppercase revision", in: "0123456789ABCDEF0123456789ABCDEF01234567", want: "0123456789AB"},
		{name: "already short", in: "abc1234", want: "abc1234"},
		{name: "exactly the short length", in: "0123456789ab", want: "0123456789ab"},
		{name: "describe output", in: "v1.4.0-2-gabc1234", want: "v1.4.0-2-gabc1234"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shortenRevision(tc.in); got != tc.want {
				t.Errorf("shortenRevision(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTrimV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "module version", in: "v1.4.0", want: "1.4.0"},
		{name: "bare version", in: "1.4.0", want: "1.4.0"},
		{name: "letter after the prefix", in: "vnext", want: "vnext"},
		{name: "prefix alone", in: "v", want: "v"},
		{name: "empty", in: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := trimV(tc.in); got != tc.want {
				t.Errorf("trimV(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := trimV(tc.want); again != tc.want {
				t.Errorf("trimV(%q) = %q on a second pass, want it unchanged", tc.want, again)
			}
		})
	}
}

func TestIsHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "digits", in: "0123456789", want: true},
		{name: "lowercase", in: "abcdef", want: true},
		{name: "uppercase", in: "ABCDEF", want: true},
		{name: "beyond hexadecimal", in: "abcdefg", want: false},
		{name: "with a separator", in: "abc-def", want: false},
		{name: "empty", in: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isHex(tc.in); got != tc.want {
				t.Errorf("isHex(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanBoundsAndNeutralizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "surrounding space", in: "  1.4.0\n", want: "1.4.0" + string(textx.Replacement)},
		{name: "escape sequence", in: "\x1b[2J1.4.0", want: "\u238b1.4.0"},
		{name: "tab expands and trims", in: "\t1.4.0", want: "1.4.0"},
		{name: "invalid utf-8", in: "1.4.0\xff", want: "1.4.0" + string(textx.Replacement)},
		{name: "bounded", in: strings.Repeat("a", maxField+10), want: strings.Repeat("a", maxField-1) + textx.Ellipsis},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := clean(tc.in)
			if got != tc.want {
				t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if w := textx.Width(got); w > maxField {
				t.Errorf("clean(%q) is %d cells wide, want at most %d", tc.in, w, maxField)
			}
		})
	}
}

func TestVcsStampsReadsEverySetting(t *testing.T) {
	t.Parallel()

	bi := embedded(develVersion, testGo,
		"-compiler", "gc",
		"vcs.revision", "abc",
		"vcs.time", "2026-08-24T13:00:00Z",
		"vcs.modified", "true",
		"GOARCH", "arm64")

	want := stamps{revision: "abc", built: "2026-08-24T13:00:00Z", modified: true}
	if got := vcsStamps(bi); got != want {
		t.Errorf("vcsStamps() = %+v, want %+v", got, want)
	}
	if got := vcsStamps(nil); got != (stamps{}) {
		t.Errorf("vcsStamps(nil) = %+v, want the zero value", got)
	}
}

// TestEveryTimeLayoutKeepsItsDay holds the claim [rfc3339] rests on: a layout
// in timeLayouts carries a four-digit year, so an instant formatted with it and
// read back reports the same day. A layout added without a year would parse to
// year zero and report a date no build was made on.
func TestEveryTimeLayoutKeepsItsDay(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, time.August, 24, 13, 0, 0, 0, time.UTC)
	for _, layout := range timeLayouts {
		stamp := instant.Format(layout)
		got, ok := normalizeTime(stamp)
		if !ok {
			t.Errorf("normalizeTime(%q), formatted with layout %q, was not read", stamp, layout)
			continue
		}
		at, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Errorf("layout %q: normalizeTime(%q) = %q, which is not RFC 3339: %v", layout, stamp, got, err)
			continue
		}
		if at.Year() != instant.Year() || at.YearDay() != instant.YearDay() {
			t.Errorf("layout %q: normalizeTime(%q) = %q, want the day %s",
				layout, stamp, got, instant.Format(time.DateOnly))
		}
		if again, ok := normalizeTime(got); !ok || again != got {
			t.Errorf("normalizeTime(%q) = %q, %v on a second pass, want it unchanged", got, again, ok)
		}
	}
}

// TestGetIsSharedAcrossGoroutines holds [Get]'s claim that the identity is
// resolved once and shared: concurrent callers receive one answer, and under
// the race detector they reach it through one synchronized read.
func TestGetIsSharedAcrossGoroutines(t *testing.T) {
	t.Parallel()

	const callers = 32
	got := make([]Info, callers)

	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range got {
		go func() {
			defer wg.Done()
			got[i] = Get()
		}()
	}
	wg.Wait()

	for i, info := range got {
		if info != got[0] {
			t.Fatalf("caller %d got %+v, want the one resolved identity %+v", i, info, got[0])
		}
	}
}
