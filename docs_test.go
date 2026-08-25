package main_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/diag"
)

// Prose is a claim about the world. These tests check the claims the repository
// makes about itself: that every package it names exists, that every document it
// links to is there, and that it does not name a filesystem without saying how
// change on it is observed — which is the shape of the over-claim that a
// filesystem list beside the word "watch" invites.

// packageMention matches a reference to an internal package in prose.
var packageMention = regexp.MustCompile(`\b(?:internal/)?(agentstate|workspace|index|watch|fileview|fsx|textx|diag|config|report|metrics|buildinfo|cli|ui/app|ui/pane|ui/render|ui/layout|ui/theme|ui/keys)\b`)

func TestNamedPackagesExist(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{"README.md", "docs/assets/architecture.svg"} {
		body := read(t, doc)
		for _, m := range packageMention.FindAllStringSubmatch(body, -1) {
			pkg := filepath.Join("internal", m[1])
			if _, err := os.Stat(pkg); err != nil {
				t.Errorf("%s names the package %q, which does not exist", doc, pkg)
			}
		}
	}
}

func TestLinkedDocumentsExist(t *testing.T) {
	t.Parallel()
	link := regexp.MustCompile(`\]\(([^)#:]+\.(?:md|svg))\)|\]\(((?:docs|contrib)/)\)`)
	body := read(t, "README.md")
	for _, m := range link.FindAllStringSubmatch(body, -1) {
		target := m[1]
		if target == "" {
			target = m[2]
		}
		if _, err := os.Stat(target); err != nil {
			t.Errorf("README links to %q, which does not exist", target)
		}
	}
}

// anchorLink matches a markdown link carrying a fragment, capturing the target
// document — empty for a link into the same page — and the fragment.
var anchorLink = regexp.MustCompile(`\]\(([^)#]*)#([^)]+)\)`)

// headingLine matches an ATX heading and captures its text.
var headingLine = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+?)[ \t]*$`)

// slugPunctuation is everything a heading's rendered text drops on its way to
// an anchor. What survives is letters, digits, hyphens, underscores and the
// spaces that become hyphens.
var slugPunctuation = regexp.MustCompile(`[^\p{L}\p{N} \-_]`)

// slug renders a heading the way a markdown host derives its anchor: lowercase,
// punctuation dropped, spaces as hyphens.
func slug(heading string) string {
	s := strings.ToLower(heading)
	s = slugPunctuation.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}

// A fragment is a claim that the target document carries a heading by that
// name, and it is the half of a link that nothing else checks: the file
// resolves, the page opens, and the reader lands at the top of it instead of at
// the section the sentence promised. Renaming a heading breaks every fragment
// pointing at it without touching the line that points.
func TestLinkFragmentsResolveToHeadings(t *testing.T) {
	t.Parallel()

	anchors := make(map[string]map[string]bool)
	for _, doc := range docFiles(t) {
		found := make(map[string]bool)
		for _, m := range headingLine.FindAllStringSubmatch(read(t, doc), -1) {
			found[slug(m[1])] = true
		}
		anchors[filepath.ToSlash(doc)] = found
	}

	checked := 0
	for _, doc := range docFiles(t) {
		for _, m := range anchorLink.FindAllStringSubmatch(read(t, doc), -1) {
			target := filepath.ToSlash(doc)
			if m[1] != "" {
				if !strings.HasSuffix(m[1], ".md") {
					continue
				}
				target = filepath.ToSlash(filepath.Join(filepath.Dir(doc), m[1]))
			}
			found, known := anchors[target]
			if !known {
				t.Errorf("%s links into %q, which is not a document in this repository", doc, target)
				continue
			}
			checked++
			if !found[m[2]] {
				t.Errorf("%s links to %s#%s, which names no heading there", doc, target, m[2])
			}
		}
	}
	if checked == 0 {
		t.Error("no document links to a heading, so this test asserted nothing")
	}
}

// A filesystem named in prose without its detection mode is the over-claim this
// test exists to prevent: kernel notification does not observe a write by
// another client of a network export, so naming one implies a mechanism that
// must be named alongside it.
func TestFilesystemClaimsNameTheirDetectionMode(t *testing.T) {
	t.Parallel()
	remote := []string{"NFS", "EFS", "SMB", "FUSE", "S3"}
	modes := []string{"sweep", "Sweep", "hybrid", "notification"}

	for _, doc := range []string{"README.md", "docs/assets/architecture.svg"} {
		body := read(t, doc)
		named := false
		for _, fs := range remote {
			if strings.Contains(body, fs) {
				named = true
			}
		}
		if !named {
			continue
		}
		explained := false
		for _, mode := range modes {
			if strings.Contains(body, mode) {
				explained = true
			}
		}
		if !explained {
			t.Errorf("%s names a network filesystem without naming how change on it is observed", doc)
		}
	}
}

// The repository states what it needs from the toolchain in one place, and
// go.mod is the place that is enforced.
func TestReadmeGoVersionMatchesTheModule(t *testing.T) {
	t.Parallel()
	mod := read(t, "go.mod")
	m := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod declares no go directive")
	}
	if !strings.Contains(read(t, "README.md"), "Go "+m[1]) {
		t.Errorf("README does not state the Go %s the module requires", m[1])
	}
}

// Session narration and time-relative wording go stale the moment anything
// changes around them, so they are refused in shipped prose.
func TestProseIsTimeless(t *testing.T) {
	t.Parallel()
	banned := []string{
		"as discussed", "we decided", "turns out", "after some digging",
		"for future reference", "as of this writing", "at the time of writing",
		"no longer ", "used to be", "has been replaced", "was previously",
	}
	for _, doc := range docFiles(t) {
		body := strings.ToLower(read(t, doc))
		for _, phrase := range banned {
			if strings.Contains(body, phrase) {
				t.Errorf("%s contains %q", doc, phrase)
			}
		}
	}
}

// A code fence left open swallows the rest of the document, which turns one
// typo into a page a reader cannot use.
func TestCodeFencesAreBalanced(t *testing.T) {
	t.Parallel()
	for _, doc := range docFiles(t) {
		fences := 0
		for _, line := range strings.Split(read(t, doc), "\n") {
			if strings.HasPrefix(line, "```") {
				fences++
			}
		}
		if fences%2 != 0 {
			t.Errorf("%s has %d code fences, so one is unclosed", doc, fences)
		}
	}
}

