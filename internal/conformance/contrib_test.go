package conformance_test

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// contribDir holds the reference writers an integrator vendors, relative to
// this suite.
const contribDir = "../../contrib"

// writer is one reference writer and the command that runs it.
type writer struct {
	// name is the language the writer is for.
	name string
	// command is the interpreter that runs the file with no build step.
	command string
	// file is the writer, relative to [contribDir].
	file string
}

// writers are the reference writers, each of which runs its own example when
// invoked with a directory to write into.
var writers = []writer{
	{name: "python", command: "python3", file: "python/agentfs_state.py"},
	{name: "typescript", command: "node", file: "typescript/agentfs-state.ts"},
}

// TestReferenceWritersProduceCleanDocuments asserts what each writer's header
// claims: a document it writes carries no diagnostic at all, and satisfies the
// published schema. The writers are what an integrator copies, so a claim about
// their output that only the header makes is a claim nothing holds them to.
//
// The document is read against the clock that wrote it rather than the corpus's
// fixed instant, because a writer stamps the moment it runs.
func TestReferenceWritersProduceCleanDocuments(t *testing.T) {
	schema := loadSchema(t)
	emitted := map[string][]string{}

	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			path := runWriter(t, w)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the written document: %v", err)
			}

			if base := filepath.Base(path); base != agentstate.StateFile {
				t.Errorf("the writer wrote %q, and a reader looks for %q", base, agentstate.StateFile)
			}

			st, ds := agentstate.Decode(agentstate.StateFile, src, agentstate.Options{Now: time.Now()})
			for _, d := range ds {
				t.Errorf("the written document raises %s", d)
			}
			if st.Schema != agentstate.SchemaVersion {
				t.Errorf("the writer declared schema %q, want %q", st.Schema, agentstate.SchemaVersion)
			}

			doc := parseObject(t, src)
			for _, violation := range checkAgainstSchema(schema, doc) {
				t.Errorf("the written document does not satisfy the schema: %s", violation)
			}
			emitted[w.name] = slices.Sorted(maps.Keys(doc))
		})
	}

	t.Run("writers agree", func(t *testing.T) {
		if len(emitted) < len(writers) {
			t.Skip("a writer did not run")
		}
		names := slices.Sorted(maps.Keys(emitted))
		first := emitted[names[0]]
		for _, name := range names[1:] {
			if !slices.Equal(emitted[name], first) {
				t.Errorf("%s writes %v and %s writes %v; one contract, one member set",
					names[0], first, name, emitted[name])
			}
		}
		for _, member := range contractMembers() {
			if !slices.Contains(first, member) {
				t.Errorf("no writer's example declares the member %q", member)
			}
		}
	})
}

