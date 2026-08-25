package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/cli"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/report"
)

// clock is the instant every command runs at, so a result records what the
// command does rather than when the test ran.
var clock = time.Date(2026, 4, 8, 13, 0, 0, 0, time.UTC)

// result is what one invocation produced.
type result struct {
	code   report.Code
	stdout string
	stderr string
}

// invoke runs one command line with a captured environment, which is what lets
// every command including the watching one be exercised without a terminal.
//
// The context is cancelled once the command returns, and a command that
// observes over time is given a deadline: it ends the way a signal ends it
// rather than running for as long as the test binary does.
func invoke(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var out, errs strings.Builder
	code := cli.Run(ctx, cli.Env{
		Args:   append([]string{"agentfs"}, args...),
		Stdout: &out,
		Stderr: &errs,
		Getenv: func(k string) string { return env[k] },
		Now:    func() time.Time { return clock },
	})
	return result{code: code, stdout: out.String(), stderr: errs.String()}
}

// workspace writes a fixture workspace and returns its path. Commands resolve a
// real path, so this is one of the few places a test needs a real directory.
func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func conformant(t *testing.T) string {
	t.Helper()
	return workspace(t, map[string]string{
		"agent-researcher/state.json":   `{"schema":"agentfs/v1","status":"running","task":"retrieval","step":3}`,
		"agent-writer/state.json":       `{"schema":"agentfs/v1","status":"idle"}`,
		"agent-researcher/logs/run.log": "2026-04-08 12:59:59 INFO working\n",
	})
}

func TestNoArgumentsPrintsUsageAndExitsUsage(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil)

	if got.code != report.CodeUsage {
		t.Errorf("exit = %d, want %d", got.code, report.CodeUsage)
	}
	if !strings.Contains(got.stderr, "usage: agentfs") {
		t.Errorf("stderr does not carry usage:\n%s", got.stderr)
	}
}

// The previous binary stat'd `--help` as a path and reported it was not a
// directory.
func TestHelpExitsOKAndListsEveryCommand(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"--help", "-h", "help"} {
		got := invoke(t, nil, form)
		if got.code != report.CodeOK {
			t.Errorf("%s exited %d, want 0", form, got.code)
		}
		for _, cmd := range cli.Commands() {
			if !strings.Contains(got.stdout, cmd.Name) {
				t.Errorf("%s does not list the command %q", form, cmd.Name)
			}
		}
		if !strings.Contains(got.stdout, "exit codes") {
			t.Errorf("%s does not state the exit codes", form)
		}
	}
}

func TestHelpForOneCommandNamesItsUsage(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "help", "validate")

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d", got.code)
	}
	if !strings.Contains(got.stdout, "usage: agentfs validate") {
		t.Errorf("stdout does not carry the command's usage:\n%s", got.stdout)
	}
}

func TestVersionReportsTheContractItImplements(t *testing.T) {
	t.Parallel()

	text := invoke(t, nil, "version")
	if text.code != report.CodeOK {
		t.Fatalf("exit = %d", text.code)
	}
	if !strings.Contains(text.stdout, agentstate.SchemaVersion) {
		t.Errorf("version does not name the contract:\n%s", text.stdout)
	}

	asJSON := invoke(t, nil, "version", "-format", "json")
	var env struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		Data   struct {
			Schema string `json:"Schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(asJSON.stdout), &env); err != nil {
		t.Fatalf("version --format json is not JSON: %v\n%s", err, asJSON.stdout)
	}
	if env.Schema != report.EnvelopeSchema {
		t.Errorf("envelope schema = %q, want %q", env.Schema, report.EnvelopeSchema)
	}
}

func TestSchemaPrintsTheContract(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "schema")

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d", got.code)
	}
	want, err := agentstate.SchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got.stdout != string(want) {
		t.Error("the printed schema differs from the one the decoder is built on")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &doc); err != nil {
		t.Fatalf("the printed schema is not JSON: %v", err)
	}
	if doc["$id"] != agentstate.SchemaID {
		t.Errorf("$id = %v, want %q", doc["$id"], agentstate.SchemaID)
	}
}

func TestScanReportsWhatTheAgentsDeclare(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", conformant(t))

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d, want 0\n%s%s", got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{"agent-researcher", "running", "retrieval", "agent-writer", "idle"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("scan output does not mention %q:\n%s", want, got.stdout)
		}
	}
}

func TestScanJSONCarriesTheEnvelope(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", "-format", "json", conformant(t))

	var env struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		Exit   int    `json:"exit"`
		Data   struct {
			Agents []struct {
				Name     string `json:"name"`
				Presence string `json:"presence"`
				State    struct {
					Status string `json:"status"`
				} `json:"state"`
			} `json:"agents"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got.stdout)
	}
	if env.Schema != report.EnvelopeSchema || env.Kind != report.KindScan {
		t.Errorf("envelope = %q/%q", env.Schema, env.Kind)
	}
	if env.Exit != int(got.code) {
		t.Errorf("the envelope reports exit %d and the process exited %d", env.Exit, got.code)
	}
	if len(env.Data.Agents) != 2 {
		t.Fatalf("scan found %d agents, want 2", len(env.Data.Agents))
	}
	// Presence and status encode as words, so adding a value cannot change what
	// an existing number means.
	for _, a := range env.Data.Agents {
		if a.Presence != "declared" {
			t.Errorf("%s presence = %q, want declared", a.Name, a.Presence)
		}
	}
}