// docFiles returns every markdown document in the repository.
func docFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A directory that cannot be listed holds no document this test
			// can check, and stopping the walk would hide the ones it can.
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// declaredTests returns the name of every test and fuzz target the tree
// declares.
//
// The names are read from the source rather than from `go test -list`, which
// compiles a test binary per package to answer. Compiling is both far slower
// than the question deserves and answerable only for the platform doing the
// asking, so a control asserted by a test behind a build tag would read as
// missing everywhere else.
func declaredTests(t *testing.T) map[string]bool {
	t.Helper()

	known := make(map[string]bool)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if name := fn.Name.Name; strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz") {
				known[name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the declared tests: %v", err)
	}
	return known
}

// A threat model names a control and the test that checks it. A named test that
// does not exist turns the control into an assertion, which is the failure the
// document exists to avoid.
func TestThreatModelNamesTestsThatExist(t *testing.T) {
	t.Parallel()

	known := declaredTests(t)

	named := regexp.MustCompile("`((?:Test|Fuzz)[A-Za-z0-9_]+)`")
	matches := named.FindAllStringSubmatch(read(t, "docs/threat-model.md"), -1)
	if len(matches) < 20 {
		t.Fatalf("the threat model names %d tests, which is too few to be evidence-bound", len(matches))
	}
	for _, m := range matches {
		if !known[m[1]] {
			t.Errorf("the threat model names %s, which no package declares", m[1])
		}
	}
}

// A flag named in prose is a claim that the binary accepts it, and a flag the
// binary accepts is one the prose owes the operator a reason for. Both
// directions are checked here: a page naming a withdrawn flag sends an operator
// to a command line that exits 2, and a flag no page names is a knob whose
// justification lives only in the generated table.
func TestDocumentedFlagsExist(t *testing.T) {
	t.Parallel()

	accepted := make(map[string]bool)
	for _, l := range config.Limits() {
		// The limits table carries every setting, including the ones no flag
		// reaches. cli.bind skips Root because the workspace is the positional
		// argument: one setting with two spellings would let a command line
		// disagree with itself.
		if l.Flag == "" || l.Flag == "root" {
			continue
		}
		accepted[l.Flag] = true
	}
	// Every command registers this one; it sets no field of config.Config, so
	// the limits table does not carry it.
	accepted["format"] = true

	named := regexp.MustCompile("`--([a-z][a-z0-9-]*)`")
	seen := make(map[string]bool)
	for _, doc := range docFiles(t) {
		for _, m := range named.FindAllStringSubmatch(read(t, doc), -1) {
			seen[m[1]] = true
			if !accepted[m[1]] {
				t.Errorf("%s names the flag --%s, which agentfs does not accept", doc, m[1])
			}
		}
	}
	for flag := range accepted {
		if !seen[flag] {
			t.Errorf("no document names --%s, which agentfs accepts", flag)
		}
	}
}