// TestReferenceWritersRefuseWhatTheDecoderRefuses asserts the other half of
// each writer's header: a value a reader would report is refused at the writer
// rather than published. The refusals are named by the diagnostic the document
// would otherwise carry.
func TestReferenceWritersRefuseWhatTheDecoderRefuses(t *testing.T) {
	refusals := []struct {
		// name is the value the writer is handed.
		name string
		// code is the diagnostic the document would carry were it published.
		code diag.Code
		// python and script are the expressions that build the refused state.
		python     string
		typescript string
	}{
		{
			name: "status outside the vocabulary", code: diag.CodeStatusUnknown,
			python:     `State(status="thinking")`,
			typescript: `stateDocument({ status: "thinking" as Status })`,
		},
		{
			name: "empty string member", code: diag.CodeEmptyString,
			python:     `State(status=Status.RUNNING, agent="")`,
			typescript: `stateDocument({ status: Status.Running, agent: "" })`,
		},
		{
			name: "negative ordinal", code: diag.CodeStepNegative,
			python:     `State(status=Status.RUNNING, step=-1)`,
			typescript: `stateDocument({ status: Status.Running, step: -1 })`,
		},
		{
			name: "timestamp naming no instant", code: diag.CodeTimeNoOffset,
			python:     `State(status=Status.RUNNING, started_at=datetime(2026, 4, 8, 13, 0, 0))`,
			typescript: `stateDocument({ status: Status.Running, startedAt: new Date("not a date") })`,
		},
		{
			name: "string past its ceiling", code: diag.CodeStringTooLong,
			python:     `State(status=Status.RUNNING, agent="a" * 257)`,
			typescript: `stateDocument({ status: Status.Running, agent: "a".repeat(257) })`,
		},
	}

	t.Run("python", func(t *testing.T) {
		dir := requireWriterDir(t, writers[0])
		for _, r := range refusals {
			t.Run(r.name, func(t *testing.T) {
				script := "from datetime import datetime\n" +
					"from agentfs_state import State, Status\n" +
					"try:\n" +
					"    " + r.python + ".document()\n" +
					"except ValueError:\n" +
					"    raise SystemExit(0)\n" +
					"raise SystemExit('published a document that raises " + string(r.code) + "')\n"
				runRefusal(t, dir, "python3", []string{"-c", script}, r.code)
			})
		}
	})

	t.Run("typescript", func(t *testing.T) {
		dir := requireWriterDir(t, writers[1])
		// The script sits outside the writer's directory, so it names the
		// module by file URL rather than by a relative specifier Node would
		// resolve against the script.
		module := moduleURL(t, writers[1])
		for _, r := range refusals {
			t.Run(r.name, func(t *testing.T) {
				script := `import { stateDocument, Status } from ` + strconv.Quote(module) + ";\n" +
					"try {\n" +
					"  " + r.typescript + ";\n" +
					"} catch {\n" +
					"  process.exit(0);\n" +
					"}\n" +
					`console.error("published a document that raises ` + string(r.code) + `");` + "\n" +
					"process.exit(1);\n"
				path := filepath.Join(t.TempDir(), "refusal.ts")
				if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
					t.Fatalf("write the refusal script: %v", err)
				}
				runRefusal(t, dir, "node", []string{path}, r.code)
			})
		}
	})
}

// contractMembers returns the wire names the contract defines.
func contractMembers() []string {
	rules := agentstate.Rules()
	out := make([]string, 0, len(rules))
	for i := range rules {
		out = append(out, rules[i].Member)
	}
	return out
}

// contract is what a writer declares about the contract it vendors. A vendored
// writer holds its own copy of the version, the document name, the ceilings and
// the vocabulary, so nothing but a test keeps those in step with the table the
// decoder reads.
type contract struct {
	Schema     string   `json:"schema"`
	StateFile  string   `json:"state_file"`
	NameChars  int      `json:"name_chars"`
	TextChars  int      `json:"text_chars"`
	Vocabulary []string `json:"vocabulary"`
}

