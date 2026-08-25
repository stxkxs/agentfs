package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The generated pages are the reference an operator reads, so the properties
// that keep them usable are asserted here rather than noticed later: they are
// byte-stable, they all carry the notice that says where to edit them, and
// nothing in them is empty.

func generateInto(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("locate the module root: %v", err)
	}
	m, err := readModule(root)
	if err != nil {
		t.Fatalf("read the module: %v", err)
	}
	if err := generate(dir, m); err != nil {
		t.Fatalf("generate: %v", err)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read %s: %v", dir, readErr)
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		b, fileErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if fileErr != nil {
			t.Fatalf("read %s: %v", e.Name(), fileErr)
		}
		out[e.Name()] = b
	}
	return out
}

// A generator that is not deterministic makes `task verify` fail on a tree
// nobody changed, which trains a reader to ignore it.
func TestGenerationIsDeterministic(t *testing.T) {
	first := generateInto(t, t.TempDir())
	second := generateInto(t, t.TempDir())

	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d pages", len(first), len(second))
	}
	for name, a := range first {
		b, ok := second[name]
		if !ok {
			t.Errorf("the second run did not produce %s", name)
			continue
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between two runs of the same generator", name)
		}
	}
}

func TestEveryDocumentIsProducedAndCarriesItsNotice(t *testing.T) {
	pages := generateInto(t, t.TempDir())

	for _, d := range documents() {
		body, ok := pages[d.name]
		if !ok {
			t.Errorf("the generator declares %s and produced no such file", d.name)
			continue
		}
		if !strings.HasPrefix(string(body), noticePrefix) {
			t.Errorf("%s does not open with the generated notice", d.name)
		}
		if !strings.Contains(string(body), d.source) {
			t.Errorf("%s does not name %s, so a reader cannot find the table to edit", d.name, d.source)
		}
		if len(body) < 200 {
			t.Errorf("%s is %d bytes, which is too short to be the page it claims", d.name, len(body))
		}
	}
	if len(pages) != len(documents()) {
		t.Errorf("the generator produced %d files for %d declared documents", len(pages), len(documents()))
	}
}

// A page whose source file has moved names a table a reader cannot open.
func TestEveryDocumentNamesASourceThatExists(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range documents() {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d.source))); err != nil {
			t.Errorf("%s names the source %s, which does not exist", d.name, d.source)
		}
	}
}

// An unclosed fence swallows the rest of a page.
func TestFencesAreBalanced(t *testing.T) {
	for name, body := range generateInto(t, t.TempDir()) {
		fences := 0
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "```") {
				fences++
			}
		}
		if fences%2 != 0 {
			t.Errorf("%s has %d fences, so one is unclosed", name, fences)
		}
	}
}

// The committed pages are what a reader browsing the repository sees, so they
// must be what the generator produces.
func TestCommittedPagesMatchTheGenerator(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	fresh := generateInto(t, t.TempDir())

	for name, want := range fresh {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(outputDir), name))
		if err != nil {
			t.Errorf("read the committed %s: %v — run `task gen`", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the committed %s differs from what the generator produces — run `task gen`", name)
		}
	}
}