// A findings status and a path status are different facts, so a caller can
// retry a mount problem without retrying a malformed document.
func TestValidateSeparatesFindingsFromAPathFailure(t *testing.T) {
	t.Parallel()

	clean := invoke(t, nil, "validate", conformant(t))
	if clean.code != report.CodeOK {
		t.Errorf("a conformant workspace exited %d\n%s", clean.code, clean.stdout)
	}

	broken := invoke(t, nil, "validate", workspace(t, map[string]string{
		"agent-a/state.json": `{"schema":"agentfs/v1","status":"not running"}`,
	}))
	if broken.code != report.CodeFindings {
		t.Errorf("a workspace with findings exited %d, want %d", broken.code, report.CodeFindings)
	}
	if !strings.Contains(broken.stdout, "AFS3002") {
		t.Errorf("the finding was not reported:\n%s", broken.stdout)
	}

	missing := invoke(t, nil, "validate", filepath.Join(t.TempDir(), "absent"))
	if missing.code != report.CodePath {
		t.Errorf("an unreadable workspace exited %d, want %d", missing.code, report.CodePath)
	}
}

// A warning is a finding to read, not a gate to fail.
func TestValidateDoesNotFailOnWarningsAlone(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "validate", workspace(t, map[string]string{
		"agent-a/status.json": `{"status":"running"}`,
	}))

	if got.code != report.CodeOK {
		t.Fatalf("a workspace raising only warnings exited %d\n%s", got.code, got.stdout)
	}
	if !strings.Contains(got.stdout, "warnings") {
		t.Errorf("the warning count was not reported:\n%s", got.stdout)
	}
}

// A gate that checked only an agent's own document would pass a workspace whose
// recorded runs are malformed.
func TestValidateChecksRunDocuments(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "validate", workspace(t, map[string]string{
		"agent-a/state.json":              `{"schema":"agentfs/v1","status":"running"}`,
		"agent-a/runs/run-001/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
	}))

	if got.code != report.CodeFindings {
		t.Fatalf("exit = %d, want %d\n%s", got.code, report.CodeFindings, got.stdout)
	}
	if !strings.Contains(got.stdout, "runs/run-001/state.json") {
		t.Errorf("the run's document was not checked:\n%s", got.stdout)
	}
}

func TestValidateJSONCarriesEveryDiagnostic(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "validate", "-format", "json", workspace(t, map[string]string{
		"agent-a/state.json": `{"schema":"agentfs/v1","status":"nope","step":{},"updated_at":"soon"}`,
	}))

	var env struct {
		Exit        int `json:"exit"`
		Diagnostics []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Pointer  string `json:"pointer"`
			Hint     string `json:"hint"`
		} `json:"diagnostics"`
		Data struct {
			Errors int `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got.stdout)
	}
	if env.Data.Errors < 3 {
		t.Errorf("a document with three bad members reported %d errors", env.Data.Errors)
	}
	for _, d := range env.Diagnostics {
		if d.Code == "" || d.Severity == "" {
			t.Errorf("a diagnostic carries no code or severity: %+v", d)
		}
		if d.Severity == "error" && d.Hint == "" {
			t.Errorf("the error %s carries no hint, so a reader has nothing to apply", d.Code)
		}
	}
}

func TestDoctorReportsHowTheWorkspaceIsObserved(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "doctor", conformant(t))

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d\n%s", got.code, got.stderr)
	}
	for _, want := range []string{"filesystem", "detection", "confinement", "contract"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("doctor does not report %q:\n%s", want, got.stdout)
		}
	}
}

// The mode a workspace resolves to is a fact an operator reads rather than
// infers from an empty feed.
func TestDoctorReportsTheRequestedMode(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "doctor", "-format", "json", "-watch", "sweep", conformant(t))

	var env struct {
		Data struct {
			Mode        string `json:"mode"`
			SweepBudget int    `json:"sweep_budget"`
			OpsPerHour  int    `json:"operations_per_hour"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got.stdout)
	}
	if env.Data.Mode != "sweep" {
		t.Errorf("mode = %q, want sweep", env.Data.Mode)
	}
	if env.Data.OpsPerHour <= 0 {
		t.Error("a sweeping mode reports no sweep cost, so an operator cannot size it")
	}
}