// TestReferenceWritersVendorTheContract asserts each writer's copy of the
// contract matches the table the decoder types documents against. A writer
// whose ceiling or vocabulary has drifted emits documents a reader reports on,
// and the drift is invisible until an integrator hits it.
func TestReferenceWritersVendorTheContract(t *testing.T) {
	want := contract{
		Schema:     agentstate.SchemaVersion,
		StateFile:  agentstate.StateFile,
		NameChars:  nameCeiling(t),
		TextChars:  agentstate.MaxStringRunes,
		Vocabulary: agentstate.Vocabulary(),
	}

	scripts := map[string][]string{
		"python": {"-c", "import json, agentfs_state as m; " +
			"print(json.dumps({'schema': m.SCHEMA_VERSION, 'state_file': m.STATE_FILE, " +
			"'name_chars': m.MAX_NAME_CHARS, 'text_chars': m.MAX_TEXT_CHARS, " +
			"'vocabulary': [s.value for s in m.Status]}))"},
	}

	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			dir := requireWriterDir(t, w)
			args, ok := scripts[w.name]
			if !ok {
				args = []string{"--input-type=module", "-e", `import { SCHEMA_VERSION, STATE_FILE, MAX_NAME_CHARS, MAX_TEXT_CHARS, STATUSES } from ` +
					strconv.Quote(moduleURL(t, w)) + `;` +
					`console.log(JSON.stringify({ schema: SCHEMA_VERSION, state_file: STATE_FILE,` +
					` name_chars: MAX_NAME_CHARS, text_chars: MAX_TEXT_CHARS, vocabulary: STATUSES }));`}
			}

			cmd := exec.CommandContext(t.Context(), w.command, args...)
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("read the writer's constants: %v\n%s", err, out)
			}
			var got contract
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("parse the writer's constants: %v\n%s", err, out)
			}

			if got.Schema != want.Schema {
				t.Errorf("the writer emits schema %q, and the decoder implements %q", got.Schema, want.Schema)
			}
			if got.StateFile != want.StateFile {
				t.Errorf("the writer writes %q, and a reader looks for %q", got.StateFile, want.StateFile)
			}
			if got.NameChars != want.NameChars {
				t.Errorf("the writer caps a name at %d characters, and the contract caps it at %d", got.NameChars, want.NameChars)
			}
			if got.TextChars != want.TextChars {
				t.Errorf("the writer caps prose at %d characters, and the contract caps it at %d", got.TextChars, want.TextChars)
			}
			if !slices.Equal(got.Vocabulary, want.Vocabulary) {
				t.Errorf("the writer offers the vocabulary %v, and the contract defines %v", got.Vocabulary, want.Vocabulary)
			}
		})
	}
}

// nameCeiling is the ceiling the contract puts on a name-shaped member, read
// from the table rather than restated.
func nameCeiling(tb testing.TB) int {
	tb.Helper()
	rules := agentstate.Rules()
	for i := range rules {
		if rules[i].Member == "agent" {
			return rules[i].MaxRunes
		}
	}
	tb.Fatal("the contract declares no agent member")
	return 0
}

// moduleURL names a writer by file URL, which a script run from outside the
// writer's directory can resolve.
func moduleURL(tb testing.TB, w writer) string {
	tb.Helper()
	dir := requireWriterDir(tb, w)
	return (&url.URL{Scheme: "file", Path: filepath.Join(dir, filepath.Base(w.file))}).String()
}

// requireWriterDir returns the directory holding a writer, skipping when its
// interpreter is not installed. A machine without node or python3 still runs
// the rest of the suite.
func requireWriterDir(tb testing.TB, w writer) string {
	tb.Helper()
	if _, err := exec.LookPath(w.command); err != nil {
		tb.Skipf("%s is not installed, so the %s writer cannot be run", w.command, w.name)
	}
	dir, err := filepath.Abs(filepath.Dir(filepath.Join(contribDir, w.file)))
	if err != nil {
		tb.Fatalf("resolve the writer's directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(w.file))); err != nil {
		tb.Fatalf("the writer is not where the README says it is: %v", err)
	}
	return dir
}

// runWriter runs a writer's own example into a temporary directory and returns
// the path it reports writing.
func runWriter(tb testing.TB, w writer) string {
	tb.Helper()
	dir := requireWriterDir(tb, w)
	target := tb.TempDir()

	cmd := exec.CommandContext(tb.Context(), w.command, filepath.Base(w.file), target)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("run %s %s: %v\n%s", w.command, w.file, err, out)
	}
	reported := lastLine(string(out))
	if reported == "" {
		tb.Fatalf("%s printed no path\n%s", w.file, out)
	}
	return reported
}

// runRefusal runs one refusal script and reports a writer that published what
// the decoder would have reported.
func runRefusal(tb testing.TB, dir, command string, args []string, code diag.Code) {
	tb.Helper()
	if _, registered := diag.Lookup(code); !registered {
		tb.Fatalf("the refusal names %s, which is not a registered code", code)
	}
	cmd := exec.CommandContext(tb.Context(), command, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Errorf("the writer accepted a value a reader reports as %s: %v\n%s", code, err, out)
	}
}

// lastLine returns the final non-empty line of a command's output.
func lastLine(out string) string {
	var last string
	for line := range strings.SplitSeq(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			last = trimmed
		}
	}
	return last
}
