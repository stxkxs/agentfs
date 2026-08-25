package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// stateSchemaName is the published state contract, whose members the tests
// below hold against the decoder's rules.
const stateSchemaName = "agent-state.v1" + schemaExt

// siteBase is the prefix .github/workflows/pages.yml derives a served path
// from. An $id outside it names a document the site does not serve, so the
// workflow fails the run rather than publishing an identifier nothing
// resolves.
const siteBase = "https://stxkxs.github.io/agentfs/"

// repoRoot returns the module root containing this test, which is the tree the
// published artifacts are committed in.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatalf("working directory: %v", err)
	}
	root, err := moduleRoot(wd)
	if err != nil {
		tb.Fatalf("locate module root: %v", err)
	}
	return root
}

// readFile reads a committed artifact, failing with the path so a missing one
// names what to run.
func readFile(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		tb.Fatalf("%v; run: go run ./internal/tools/schemagen", err)
	}
	return b
}

// TestGeneratorIsDeterministic asserts two runs produce identical artifacts, so
// the gate that fails on a dirty tree after regeneration reports a changed
// contract rather than a generator that varies.
func TestGeneratorIsDeterministic(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, root := range []string{first, second} {
		if _, err := generate(root); err != nil {
			t.Fatalf("generate into %s: %v", root, err)
		}
	}
	for _, a := range artifacts() {
		for _, at := range []func(string, string) string{schemaPath, frozenPath} {
			x, y := readFile(t, at(first, a.name)), readFile(t, at(second, a.name))
			if !bytes.Equal(x, y) {
				t.Errorf("%s differs between runs:\nfirst:\n%s\nsecond:\n%s",
					filepath.Base(at(first, a.name)), x, y)
			}
		}
	}
}

// TestGeneratorWritesEveryArtifact asserts a run reports the paths it wrote and
// that all of them exist, so a caller reading the output is reading the truth.
func TestGeneratorWritesEveryArtifact(t *testing.T) {
	root := t.TempDir()
	written, err := generate(root)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := make([]string, 0, 2*len(artifacts()))
	for _, a := range artifacts() {
		want = append(want, schemaPath(root, a.name), frozenPath(root, a.name))
	}
	if !slices.Equal(written, want) {
		t.Fatalf("wrote %v, want %v", written, want)
	}
	for _, name := range written {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("reported writing %s: %v", name, err)
		}
	}
}

// TestCommittedSchemasMatchTheirGenerators asserts each published document is
// the one this build renders, byte for byte. A hand edit to a published file,
// or a contract change that was not regenerated, fails here.
func TestCommittedSchemasMatchTheirGenerators(t *testing.T) {
	root := repoRoot(t)
	for _, a := range artifacts() {
		want, err := a.render()
		if err != nil {
			t.Errorf("render %s: %v", a.name, err)
			continue
		}
		if got := readFile(t, schemaPath(root, a.name)); !bytes.Equal(got, want) {
			t.Errorf("the committed %s is not what this build renders; "+
				"run: go run ./internal/tools/schemagen\ncommitted:\n%s\nrendered:\n%s", a.name, got, want)
		}
	}
}

// TestFreezeMatchesCommittedSchemas asserts each recorded digest covers the
// document beside it. This is the freeze: changing a published contract
// changes a checksum a reviewer reads, rather than only a generated file a
// review collapses.
func TestFreezeMatchesCommittedSchemas(t *testing.T) {
	root := repoRoot(t)
	for _, a := range artifacts() {
		want := checksumLine(a.name, readFile(t, schemaPath(root, a.name)))
		got := string(readFile(t, frozenPath(root, a.name)))
		if got != want {
			t.Errorf("the frozen checksum does not cover the committed %s; "+
				"run: go run ./internal/tools/schemagen\nrecorded: %s\ncomputed: %s",
				a.name, strings.TrimSpace(got), strings.TrimSpace(want))
		}
	}
}

// TestFrozenChecksumsNameTheirSchemas asserts each recorded line is the format
// shasum(1) and sha256sum(1) read, with the path resolved from the module root.
func TestFrozenChecksumsNameTheirSchemas(t *testing.T) {
	root := repoRoot(t)
	for _, a := range artifacts() {
		line := strings.TrimSuffix(string(readFile(t, frozenPath(root, a.name))), "\n")
		digest, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Errorf("checksum line %q does not separate the digest from the path with two spaces", line)
			continue
		}
		if len(digest) != 64 {
			t.Errorf("digest %q is not a 64-character SHA-256", digest)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("the checksum names %q, which does not resolve from the module root: %v", name, err)
		}
	}
}