// A mode that does not sweep spends no sweep operations, and reporting a figure
// would invite a capacity calculation against work that never happens.
func TestDoctorReportsNoSweepCostForNotify(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "doctor", "-format", "json", "-watch", "notify", conformant(t))

	var env struct {
		Data struct {
			OpsPerHour int `json:"operations_per_hour"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.OpsPerHour != 0 {
		t.Errorf("notify mode reports %d sweep operations per hour", env.Data.OpsPerHour)
	}
}

// A first argument that names no command is the workspace, watched. Without a
// terminal there is nothing to draw, so a pipe gets the record stream rather
// than an escape stream or a refusal.
func TestABareDirectoryIsWatched(t *testing.T) {
	t.Parallel()
	run := watchStream(t, conformant(t))
	defer run.stop()

	first := run.await(t, func(recs []report.Record) bool { return len(recs) > 0 })
	if first[0].Kind != report.RecordStatus {
		t.Errorf("the stream opens with a %q record, want %q", first[0].Kind, report.RecordStatus)
	}
}

func TestAMalformedInvocationExitsUsage(t *testing.T) {
	t.Parallel()
	ws := conformant(t)
	cases := []struct {
		name string
		args []string
	}{
		{"no directory", []string{"scan"}},
		{"two directories", []string{"scan", ws, ws}},
		{"unknown format", []string{"scan", "-format", "yaml", ws}},
		{"unknown watch mode", []string{"scan", "-watch", "telepathy", ws}},
		{"unknown flag", []string{"scan", "-nonsense", ws}},
		{"invalid ceiling", []string{"scan", "-max-nodes", "0", ws}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := invoke(t, nil, tc.args...)
			if got.code != report.CodeUsage {
				t.Errorf("exit = %d, want %d\n%s", got.code, report.CodeUsage, got.stderr)
			}
			if got.stderr == "" {
				t.Error("a refused invocation explained nothing")
			}
		})
	}
}

// A ceiling that fails validation is reported alongside every other, rather
// than one round trip per mistake.
func TestValidationReportsEveryBadSettingAtOnce(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", "-max-nodes", "0", "-max-depth", "0", conformant(t))

	if got.code != report.CodeUsage {
		t.Fatalf("exit = %d", got.code)
	}
	for _, want := range []string{"max-nodes", "max-depth"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not name %q:\n%s", want, got.stderr)
		}
	}
}

func TestEnvironmentSetsAFlag(t *testing.T) {
	t.Parallel()
	got := invoke(t, map[string]string{"AGENTFS_WATCH": "sweep"},
		"doctor", "-format", "json", conformant(t))

	var env struct {
		Data struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Mode != "sweep" {
		t.Errorf("mode = %q, want the environment's sweep", env.Data.Mode)
	}
}

// The more specific statement of intent is the later one.
func TestAFlagOverridesTheEnvironment(t *testing.T) {
	t.Parallel()
	got := invoke(t, map[string]string{"AGENTFS_WATCH": "sweep"},
		"doctor", "-format", "json", "-watch", "notify", conformant(t))

	var env struct {
		Data struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Mode != "notify" {
		t.Errorf("mode = %q, want the flag's notify", env.Data.Mode)
	}
}

// An exported value is ambient rather than asked for, so refusing to start
// because of an unrelated shell setting would be worse than the default.
func TestAnUnparseableEnvironmentValueIsIgnored(t *testing.T) {
	t.Parallel()
	got := invoke(t, map[string]string{"AGENTFS_MAX_NODES": "lots"}, "scan", conformant(t))

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d, want the command to run with the default\n%s", got.code, got.stderr)
	}
}

func TestEveryCommandAcceptsJSONAndEmitsAnEnvelope(t *testing.T) {
	t.Parallel()
	ws := conformant(t)

	for _, cmd := range cli.Commands() {
		if cmd.Name == "watch" {
			continue // Its output is a terminal frame rather than a result.
		}
		t.Run(cmd.Name, func(t *testing.T) {
			args := []string{cmd.Name, "-format", "json"}
			if cmd.NeedsRoot {
				args = append(args, ws)
			}
			got := invoke(t, nil, args...)

			if cmd.Name == "schema" {
				// The schema is the published contract, emitted as itself.
				var doc map[string]any
				if err := json.Unmarshal([]byte(got.stdout), &doc); err != nil {
					t.Fatalf("schema is not JSON: %v", err)
				}
				return
			}
			var env struct {
				Schema  string `json:"schema"`
				Kind    string `json:"kind"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
				t.Fatalf("%s --format json is not JSON: %v\n%s", cmd.Name, err, got.stdout)
			}
			if env.Schema != report.EnvelopeSchema {
				t.Errorf("%s envelope schema = %q", cmd.Name, env.Schema)
			}
			if env.Kind == "" || env.Version == "" {
				t.Errorf("%s envelope = %+v, want a kind and a version", cmd.Name, env)
			}
		})
	}
}

// The reciprocal of report.Kinds: every kind in the vocabulary is one a command
// emits, and every kind a command emits is in the vocabulary. A kind no command
// emits is a payload shape a consumer branches on and never reaches; a kind a
// command emits from outside the table is one no consumer knows to expect.
func TestEveryKindIsEmitted(t *testing.T) {
	t.Parallel()
	ws := conformant(t)

	produced := make(map[string]bool)
	collect := func(args []string, out string) {
		var env struct {
			Schema string `json:"schema"`
			Kind   string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || env.Schema != report.EnvelopeSchema {
			// The schema command emits the state contract rather than a
			// result, so there is no kind to collect from it.
			return
		}
		if !slices.Contains(report.Kinds(), env.Kind) {
			t.Errorf("%v emitted the kind %q, which is outside report.Kinds()", args, env.Kind)
		}
		produced[env.Kind] = true
	}

	for _, cmd := range cli.Commands() {
		if !slices.Contains(cmd.Formats, cli.FormatJSON) {
			continue
		}
		args := []string{cmd.Name, "-format", "json"}
		if cmd.NeedsRoot {
			args = append(args, ws)
		}
		collect(args, invoke(t, nil, args...).stdout)
	}

	// A command that could not produce its result reports that as a result of
	// its own, so a caller parsing standard output is never handed prose.
	failing := []string{"scan", "-format", "json", filepath.Join(t.TempDir(), "absent")}
	collect(failing, invoke(t, nil, failing...).stdout)

	for _, kind := range report.Kinds() {
		if !produced[kind] {
			t.Errorf("no command emits the kind %q, so a consumer branching on it waits forever", kind)
		}
	}
}

// A command that cannot read the workspace reports it in the form the caller
// asked for, with the finding a machine branches on rather than a line of
// prose on standard error.
func TestAnUnreadableWorkspaceIsAnErrorEnvelope(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", "-format", "json", filepath.Join(t.TempDir(), "absent"))

	if got.code != report.CodePath {
		t.Fatalf("exit = %d, want %d", got.code, report.CodePath)
	}
	var env struct {
		Kind        string `json:"kind"`
		Exit        int    `json:"exit"`
		Diagnostics []struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got.stdout)
	}
	if env.Kind != report.KindError {
		t.Errorf("kind = %q, want %q", env.Kind, report.KindError)
	}
	if env.Exit != int(report.CodePath) {
		t.Errorf("the envelope reports exit %d and the process exited %d", env.Exit, got.code)
	}
	if len(env.Diagnostics) != 1 || env.Diagnostics[0].Code != string(diag.CodeRootLost) {
		t.Fatalf("the envelope does not carry the root diagnostic: %+v", env.Diagnostics)
	}
	if env.Diagnostics[0].Hint == "" {
		t.Error("the finding carries no hint, so a reader has nothing to apply")
	}
	if got.stderr != "" {
		t.Errorf("a machine-readable refusal also wrote prose to stderr:\n%s", got.stderr)
	}
}

