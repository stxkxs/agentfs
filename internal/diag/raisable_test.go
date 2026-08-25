package diag_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/diag"
)

// diagImportPath is the package a raising call site imports.
const diagImportPath = "github.com/stxkxs/agentfs/internal/diag"

// Every registered code must be raisable.
//
// The registry is a published surface: the generated reference lists every code
// and the runbook tells an operator to branch on some of them. A code no call
// site names is a promise the binary cannot keep — a consumer that suppresses
// it, or waits for it, waits forever.
//
// The check reads the module rather than the registry. It parses the constant
// block in this package to learn which identifier carries each code, then walks
// every Go file outside this package for a selector naming that identifier
// through an import of this package. Test files are excluded: a code only a
// test raises is still a code no operator can see.
//
// This is the reciprocal of TestRunbookIndexesRealDiagnosticCodes, which holds
// the runbook to the registry. One direction alone lets the registry grow codes
// nothing produces; the pair closes it.
func TestEveryCodeIsRaisable(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	pkg := filepath.Join(root, "internal", "diag")
	names := codeConstants(t, pkg)
	raised := raisedConstants(t, root, pkg)

	for _, info := range diag.Codes() {
		name, declared := names[info.Code]
		if !declared {
			t.Errorf("%s is registered under no Code constant, so no call site can name it", info.Code)
			continue
		}
		if !raised[name] {
			t.Errorf("%s (diag.%s) is registered and raised nowhere outside %s: %s",
				info.Code, name, diagImportPath, info.Summary)
		}
	}
}

// moduleRoot walks up from the package directory to the directory holding
// go.mod, which is the tree the check reads.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// codeConstants maps each declared code to the identifier that carries it, so
// a registry entry can be checked against the name a call site would write.
func codeConstants(t *testing.T, dir string) map[diag.Code]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[diag.Code]string)
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !isSourceFile(e.Name()) {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				name, value, ok := codeSpec(spec)
				if !ok {
					continue
				}
				out[value] = name
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Code constant is declared in %s", dir)
	}
	return out
}

// codeSpec reads one `Name Code = "AFSxxxx"` declaration.
func codeSpec(spec ast.Spec) (name string, value diag.Code, ok bool) {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
		return "", "", false
	}
	if ident, isIdent := vs.Type.(*ast.Ident); !isIdent || ident.Name != "Code" {
		return "", "", false
	}
	lit, ok := vs.Values[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", "", false
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", "", false
	}
	return vs.Names[0].Name, diag.Code(unquoted), true
}

// raisedConstants collects every identifier a non-test file outside skip names
// through an import of the diagnostic package. Resolving the import rather than
// matching the selector's text is what keeps a same-named constant from another
// package out of the result.
func raisedConstants(t *testing.T, root, skip string) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if p == skip || (p != root && strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !isSourceFile(d.Name()) {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		alias, imported := diagImportName(f)
		if !imported {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, isIdent := sel.X.(*ast.Ident); isIdent && x.Name == alias {
				out[sel.Sel.Name] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// diagImportName returns the identifier a file names the diagnostic package by,
// and whether it imports it at all.
func diagImportName(f *ast.File) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != diagImportPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return "diag", true
	}
	return "", false
}

// isSourceFile reports whether a filename is Go the binary is built from, as
// opposed to a test.
func isSourceFile(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}