// TestPublishedDirectoryHoldsOnlyGeneratedArtifacts asserts schema/ carries a
// document per table entry and nothing else, and .frozen/ a checksum per
// document.
//
// A stray document there is served by the Pages workflow, which publishes
// every schema/*.json: a contract nothing generates would reach an integrator
// as though agentfs implemented it.
func TestPublishedDirectoryHoldsOnlyGeneratedArtifacts(t *testing.T) {
	root := repoRoot(t)

	declared := make([]string, 0, len(artifacts()))
	frozen := make([]string, 0, len(artifacts()))
	for _, a := range artifacts() {
		declared = append(declared, a.name)
		frozen = append(frozen, frozenName(a.name))
	}

	documents, err := filepath.Glob(filepath.Join(root, schemaDir, "*"+schemaExt))
	if err != nil {
		t.Fatalf("list %s: %v", schemaDir, err)
	}
	for _, doc := range documents {
		if name := filepath.Base(doc); !slices.Contains(declared, name) {
			t.Errorf("%s holds %s, which no table entry generates", schemaDir, name)
		}
	}
	if len(documents) != len(declared) {
		t.Errorf("%s holds %d documents for %d table entries", schemaDir, len(documents), len(declared))
	}

	checksums, err := filepath.Glob(filepath.Join(root, schemaDir, frozenDir, "*"+frozenExt))
	if err != nil {
		t.Fatalf("list %s: %v", frozenDir, err)
	}
	for _, sum := range checksums {
		if name := filepath.Base(sum); !slices.Contains(frozen, name) {
			t.Errorf("%s holds %s, which covers no published document", frozenDir, name)
		}
	}
	if len(checksums) != len(frozen) {
		t.Errorf("%s holds %d checksums for %d published documents", frozenDir, len(checksums), len(frozen))
	}
}

// schemaDoc is the part of a published schema this suite reads.
type schemaDoc struct {
	ID         string                     `json:"$id"`
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// committedSchema parses a published document.
func committedSchema(tb testing.TB, name string) schemaDoc {
	tb.Helper()
	var doc schemaDoc
	if err := json.Unmarshal(readFile(tb, schemaPath(repoRoot(tb), name)), &doc); err != nil {
		tb.Fatalf("parse the committed %s: %v", name, err)
	}
	return doc
}

// TestEveryIdentifierNamesItsPublishedPath asserts each document identifies
// itself as the address it is served from, so resolving an $id returns that
// document. The Pages workflow derives the served path by stripping the site
// base from the $id, which is the same arithmetic as here.
func TestEveryIdentifierNamesItsPublishedPath(t *testing.T) {
	for _, a := range artifacts() {
		id := committedSchema(t, a.name).ID
		want := siteBase + path.Join(schemaDir, a.name)
		if id != want {
			t.Errorf("%s declares $id %q, and it is served from %q", a.name, id, want)
		}
	}
}

// TestStateSchemaIDIsCanonical asserts the published state contract carries the
// identifier the decoder's package declares, which is the one a $ref in
// another schema resolves against.
func TestStateSchemaIDIsCanonical(t *testing.T) {
	if got := committedSchema(t, stateSchemaName).ID; got != agentstate.SchemaID {
		t.Errorf("$id is %q, want %q", got, agentstate.SchemaID)
	}
}

// TestEveryRuleIsPublished asserts the state schema declares exactly the
// contract's members, and requires exactly the members the contract requires. A
// member the decoder types but the schema omits is a contract an integrator
// cannot conform to; one the schema declares but the decoder does not type is a
// promise nothing keeps.
func TestEveryRuleIsPublished(t *testing.T) {
	doc := committedSchema(t, stateSchemaName)

	rules := agentstate.Rules()
	wantMembers := make([]string, 0, len(rules))
	var wantRequired []string
	for _, r := range rules {
		wantMembers = append(wantMembers, r.Member)
		if r.Required {
			wantRequired = append(wantRequired, r.Member)
		}
	}

	for _, member := range wantMembers {
		if _, ok := doc.Properties[member]; !ok {
			t.Errorf("rule %q has no entry in the schema's properties", member)
		}
	}
	for member := range doc.Properties {
		if !slices.Contains(wantMembers, member) {
			t.Errorf("the schema declares property %q, which no rule defines", member)
		}
	}
	if !slices.Equal(doc.Required, wantRequired) {
		t.Errorf("the schema requires %v, want %v", doc.Required, wantRequired)
	}
}

// TestRunRejectsArguments asserts the generator takes no arguments, so an
// invocation that expected a flag fails loudly instead of writing the fixed
// paths and appearing to have honored it.
func TestRunRejectsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-out", "elsewhere"}, &stdout, &stderr); code != exitUsage {
		t.Errorf("exit code %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("wrote %q to stdout on a usage failure", stdout.String())
	}
	if !strings.Contains(stderr.String(), "takes no arguments") {
		t.Errorf("stderr %q does not say the command takes no arguments", stderr.String())
	}
	for _, a := range artifacts() {
		if !strings.Contains(stderr.String(), a.name) {
			t.Errorf("stderr %q does not name %s, so the caller is not told what a bare run writes",
				stderr.String(), a.name)
		}
	}
}