// Every declared command must be reachable, or the help text lists something
// that cannot be run. A command that observes over time is ended by the same
// cancellation a signal delivers.
func TestEveryDeclaredCommandRuns(t *testing.T) {
	t.Parallel()
	ws := conformant(t)

	for _, cmd := range cli.Commands() {
		args := []string{cmd.Name}
		if cmd.NeedsRoot {
			args = append(args, ws)
		}
		got := invoke(t, nil, args...)
		if got.code == report.CodeUsage {
			t.Errorf("%s exited usage against a valid invocation:\n%s", cmd.Name, got.stderr)
		}
		if got.code == report.CodeInternal {
			t.Errorf("%s exited internal:\n%s", cmd.Name, got.stderr)
		}
	}
}

// invokeTo runs one command line against a caller-supplied stdout, which is how
// a command's answer to a write that fails is exercised.
func invokeTo(t *testing.T, stdout io.Writer, args ...string) result {
	t.Helper()

	var errs strings.Builder
	code := cli.Run(context.Background(), cli.Env{
		Args:   append([]string{"agentfs"}, args...),
		Stdout: stdout,
		Stderr: &errs,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return clock },
	})
	return result{code: code, stderr: errs.String()}
}

// failingWriter refuses every write with one error, which separates a command's
// answer to a closed pipe from its answer to a fault.
type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// line returns the output line naming the agent.
func line(out, agent string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, agent+" ") {
			return l
		}
	}
	return ""
}

func TestTheVersionShorthandsPrintTheBuild(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"-v", "--version"} {
		got := invoke(t, nil, form)
		if got.code != report.CodeOK {
			t.Errorf("%s exited %d, want 0", form, got.code)
		}
		if !strings.Contains(got.stdout, agentstate.SchemaVersion) {
			t.Errorf("%s does not name the contract:\n%s", form, got.stdout)
		}
	}
}

// The shorthand form is an argument that names no command, which a flag in
// front of the directory does not change: the refusal is the watch command's
// own, naming the formats watch renders.
func TestAFlagBeforeABareDirectoryIsWatched(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "-format", "json", conformant(t))

	if got.code != report.CodeUsage {
		t.Fatalf("exit = %d, want %d\n%s", got.code, report.CodeUsage, got.stderr)
	}
	if !strings.Contains(got.stderr, "watch does not render") {
		t.Errorf("stderr does not explain the refusal:\n%s", got.stderr)
	}
}

// A topic that names no command leaves the reader the list to pick from rather
// than an error about the word they guessed.
func TestHelpForAnUnknownTopicListsEveryCommand(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "help", "nonsense")

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d", got.code)
	}
	if !strings.Contains(got.stdout, "usage: agentfs <command>") {
		t.Errorf("stdout does not carry the general usage:\n%s", got.stdout)
	}
	for _, cmd := range cli.Commands() {
		if !strings.Contains(got.stdout, cmd.Name) {
			t.Errorf("the list omits the command %q", cmd.Name)
		}
	}
}

// The flag list is a column: a description starts at one offset on every row and
// wraps within the help width, so a name is scanned down one edge and its text
// down another. A single token wider than the column has no word boundary to
// break on and stands alone.
func TestFlagHelpIsAColumn(t *testing.T) {
	t.Parallel()
	const column, width = 30, 96
	indent := strings.Repeat(" ", column)

	for _, l := range strings.Split(invoke(t, nil, "--help").stdout, "\n") {
		if n := len([]rune(l)); n > width && strings.Contains(strings.TrimSpace(l), " ") {
			t.Errorf("a help line runs to %d columns past %d with a break available:\n%s", n, width, l)
		}
		if !strings.HasPrefix(l, "  -") && !strings.HasPrefix(l, indent) {
			continue
		}
		if len(l) <= column || l[column-1] != ' ' || l[column] == ' ' {
			t.Errorf("a description does not begin at column %d:\n%q", column, l)
		}
	}
}

