package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// modulePath is the import path a package must name for its selectors to count
// as reads of a [Config] field. A package that does not import this one cannot
// hold a [Config].
const modulePath = "github.com/stxkxs/agentfs/internal/config"

// TestEverySettingHasAConsumer closes the direction
// TestLimitsNamesEveryConfigField leaves open. That test asserts every field is
// published; this one asserts every published field is read. A setting nothing
// reads is a flag an operator sets, a summary the reference prints and a
// behaviour the binary does not have — the failure this package's own
// documentation is most exposed to, because a ceiling is easier to describe
// than to apply.
//
// The scan is syntactic rather than typed, so that the whole module is checked
// from the standard library. It finds the packages outside this one whose
// non-test files import it, collects the reads of a [Config] field in them, and
// requires each row of [Limits] to have one. The unit is the package rather
// than the file, because a [Config] reaches the rest of a package through a
// field of a struct declared in it and the file holding the read need not name
// this package at all.
//
// A selector counts as a read of Config.Field when it is Holder.Field, where
// Holder is a name the module declares somewhere as a [Config] — a struct
// field, a parameter or a variable. Three exclusions follow from that rule and
// each of them exists to stop a row passing on a name that is not this
// package's:
//
//   - A package qualifier is not a holder, so fsx.Root names a type rather than
//     satisfying the row for Config.Root.
//   - A field of some other struct is not a holder, so app.Options.Root does
//     not satisfy it either.
//   - A selector on the left of an assignment is a write, and a value written
//     and never read is the defect rather than the consumer. Config.Root is set
//     from the positional workspace argument, and counting that write would
//     leave the one row whose value could go unused as the one row that cannot
//     fail.
//
// Two limits remain, and both are deliberate:
//
//   - A holder is matched by name and not by type, so a variable named cfg
//     holding something other than a [Config] would satisfy a row whose field
//     name it happens to carry. The names in play are the ones this module
//     declares a [Config] under, which keeps the coincidence narrow.
//   - Reflection is invisible. Flag registration reaches every field through
//     reflect.Value.FieldByName, and counting that as a consumer would make the
//     test pass for every row unconditionally.
func TestEverySettingHasAConsumer(t *testing.T) {
	read := configFieldsReadOutsideThisPackage(t)
	for _, l := range Limits() {
		if _, ok := read[l.Name]; !ok {
			t.Errorf("Config.%s (--%s) is published and never read: no file outside %s reads it from a Config",
				l.Name, l.Flag, modulePath)
		}
	}
}

// configFieldsReadOutsideThisPackage returns the set of [Config] field names
// read in the non-test files of every package outside this one that imports it.
func configFieldsReadOutsideThisPackage(t *testing.T) map[string]struct{} {
	t.Helper()
	files := productionFilesImportingConfig(t)
	holders := configHolderNames(files)
	if len(holders) == 0 {
		t.Fatalf("no file outside %s declares a Config; the scan found nothing to read fields from", modulePath)
	}

	out := map[string]struct{}{}
	for _, f := range files {
		written := assignmentTargets(f)
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, isWrite := written[sel]; isWrite {
				return true
			}
			if _, held := holders[baseName(sel.X)]; held {
				out[sel.Sel.Name] = struct{}{}
			}
			return true
		})
	}
	return out
}

// productionFilesImportingConfig parses the non-test files of every package
// outside this one that imports it.
func productionFilesImportingConfig(t *testing.T) []*ast.File {
	t.Helper()
	root := moduleRoot(t)
	self := filepath.Join(root, filepath.FromSlash("internal/config"))
	fset := token.NewFileSet()
	var out []*ast.File

	err := filepath.WalkDir(root, func(dir string, d os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case !d.IsDir():
			return nil
		case d.Name() == "testdata":
			return filepath.SkipDir
		case dir == self:
			return filepath.SkipDir
		}

		files, parseErr := parseProductionFiles(fset, dir)
		if parseErr != nil {
			return parseErr
		}
		if !slices.ContainsFunc(files, func(f *ast.File) bool { return imports(f, modulePath) }) {
			return nil
		}
		out = append(out, files...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no package outside %s imports it; the scan found nothing to check against", modulePath)
	}
	return out
}

// configHolderNames returns the names the given files declare a [Config] under:
// struct fields, parameters, results and variables typed config.Config or
// *config.Config. A read of a setting is a selector taken on one of them.
func configHolderNames(files []*ast.File) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range files {
		qualifier, ok := importAlias(f, modulePath)
		if !ok {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.Field:
				if isConfigType(decl.Type, qualifier) {
					collectNames(out, decl.Names)
				}
			case *ast.ValueSpec:
				if isConfigType(decl.Type, qualifier) {
					collectNames(out, decl.Names)
				}
			}
			return true
		})
	}
	return out
}

// collectNames adds each identifier to out.
func collectNames(out map[string]struct{}, names []*ast.Ident) {
	for _, name := range names {
		out[name.Name] = struct{}{}
	}
}

// isConfigType reports whether expr writes the [Config] type, or a pointer to
// it, under the qualifier the file imports this package as.
func isConfigType(expr ast.Expr, qualifier string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Config" {
		return false
	}
	base, ok := sel.X.(*ast.Ident)
	return ok && base.Name == qualifier
}

// baseName returns the name a selector is taken on: the identifier itself, or
// the final field of a chain such as opts.Config. Anything else has no name,
// and returning the empty string keeps it out of the holder set.
func baseName(expr ast.Expr) string {
	switch base := expr.(type) {
	case *ast.Ident:
		return base.Name
	case *ast.SelectorExpr:
		return base.Sel.Name
	default:
		return ""
	}
}

// assignmentTargets returns the selectors file assigns to with = or :=, which
// are writes rather than reads. A compound assignment such as x.f += 1 reads
// the field before it writes it and is therefore not collected, and neither is
// the inner selector of a.b.c = v: only c is written there, and a.b is read to
// reach it.
func assignmentTargets(file *ast.File) map[*ast.SelectorExpr]struct{} {
	out := map[*ast.SelectorExpr]struct{}{}
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || (as.Tok != token.ASSIGN && as.Tok != token.DEFINE) {
			return true
		}
		for _, lhs := range as.Lhs {
			if sel, isSel := lhs.(*ast.SelectorExpr); isSel {
				out[sel] = struct{}{}
			}
		}
		return true
	})
	return out
}

// parseProductionFiles parses the non-test Go files directly in dir.
func parseProductionFiles(fset *token.FileSet, dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, f)
	}
	return out, nil
}

// imports reports whether file imports path.
func imports(file *ast.File, path string) bool {
	_, ok := importAlias(file, path)
	return ok
}

// importAlias returns the identifier file qualifies path's members with: the
// alias where one is written, and the last path segment otherwise.
func importAlias(file *ast.File, path string) (string, bool) {
	for _, spec := range file.Imports {
		got, err := strconv.Unquote(spec.Path.Value)
		if err != nil || got != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return got[strings.LastIndexByte(got, '/')+1:], true
	}
	return "", false
}

// moduleRoot returns the directory holding go.mod, found by walking up from the
// package directory the test runs in.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
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