// TestRunWritesUnderTheModuleRoot asserts a bare run resolves the module root
// from the working directory, writes every artifact there, and names them.
// This is the invocation "task gen" makes.
func TestRunWritesUnderTheModuleRoot(t *testing.T) {
	// The run resolves its root through the working directory, which the
	// kernel reports with symlinks expanded; the paths it prints are compared
	// against, so the expected root is expanded too.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.26\n"), filePerm); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	nested := filepath.Join(root, "internal", "tools", "schemagen")
	if err := os.MkdirAll(nested, dirPerm); err != nil {
		t.Fatalf("create package directory: %v", err)
	}
	t.Chdir(nested)

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}
	for _, a := range artifacts() {
		for _, name := range []string{schemaPath(root, a.name), frozenPath(root, a.name)} {
			if _, err := os.Stat(name); err != nil {
				t.Errorf("%v", err)
			}
			if !strings.Contains(stdout.String(), name) {
				t.Errorf("stdout %q does not name %s", stdout.String(), name)
			}
		}
	}
}

// TestModuleRootRejectsAnUnrelatedModule asserts the walk refuses a go.mod
// declaring another module, so a run from inside a vendored or nested tree
// does not create a schema directory there.
func TestModuleRootRejectsAnUnrelatedModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n\ngo 1.26\n"), filePerm); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	_, err := moduleRoot(dir)
	if err == nil {
		t.Fatal("accepted a go.mod declaring another module")
	}
	if !strings.Contains(err.Error(), "example.com/other") {
		t.Errorf("error %q does not name the module it found", err)
	}
}

// TestModuleRootReportsAnAbsentGoMod asserts the walk terminates at the
// filesystem root with an error naming where it started.
func TestModuleRootReportsAnAbsentGoMod(t *testing.T) {
	dir := t.TempDir()
	_, err := moduleRoot(dir)
	if err == nil {
		t.Fatal("found a module root above a directory with no go.mod")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q does not name the directory the walk started from", err)
	}
}

// TestDeclaredModuleReadsTheDirective asserts the go.mod reader takes the
// module path from the directive and nothing else.
func TestDeclaredModuleReadsTheDirective(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"leading directive", "module github.com/stxkxs/agentfs\n\ngo 1.26\n", modulePath},
		{"after a comment", "// a comment\nmodule example.com/x\n", "example.com/x"},
		{"indented", "\tmodule example.com/x\n", "example.com/x"},
		{"no trailing newline", "module example.com/x", "example.com/x"},
		{"absent", "go 1.26\n", ""},
		{"empty", "", ""},
		{"require block naming a module", "module example.com/x\n\nrequire (\n\texample.com/y v1.0.0\n)\n", "example.com/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := declaredModule([]byte(c.src)); got != c.want {
				t.Errorf("declaredModule = %q, want %q", got, c.want)
			}
		})
	}
}

// TestChecksumLineCoversTheDocument asserts the recorded digest is of the bytes
// written, by checking a document the test controls.
func TestChecksumLineCoversTheDocument(t *testing.T) {
	// The SHA-256 of the empty input, which is a fixed value of the algorithm.
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	want := fmt.Sprintf("%s  schema/%s\n", emptyDigest, stateSchemaName)
	if got := checksumLine(stateSchemaName, nil); got != want {
		t.Errorf("checksumLine = %q, want %q", got, want)
	}
}