// The table opens a summary with the Go field name because it documents a
// struct. The flag list does not, because a Go identifier names nothing the
// reader can type. The subjects are every row of [config.Limits], so a field
// spelled as an acronym is held to the rule a camel-cased one is.
func TestNoFlagHelpOpensWithItsGoFieldName(t *testing.T) {
	t.Parallel()
	rendered := flagDescriptions(invoke(t, nil, "--help").stdout)

	for _, l := range config.Limits() {
		if l.Flag == "" || l.Flag == "root" {
			// The workspace is a positional argument, so the list has no row
			// for it.
			continue
		}
		text, listed := rendered[l.Flag]
		if !listed {
			t.Errorf("the flag list omits -%s", l.Flag)
			continue
		}
		opening, _, _ := strings.Cut(text, " ")
		switch {
		case opening == "":
			t.Errorf("-%s carries no description", l.Flag)
		case strings.EqualFold(strings.TrimRight(opening, ".,;:"), l.Name):
			t.Errorf("-%s opens with the Go field name %q: %s", l.Flag, l.Name, text)
		case opening == "Is":
			t.Errorf("-%s opens with the copula its field name left behind: %s", l.Flag, text)
		}
	}
}

// flagDescriptions reads the help text back into the description rendered
// against each flag, keyed by flag name. A description that wrapped is rejoined
// so an assertion reads the sentence rather than wherever the column broke it,
// and a name too wide for the column takes the following line's text.
func flagDescriptions(help string) map[string]string {
	const column = 30
	indent := strings.Repeat(" ", column)

	out := map[string]string{}
	flag := ""
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.HasPrefix(line, "  -"):
			flag, _, _ = strings.Cut(strings.TrimPrefix(line, "  -"), " ")
			out[flag] = ""
			if len(line) > column && line[column-1] == ' ' && line[column] != ' ' {
				out[flag] = strings.TrimSpace(line[column:])
			}
		case flag != "" && strings.HasPrefix(line, indent):
			out[flag] = strings.TrimSpace(out[flag] + " " + strings.TrimSpace(line))
		default:
			flag = ""
		}
	}
	return out
}

// A workspace declaring no agent is a result, not a failure, and saying so
// beats printing nothing.
func TestScanSaysSoWhenAWorkspaceDeclaresNoAgent(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", t.TempDir())

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "no agents in") {
		t.Errorf("stdout does not report an empty workspace:\n%q", got.stdout)
	}
}

// A finding belongs beside the agent that raised it, and an error-severity one
// is a gate a pipeline can act on.
func TestScanReportsAnAgentsOwnFindings(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", workspace(t, map[string]string{
		"agent-a/state.json": `{"schema":"agentfs/v1","status":"not running"}`,
	}))

	if got.code != report.CodeFindings {
		t.Fatalf("exit = %d, want %d\n%s%s", got.code, report.CodeFindings, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "AFS3002") {
		t.Errorf("the finding is not printed with its agent:\n%s", got.stdout)
	}
}

// The summary is the one line an operator reads to know what an agent is doing,
// so every member it can carry appears and the order does not shuffle.
func TestScanSummarisesEveryMemberAnAgentDeclares(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "scan", workspace(t, map[string]string{
		"agent-task/state.json":    `{"schema":"agentfs/v1","status":"running","task":"retrieval"}`,
		"agent-step/state.json":    `{"schema":"agentfs/v1","status":"running","step":"review"}`,
		"agent-model/state.json":   `{"schema":"agentfs/v1","status":"running","model":"opus"}`,
		"agent-problem/state.json": `{"schema":"agentfs/v1","status":"error","problem":"rate limited"}`,
		"agent-every/state.json":   `{"schema":"agentfs/v1","status":"error","task":"retrieval","step":4,"model":"opus","problem":"rate limited"}`,
	}))

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d\n%s%s", got.code, got.stdout, got.stderr)
	}
	cases := map[string]string{
		"agent-task":    "retrieval",
		"agent-step":    "step review",
		"agent-model":   "opus",
		"agent-problem": "problem: rate limited",
		"agent-every":   "retrieval · step 4 · opus · problem: rate limited",
	}
	for agent, want := range cases {
		l := line(got.stdout, agent)
		if l == "" {
			t.Errorf("%s is not reported:\n%s", agent, got.stdout)
			continue
		}
		if got := strings.TrimRight(l, " "); !strings.HasSuffix(got, want) {
			t.Errorf("%s summary = %q, want it to end %q", agent, got, want)
		}
	}
}

// A path that is not a directory is a path failure rather than a usage mistake,
// so a caller retries the mount without re-reading its command line.
func TestAFileIsNotAWorkspace(t *testing.T) {
	t.Parallel()
	file := filepath.Join(workspace(t, map[string]string{"notes.txt": "text"}), "notes.txt")

	for _, args := range [][]string{{"scan", file}, {"doctor", file}, {"validate", file}, {file}} {
		got := invoke(t, nil, args...)
		if got.code != report.CodePath {
			t.Errorf("%v exited %d, want %d", args, got.code, report.CodePath)
		}
		if !strings.Contains(got.stderr, "cannot read workspace") {
			t.Errorf("%v explained nothing:\n%s", args, got.stderr)
		}
	}
}

// The sweep cost is the figure an operator multiplies by the number of
// instances pointed at one shared export, so the text form carries it too.
func TestDoctorPrintsTheSweepCostInText(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "doctor", "-watch", "sweep", conformant(t))

	if got.code != report.CodeOK {
		t.Fatalf("exit = %d\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "sweep cost") {
		t.Errorf("a sweeping mode reports no sweep cost:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "filesystem operations per hour") {
		t.Errorf("the cost carries no unit:\n%s", got.stdout)
	}
}

// Findings are read as a list against the tree they came from, which is only
// navigable if they arrive in path order.
func TestValidateOrdersFindingsByPath(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "validate", workspace(t, map[string]string{
		"agent-zulu/state.json":  `{"schema":"agentfs/v1","status":"nope"}`,
		"agent-alpha/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
	}))

	if got.code != report.CodeFindings {
		t.Fatalf("exit = %d\n%s", got.code, got.stdout)
	}
	alpha := strings.Index(got.stdout, "agent-alpha")
	zulu := strings.Index(got.stdout, "agent-zulu")
	if alpha < 0 || zulu < 0 {
		t.Fatalf("both documents were not reported:\n%s", got.stdout)
	}
	if alpha > zulu {
		t.Errorf("findings are not in path order:\n%s", got.stdout)
	}
}

