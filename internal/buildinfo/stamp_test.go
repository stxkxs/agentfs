package buildinfo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// stampProbeEnv names the variable that turns TestLinkTimeStampsReachTheIdentity
// into the assertion it runs inside the stamped binary. A run without it set
// takes the branch that produces that binary, so the two halves of the test are
// one function and cannot drift apart.
const stampProbeEnv = "AGENTFS_BUILDINFO_STAMP_PROBE"

// The facts the probe binary is stamped with. Each differs from the others in
// form as well as value: a version no revision could be mistaken for, a full
// revision that must come back shortened, and a date already in the reported
// form. A resolution that crossed two stamps reports something the probe
// rejects.
const (
	probeVersion = "9.9.9-probe"
	probeCommit  = "0123456789abcdef0123456789abcdef01234567"
	probeShort   = "0123456789ab"
	probeDate    = "2019-11-09T02:19:31Z"
)

// TestLinkTimeStampsReachTheIdentity builds this package with the -X flags the
// package documents and asserts the identity that build reports.
//
// The linker accepts an -X flag naming a variable that does not exist and
// applies nothing, so a rename of version, commit or buildDate leaves the
// documented release command silently stamping nothing. No assertion made
// inside an unstamped test binary can observe that, because the path under
// test is chosen at link time.
func TestLinkTimeStampsReachTheIdentity(t *testing.T) {
	if os.Getenv(stampProbeEnv) != "" {
		assertStampedIdentity(t)
		return
	}

	if testing.Short() {
		t.Skip("stamping a binary invokes the go toolchain")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain to stamp a binary with: %v", err)
	}

	const pkg = "github.com/stxkxs/agentfs/internal/buildinfo"
	ldflags := fmt.Sprintf("-X %[1]s.version=%[2]s -X %[1]s.commit=%[3]s -X %[1]s.buildDate=%[4]s",
		pkg, probeVersion, probeCommit, probeDate)
	bin := filepath.Join(t.TempDir(), "stamped.test")

	build := exec.CommandContext(t.Context(), goTool, "test", "-c", "-o", bin, "-ldflags", ldflags, pkg)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go test -c -ldflags %q: %v\n%s", ldflags, err, out)
	}

	probe := exec.CommandContext(t.Context(), bin, "-test.run", "^"+t.Name()+"$", "-test.v")
	probe.Env = append(os.Environ(), stampProbeEnv+"=1")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("the stamped binary rejected its own identity: %v\n%s", err, out)
	}
}

// assertStampedIdentity runs inside the stamped binary and holds what [Get]
// reports against what the linker was given.
func assertStampedIdentity(t *testing.T) {
	t.Helper()

	if version != probeVersion || commit != probeCommit || buildDate != probeDate {
		t.Fatalf("the linker left the stamps as version %q, commit %q, date %q; want %q, %q, %q",
			version, commit, buildDate, probeVersion, probeCommit, probeDate)
	}

	got := Get()
	want := Info{
		Version:   probeVersion,
		Commit:    probeShort,
		BuildDate: probeDate,
		GoVersion: got.GoVersion,
		Schema:    agentstate.SchemaVersion,
	}
	if got != want {
		t.Fatalf("Get()\n got %+v\nwant %+v", got, want)
	}
	if !strings.HasPrefix(got.GoVersion, "go") {
		t.Errorf("GoVersion = %q, want the toolchain that compiled the binary", got.GoVersion)
	}

	line := got.String()
	for _, fact := range []string{probeVersion, probeShort, probeDate} {
		if !strings.Contains(line, fact) {
			t.Errorf("String() = %q, want it to carry %q", line, fact)
		}
	}
}
