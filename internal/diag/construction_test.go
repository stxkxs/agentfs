package diag_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A diagnostic quotes the workspace back — a path, a member name, a value an
// agent wrote — and it is printed to a terminal, so every member it carries has
// to be neutralized. The constructors in this package do that.
//
// A composite literal built elsewhere skips them. This test is what makes the
// invariant a property of the type rather than of each caller remembering, and
// it is the gate that would have caught the two live injections this repository
// shipped with: one field unsanitized inside the sink, and four constructors
// that never reached it.
func TestNoDiagnosticIsBuiltOutsideThisPackage(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable subtree holds no Go this test can check
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "contrib":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(filepath.ToSlash(rel), "internal/diag/") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("parse %s: %v", rel, parseErr)
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !namesDiagnostic(lit.Type) {
				return true
			}
			offenders = append(offenders,
				filepath.ToSlash(rel)+":"+fmtLine(fset, lit.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, o := range offenders {
		t.Errorf("%s builds a diag.Diagnostic directly, so its members are not neutralized — "+
			"use diag.About or diag.Observed", o)
	}
}

// namesDiagnostic reports whether a composite literal's type is
// diag.Diagnostic, including the elided form inside a []diag.Diagnostic literal.
func namesDiagnostic(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "diag" && sel.Sel.Name == "Diagnostic"
}

func fmtLine(fset *token.FileSet, pos token.Pos) string {
	return strings.TrimPrefix(fset.Position(pos).String(), fset.Position(pos).Filename+":")
}