// A reader closing the pipe is a decision that it has enough, so the command
// keeps the code it reached and says nothing about the write.
func TestABrokenPipeKeepsTheCommandsCode(t *testing.T) {
	t.Parallel()
	clean, findings := conformant(t), workspace(t, map[string]string{
		"agent-a/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
	})
	cases := []struct {
		args []string
		want report.Code
	}{
		{[]string{"scan", clean}, report.CodeOK},
		{[]string{"scan", findings}, report.CodeFindings},
		{[]string{"scan", "-format", "json", findings}, report.CodeFindings},
		{[]string{"validate", findings}, report.CodeFindings},
		{[]string{"validate", "-format", "json", findings}, report.CodeFindings},
		{[]string{"doctor", clean}, report.CodeOK},
		{[]string{"doctor", "-format", "json", clean}, report.CodeOK},
		{[]string{"schema"}, report.CodeOK},
		{[]string{"version"}, report.CodeOK},
		{[]string{"version", "-format", "json"}, report.CodeOK},
	}
	for _, tc := range cases {
		got := invokeTo(t, failingWriter{err: syscall.EPIPE}, tc.args...)
		if got.code != tc.want {
			t.Errorf("%v exited %d against a closed pipe, want %d", tc.args, got.code, tc.want)
		}
		if got.stderr != "" {
			t.Errorf("%v reported a closed pipe as a fault:\n%s", tc.args, got.stderr)
		}
	}
}

// Any other write failure is one the operator needs told about, and the result
// it belonged to was not delivered.
func TestAWriteFailureIsReportedAndExitsInternal(t *testing.T) {
	t.Parallel()
	ws := conformant(t)
	fault := errors.New("device is on fire")
	cases := [][]string{
		{"scan", ws},
		{"scan", "-format", "json", ws},
		{"validate", ws},
		{"validate", "-format", "json", ws},
		{"doctor", ws},
		{"doctor", "-format", "json", ws},
		{"schema"},
		{"version"},
		{"version", "-format", "json"},
	}
	for _, args := range cases {
		got := invokeTo(t, failingWriter{err: fault}, args...)
		if got.code != report.CodeInternal {
			t.Errorf("%v exited %d against a failing writer, want %d", args, got.code, report.CodeInternal)
		}
		if !strings.Contains(got.stderr, fault.Error()) {
			t.Errorf("%v did not report the write failure:\n%s", args, got.stderr)
		}
	}
}

