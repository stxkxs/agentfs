package layout_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The package divides the screen and renders nothing into it, so it depends on
// no rendering library: geometry that could reach for a style, a terminal
// driver or a widget would grow a second place where a pane's size is decided.
// The check reads this directory's own source rather than a resolved
// dependency list, so an import added to any file here fails it, and it holds
// the test files to the same rule beyond the package under test.
func TestThePackageImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	const self = "github.com/stxkxs/agentfs/internal/ui/layout"

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing the package directory: %v", err)
	}
	sources := 0
	fset := token.NewFileSet()
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		if !strings.HasSuffix(name, "_test.go") {
			sources++
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: import path %s: %v", name, spec.Path.Value, err)
			}
			if strings.HasSuffix(name, "_test.go") && path == self {
				continue
			}
			// A standard library import path has no dot in its first
			// element, which is where a module's host would be.
			if root, _, _ := strings.Cut(path, "/"); strings.Contains(root, ".") {
				t.Errorf("%s imports %q, which is outside the standard library", name, path)
			}
		}
	}
	if sources == 0 {
		t.Fatalf("found no non-test source in %v, so the check proved nothing", names)
	}
}
