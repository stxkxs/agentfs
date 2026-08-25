package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/textx"
)

// FuzzResolve holds the identity contract against arbitrary stamps: every fact
// is present, fits on a line, and carries nothing a terminal would act on.
func FuzzResolve(f *testing.F) {
	f.Add("v1.4.0", "0123456789abcdef0123456789abcdef01234567", "2026-08-24T13:00:00Z",
		"v1.4.0", "go1.26.3", "0123456789abcdef0123456789abcdef01234567", "2026-08-24T12:00:00Z", "false")
	f.Add("", "", "", "v0.0.0-20260824130000-abcdef123456", "go1.26.3", "", "", "")
	f.Add("", "", "", develVersion, "go1.26.3", "abcdef", "1756040400", "true")
	f.Add("\x1b]0;title\x07", "\t\n", "last tuesday", "\xff\xfe", "", "\x1b[2J", "99999999999999999999", "TRUE")
	f.Add(strings.Repeat("9", 300), strings.Repeat("a", 300), strings.Repeat("0", 300),
		strings.Repeat("v", 300), strings.Repeat("g", 300), strings.Repeat("f", 300), strings.Repeat("1", 300), "true")

	f.Fuzz(func(t *testing.T,
		stampedVersion, stampedCommit, stampedDate,
		mainVersion, goVersion, revision, vcsTime, modified string,
	) {
		bi := &debug.BuildInfo{
			GoVersion: goVersion,
			Main:      debug.Module{Path: "github.com/stxkxs/agentfs", Version: mainVersion},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: revision},
				{Key: "vcs.time", Value: vcsTime},
				{Key: "vcs.modified", Value: modified},
			},
		}
		got := resolve(stampedVersion, stampedCommit, stampedDate, bi)

		for _, field := range []struct{ name, value string }{
			{"Version", got.Version},
			{"Commit", got.Commit},
			{"BuildDate", got.BuildDate},
			{"GoVersion", got.GoVersion},
			{"Schema", got.Schema},
		} {
			if field.value == "" {
				t.Errorf("%s is empty, want a fact or %q", field.name, Unknown)
			}
			if strings.ContainsAny(field.value, "\n\r\x1b") {
				t.Errorf("%s = %q, want no line break or escape", field.name, field.value)
			}
			if w := textx.Width(field.value); w > maxField {
				t.Errorf("%s is %d cells wide, want at most %d", field.name, w, maxField)
			}
		}

		if got.BuildDate != Unknown {
			if _, err := time.Parse(time.RFC3339, got.BuildDate); err != nil {
				t.Errorf("BuildDate = %q, want RFC 3339 or %q: %v", got.BuildDate, Unknown, err)
			}
		}

		line := got.String()
		if strings.ContainsAny(line, "\n\r") {
			t.Errorf("String() = %q, want one line", line)
		}
		if !strings.HasPrefix(line, Name+" ") {
			t.Errorf("String() = %q, want it to open with %q", line, Name)
		}
		if n := len(strings.Split(got.Long(), "\n")); n != 5 {
			t.Errorf("Long() has %d lines, want one heading and four facts", n)
		}

		// Reporting an identity back as the stamps of another build reports the
		// same identity, so a stamp copied from --version into a release
		// pipeline cannot drift on the way through.
		again := resolve(got.Version, got.Commit, got.BuildDate, nil)
		if again.Version != got.Version || again.Commit != got.Commit || again.BuildDate != got.BuildDate {
			t.Errorf("resolving %+v again gave %+v", got, again)
		}
	})
}

// FuzzNormalizeTime holds the build date to one form: whatever a pipeline
// stamps, a reported date is RFC 3339 in UTC and reading it again changes
// nothing.
func FuzzNormalizeTime(f *testing.F) {
	for _, seed := range []string{
		"", "2026-08-24T13:00:00Z", "2026-08-24T13:00:00.123456789+02:00",
		"2026-08-24 13:00:00", "20260824130000", "2026-08-24", "1756040400",
		"0", "-1", "99999999999999999999", "999999999999", "last tuesday", "\x00",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got, ok := normalizeTime(in)
		if !ok {
			if got != "" {
				t.Errorf("normalizeTime(%q) = %q with ok false, want an empty result", in, got)
			}
			return
		}
		at, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("normalizeTime(%q) = %q, which is not RFC 3339: %v", in, got, err)
		}
		if at.Location() != time.UTC {
			t.Errorf("normalizeTime(%q) = %q, want UTC", in, got)
		}
		again, ok := normalizeTime(got)
		if !ok || again != got {
			t.Errorf("normalizeTime(%q) = %q, %v on a second pass, want it unchanged", got, again, ok)
		}
	})
}