// An environment and a clock are optional, and a caller that supplies neither
// gets the process defaults rather than a panic.
func TestAnEnvironmentAndClockAreOptional(t *testing.T) {
	t.Parallel()

	var out, errs strings.Builder
	code := cli.Run(context.Background(), cli.Env{
		Args:   []string{"agentfs", "scan", conformant(t)},
		Stdout: &out,
		Stderr: &errs,
	})
	if code != report.CodeOK {
		t.Fatalf("exit = %d\n%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "agent-researcher") {
		t.Errorf("the workspace was not scanned:\n%s", out.String())
	}
}

// The flag promises a run that holds a workspace to the whole contract, so a
// warning has to fail it. A flag that changes nothing observable is the
// aspirational-comment defect wearing a command-line option.
func TestStrictPromotesAWarningToAFinding(t *testing.T) {
	t.Parallel()
	// A compatibility filename and an absent schema member raise warnings and
	// no error.
	ws := workspace(t, map[string]string{
		"agent-a/status.json": `{"status":"running"}`,
	})

	lenient := invoke(t, nil, "validate", ws)
	if lenient.code != report.CodeOK {
		t.Fatalf("a workspace raising only warnings exited %d\n%s", lenient.code, lenient.stdout)
	}

	strict := invoke(t, nil, "validate", "-strict", ws)
	if strict.code != report.CodeFindings {
		t.Fatalf("a strict run over the same workspace exited %d, want %d\n%s",
			strict.code, report.CodeFindings, strict.stdout)
	}
	if !strings.Contains(strict.stdout, "0 warnings") {
		t.Errorf("a strict run still reports warnings separately:\n%s", strict.stdout)
	}
}

// The flag names members whose values are masked before rendering, and a flag
// that masks nothing is worse than no flag: an operator relies on it.
func TestRedactKeysMasksAValueInThePreview(t *testing.T) {
	t.Parallel()
	// The mask is applied where a workspace's own bytes are rendered, so the
	// property is asserted against the preview loader the terminal uses.
	const secret = "s3cr3t-value"
	ws := workspace(t, map[string]string{
		"agent-a/state.json": `{"schema":"agentfs/v1","status":"running","token":"` + secret + `"}`,
	})

	root, err := fsx.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	w := fileview.Load(root, "agent-a/state.json", fileview.Options{
		RedactKeys: config.Defaults().RedactKeys,
	})
	var text string
	for _, line := range w.Lines() {
		text += line.Text
	}
	if strings.Contains(text, secret) {
		t.Fatalf("the default redaction list did not mask a token:\n%s", text)
	}
}

// hostile is what a workspace can put in a name or a declared value: a
// clipboard write, a screen clear, and a right-to-left override that renders
// gpj.exe as exe.jpg.
const hostile = "benign\x1b]52;c;SEFDS0VE\x07\x1b[2J\u202egpj.exe"

// Every command that prints workspace content is a path from a byte an agent
// wrote to the operator's terminal. The terminal interface neutralizes in its
// panes; these commands neutralize here, and the non-terminal path steers a
// caller straight at them.
func TestNoCommandWritesAnEscapeFromWorkspaceContent(t *testing.T) {
	t.Parallel()

	// Each fixture reaches a different field. A hostile directory name only
	// reaches Diagnostic.Path when that document also raises a diagnostic, and
	// a hostile member name only reaches Diagnostic.Pointer when the member is
	// one the contract does not define — so both documents are built to raise.
	hostileDir := "agent-" + strings.ReplaceAll(hostile, "/", "")
	ws := workspace(t, map[string]string{
		// Path: a hostile directory name, on a document that raises.
		hostileDir + "/state.json": `{"schema":"agentfs/v1","status":"running","updated_at":"not-a-time"}`,
		// Pointer: a hostile member name the contract does not define.
		"agent-b/state.json": `{"schema":"agentfs/v1","status":"running",` + jsonString(hostile) + `:1}`,
		// Message and Value: a hostile declared value quoted back.
		"agent-c/state.json": `{"schema":"agentfs/v1","status":` + jsonString(hostile) + `}`,
		// Task: a hostile value that decodes cleanly and is rendered.
		"agent-d/state.json": `{"schema":"agentfs/v1","status":"running","task":` + jsonString(hostile) + `}`,
	})

	for _, args := range [][]string{
		{"scan", ws},
		{"scan", "-format", "json", ws},
		{"validate", ws},
		{"validate", "-format", "json", ws},
		{"doctor", ws},
	} {
		t.Run(strings.Join(args[:len(args)-1], " "), func(t *testing.T) {
			t.Parallel()
			got := invoke(t, nil, args...)
			assertNoTerminalControl(t, "stdout", got.stdout)
			assertNoTerminalControl(t, "stderr", got.stderr)
		})
	}
}

// jsonString quotes s as JSON, so a fixture carries the runes it names rather
// than a Go escape sequence spelled out in ASCII.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// assertNoTerminalControl fails on any rune a terminal acts on rather than
// draws.
//
// The unsafe set is named by Unicode category, matching what textx.Sanitize
// neutralizes. An assertion listing the characters somebody thought of passes
// for every character nobody did, and would go green against a sanitizer
// narrowed back to an enumeration.
func assertNoTerminalControl(t *testing.T, where, out string) {
	t.Helper()
	for i, r := range out {
		switch {
		case r == 0x1B:
			t.Fatalf("%s carries an escape at byte %d:\n%q", where, i, out)
		case r < 0x20 && r != '\n' && r != '\t':
			t.Fatalf("%s carries the control U+%04X at byte %d:\n%q", where, r, i, out)
		case r == 0x7F, r >= 0x80 && r <= 0x9F:
			t.Fatalf("%s carries the control U+%04X at byte %d:\n%q", where, r, i, out)
		case unicode.In(r, unicode.Cf):
			t.Fatalf("%s carries the format character U+%04X at byte %d:\n%q", where, r, i, out)
		case unicode.Is(unicode.Cs, r):
			t.Fatalf("%s carries the surrogate U+%04X at byte %d:\n%q", where, r, i, out)
		case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
			t.Fatalf("%s carries the variation selector U+%04X at byte %d:\n%q", where, r, i, out)
		}
	}
}

// The fixture only proves the sanitizer when it actually reaches every field,
// so the routes it is meant to cover are asserted rather than assumed. A
// fixture that raises no diagnostic makes the whole assertion vacuous, which is
// how two live injections survived a green test.
func TestTheHostileFixtureReachesEveryDiagnosticField(t *testing.T) {
	t.Parallel()

	hostileDir := "agent-" + strings.ReplaceAll(hostile, "/", "")
	ws := workspace(t, map[string]string{
		hostileDir + "/state.json": `{"schema":"agentfs/v1","status":"running","updated_at":"not-a-time"}`,
		"agent-b/state.json":       `{"schema":"agentfs/v1","status":"running",` + jsonString(hostile) + `:1}`,
		"agent-c/state.json":       `{"schema":"agentfs/v1","status":` + jsonString(hostile) + `}`,
	})

	got := invoke(t, nil, "validate", "-format", "json", ws)
	var env struct {
		Diagnostics []struct {
			Path    string `json:"path"`
			Pointer string `json:"pointer"`
			Value   string `json:"value"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, got.stdout)
	}

	var reachedPath, reachedPointer, reachedValue bool
	for _, d := range env.Diagnostics {
		reachedPath = reachedPath || strings.HasPrefix(d.Path, "agent-benign")
		reachedPointer = reachedPointer || strings.Contains(d.Pointer, "benign")
		reachedValue = reachedValue || strings.Contains(d.Value, "benign")
	}
	if !reachedPath {
		t.Error("no diagnostic carries the hostile directory name, so Path is untested")
	}
	if !reachedPointer {
		t.Error("no diagnostic carries the hostile member name, so Pointer is untested")
	}
	if !reachedValue {
		t.Error("no diagnostic quotes the hostile value, so Value is untested")
	}
}

// A ceiling that is only rendered as a phrase on a terminal is invisible to
// every caller that is not a person. The command whose job is to report what
// agentfs can and cannot see in a workspace reports them as codes.
func TestDoctorReportsTheCeilingsItReached(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a/state.json":       `{"schema":"agentfs/v1","status":"idle"}`,
		"a/b/c/d/e/f/x.json": `{}`,
	}
	for i := range 30 {
		files["a/f"+strconv.Itoa(i)+".json"] = `{}`
	}
	ws := workspace(t, files)

	cases := []struct {
		name string
		args []string
		want diag.Code
	}{
		{"entries", []string{"-max-entries-per-dir", "3"}, diag.CodeEntriesTruncated},
		{"depth", []string{"-max-depth", "2"}, diag.CodeDepthTruncated},
		{"nodes", []string{"-max-nodes", "3"}, diag.CodeNodeCeiling},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"doctor", "-format", "json"}, tc.args...)
			got := invoke(t, nil, append(args, ws)...)

			var env struct {
				Diagnostics []struct {
					Code string `json:"code"`
					Hint string `json:"hint"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, got.stdout)
			}
			for _, d := range env.Diagnostics {
				if d.Code != string(tc.want) {
					continue
				}
				if d.Hint == "" {
					t.Errorf("%s carries no hint, so a reader has nothing to apply", d.Code)
				}
				return
			}
			t.Fatalf("doctor did not report %s:\n%s", tc.want, got.stdout)
		})
	}
}