// Every diagnostic code the runbook indexes must be one agentfs can raise, or a
// reader looks up a symptom that cannot occur.
func TestRunbookIndexesRealDiagnosticCodes(t *testing.T) {
	t.Parallel()

	registered := make(map[string]bool)
	for _, info := range diag.Codes() {
		registered[string(info.Code)] = true
	}

	named := regexp.MustCompile(`AFS\d{4}`)
	found := named.FindAllString(read(t, "docs/runbook.md"), -1)
	if len(found) < 8 {
		t.Fatalf("the runbook names %d codes, which is too few to be an index", len(found))
	}
	for _, code := range found {
		if !registered[code] {
			t.Errorf("the runbook indexes %s, which is not a registered code", code)
		}
	}
}

// A test under a directory named testdata is invisible to `go list ./...`, so
// it runs in no gate: the suite holding the published schema to the decoder
// would execute only when somebody typed the path by hand. A test file earns
// its place by running whenever the gate runs.
func TestNoTestHidesOutsideTheBuildGraph(t *testing.T) {
	t.Parallel()

	graphed := make(map[string]bool)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	out, err := exec.CommandContext(ctx, "go", "list", "-f", "{{.Dir}}", "./...").Output()
	if err != nil {
		t.Fatalf("list packages: %v", err)
	}
	for _, dir := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		graphed[filepath.Clean(dir)] = true
	}

	err = filepath.WalkDir(".", func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable subtree holds nothing to check
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		abs, absErr := filepath.Abs(filepath.Dir(path))
		if absErr != nil {
			return nil //nolint:nilerr // a path that will not resolve names no package
		}
		if !graphed[filepath.Clean(abs)] {
			t.Errorf("%s is a test in no package `go test ./...` reaches, so it gates nothing", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// A transcript is a claim about what a command prints, and it is the claim a
// reader checks first by running the thing. Every other class of documentation
// claim in this repository has a gate; this is the one for transcripts.
//
// It covers the commands whose output is a function of the workspace alone. A
// transcript of the terminal interface is a frame, and the goldens under
// internal/ui/app cover those.
// filesystemLine matches the line of `agentfs doctor` that names the filesystem
// under the root, whose value is a property of the machine the command runs on.
// The label is held; the value is not.
var filesystemLine = regexp.MustCompile(`^(filesystem\s+).+$`)

func TestTutorialTranscriptsMatchTheCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binary := buildAgentfs(t)
	ws := filepath.Join(dir, "ws")

	// The workspace the tutorial tells a reader to create.
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(ws, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(ws, "agent-researcher", "logs"), 0o750); err != nil {
		t.Fatal(err)
	}
	write("agent-researcher/state.json", `{
  "schema": "agentfs/v1",
  "status": "running",
  "task": "Retrieve and rank sources",
  "step": 3,
  "steps_total": 8,
  "model": "claude-opus-5"
}
`)
	write("agent-writer/state.json", `{
  "schema": "agentfs/v1",
  "status": "idle"
}
`)

	tutorial := read(t, "docs/tutorial/getting-started.md")

	// Each case names a command and a line the tutorial prints for it. The
	// comparison is per line, with the values that name the machine rather than
	// the program substituted out: the workspace path, and the filesystem
	// `agentfs doctor` finds under the root. A transcript stating one machine's
	// filesystem would be a measurement rather than documentation — true where
	// it was captured and false everywhere else.
	cases := []struct {
		name string
		args []string
	}{
		{"scan", []string{"scan", ws}},
		{"validate", []string{"validate", ws}},
		{"doctor", []string{"doctor", ws}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()

			out, err := exec.CommandContext(ctx, binary, tc.args...).Output()
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
				line = strings.TrimRight(strings.ReplaceAll(line, ws, "/tmp/ws"), " ")
				line = filesystemLine.ReplaceAllString(line, "${1}<the filesystem under the root>")
				if line == "" {
					continue
				}
				if !strings.Contains(tutorial, line) {
					t.Errorf("the tutorial does not print this line of `agentfs %s`:\n\t%s",
						strings.Join(tc.args[:1], " "), line)
				}
			}
		})
	}
}