// A workspace inside every ceiling is a complete view, and saying so is what
// makes the reported conditions mean something.
func TestDoctorReportsNoCeilingForAnOrdinaryWorkspace(t *testing.T) {
	t.Parallel()
	got := invoke(t, nil, "doctor", "-format", "json", conformant(t))

	var env struct {
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
		t.Fatal(err)
	}
	for _, d := range env.Diagnostics {
		if strings.HasPrefix(d.Code, "AFS5") {
			t.Errorf("an ordinary workspace reported the ceiling %s", d.Code)
		}
	}
}

// A published environment variable that no command can act on is a control
// that reads as honoured and is inert.
func TestTheWorkspaceCanComeFromTheEnvironment(t *testing.T) {
	t.Parallel()
	ws := conformant(t)

	fromEnv := invoke(t, map[string]string{"AGENTFS_ROOT": ws}, "scan")
	if fromEnv.code != report.CodeOK {
		t.Fatalf("a workspace named only in the environment exited %d\n%s", fromEnv.code, fromEnv.stderr)
	}
	if !strings.Contains(fromEnv.stdout, "agent-researcher") {
		t.Errorf("the environment's workspace was not read:\n%s", fromEnv.stdout)
	}

	// The argument is the more specific statement of intent.
	fromArg := invoke(t, map[string]string{"AGENTFS_ROOT": filepath.Join(t.TempDir(), "absent")}, "scan", ws)
	if fromArg.code != report.CodeOK {
		t.Fatalf("the argument did not override the environment: exit %d\n%s", fromArg.code, fromArg.stderr)
	}

	neither := invoke(t, nil, "scan")
	if neither.code != report.CodeUsage {
		t.Errorf("a command with no workspace exited %d, want the usage code", neither.code)
	}
	if !strings.Contains(neither.stderr, "AGENTFS_ROOT") {
		t.Errorf("the refusal does not name the environment variable:\n%s", neither.stderr)
	}
}

// Asking for help is not a malformed invocation, and help is a result rather
// than a complaint.
func TestPerCommandHelpSucceedsOnStandardOutput(t *testing.T) {
	t.Parallel()
	for _, cmd := range cli.Commands() {
		t.Run(cmd.Name, func(t *testing.T) {
			t.Parallel()
			for _, form := range []string{"--help", "-h"} {
				got := invoke(t, nil, cmd.Name, form)
				if got.code != report.CodeOK {
					t.Errorf("%s %s exited %d, want 0\n%s", cmd.Name, form, got.code, got.stderr)
				}
				if !strings.Contains(got.stdout, "usage: agentfs "+cmd.Name) {
					t.Errorf("%s %s wrote no usage to stdout:\n%s", cmd.Name, form, got.stdout)
				}
				if got.stderr != "" {
					t.Errorf("%s %s wrote to stderr:\n%s", cmd.Name, form, got.stderr)
				}
			}
		})
	}
}

// A setting that names the commands reading it is only useful if those commands
// exist, and if the naming is true: a row claiming every command must reach one
// the one-shot commands actually consult.
func TestEverySettingNamesCommandsThatExist(t *testing.T) {
	t.Parallel()

	known := make(map[string]bool)
	for _, c := range cli.Commands() {
		known[c.Name] = true
	}
	for _, l := range config.Limits() {
		for _, name := range l.Commands {
			if !known[name] {
				t.Errorf("%s is read by %q, which is not a command", l.Flag, name)
			}
		}
	}
}

// A flag scoped to the terminal command must not read as honoured elsewhere.
// The reference says which commands reach a setting; this is the half that
// makes the statement checkable.
func TestASettingScopedToWatchDoesNotChangeAOneShotResult(t *testing.T) {
	t.Parallel()
	ws := workspace(t, map[string]string{
		"a/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
		"b/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
		"c/state.json": `{"schema":"agentfs/v1","status":"nope"}`,
	})

	base := invoke(t, nil, "validate", "-format", "json", ws)
	scoped := invoke(t, nil, "validate", "-format", "json", "-max-diagnostics", "1", ws)

	if base.stdout != scoped.stdout {
		t.Errorf("a flag the reference scopes to `watch` changed a validate result:\n%s\n%s",
			base.stdout, scoped.stdout)
	}
}
