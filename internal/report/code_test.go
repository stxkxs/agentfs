package report

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/diag"
)

// declaredCodes reads the package's own source for every constant of type
// Code. Reading the source rather than restating the constants is what makes
// the completeness check below a gate: a constant added without a registry row
// fails it, and a test that listed the constants by hand would not.
func declaredCodes(t *testing.T) map[string]Code {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	out := make(map[string]Code)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		collectCodes(t, name, src, out)
	}
	return out
}

// collectCodes records every Code constant src declares into out. It reads
// the three forms a Code reaches a declaration through: a typed spec
// (CodeOK Code = 0), an untyped spec holding a conversion (CodeOK = Code(0)),
// and a spec in a const block that omits its value and so repeats the previous
// spec's type and expression list.
func collectCodes(t *testing.T, file string, src []byte, out map[string]Code) {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		var lastType ast.Expr
		var lastValues []ast.Expr
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typ, values := value.Type, value.Values
			if len(values) == 0 {
				typ, values = lastType, lastValues
			}
			lastType, lastValues = typ, values

			ident, named := typ.(*ast.Ident)
			switch {
			case named && ident.Name == "Code":
				collectTypedCodes(t, file, value.Names, values, out)
			case typ == nil:
				collectConvertedCodes(value.Names, values, out)
			}
		}
	}
}

// collectTypedCodes reads a spec that names Code as its type. Every such
// constant has to be readable, so a form the parser cannot evaluate fails here
// rather than dropping the constant out of the completeness check.
func collectTypedCodes(t *testing.T, file string, names []*ast.Ident, values []ast.Expr, out map[string]Code) {
	t.Helper()
	for i, ident := range names {
		if i >= len(values) {
			t.Fatalf("%s: constant %s declares no value, so the registry check cannot read it", file, ident.Name)
		}
		n, ok := intLiteral(values[i])
		if !ok {
			t.Fatalf("%s: constant %s is typed Code but does not hold an integer literal, so the registry check cannot read it", file, ident.Name)
		}
		out[ident.Name] = Code(n)
	}
}

// collectConvertedCodes reads a spec with no declared type, where only a
// Code(n) conversion is a Code constant. A bare literal in such a spec is an
// untyped constant of some other kind, so it is passed over.
func collectConvertedCodes(names []*ast.Ident, values []ast.Expr, out map[string]Code) {
	for i, ident := range names {
		if i >= len(values) {
			continue
		}
		call, ok := values[i].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			continue
		}
		fun, ok := call.Fun.(*ast.Ident)
		if !ok || fun.Name != "Code" {
			continue
		}
		if n, ok := intLiteral(call.Args[0]); ok {
			out[ident.Name] = Code(n)
		}
	}
}

// intLiteral evaluates the integer literal forms an exit status is written in,
// including the negation a caller could reach through Code conversion.
func intLiteral(expr ast.Expr) (int, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.INT {
			return 0, false
		}
		n, err := strconv.Atoi(v.Value)
		return n, err == nil
	case *ast.UnaryExpr:
		if v.Op != token.SUB {
			return 0, false
		}
		n, ok := intLiteral(v.X)
		return -n, ok
	}
	return 0, false
}

func TestDeclaredCodesAreParsed(t *testing.T) {
	declared := declaredCodes(t)
	if len(declared) == 0 {
		t.Fatal("parsed no Code constants; the completeness check is not reading the package source")
	}
	for _, name := range []string{"CodeOK", "CodeFindings", "CodeUsage", "CodePath", "CodeInternal", "CodeInterrupted"} {
		if _, ok := declared[name]; !ok {
			t.Errorf("source parse missed %s", name)
		}
	}
}

// The completeness check is only as wide as the declaration forms its parser
// recognizes, so both forms are read here against a source the package does
// not itself contain. The kind vocabularies and the schema identifiers are
// untyped string constants and must not be mistaken for exit statuses.
func TestCodeParserReadsEveryDeclarationForm(t *testing.T) {
	src := []byte(`package report

const CodeTyped Code = 11

const CodeConverted = Code(12)

const (
	CodeGroupedA Code = 13
	CodeGroupedB Code = 14
)

const CodeNegative = Code(-15)

const (
	CodeWritten  Code = 18
	CodeInherits      // repeats the expression list above it
)

const (
	untypedInteger = 16
	untypedString  = "17"
	schemaLike     = "agentfs/report/v1"
	kindLike       = "scan"
	notAConversion = len("abc")
)
`)

	got := make(map[string]Code)
	collectCodes(t, "synthetic.go", src, got)

	want := map[string]Code{
		"CodeTyped":     11,
		"CodeConverted": 12,
		"CodeGroupedA":  13,
		"CodeGroupedB":  14,
		"CodeNegative":  -15,
		"CodeWritten":   18,
		"CodeInherits":  18,
	}
	if !maps.Equal(got, want) {
		t.Errorf("collectCodes = %v, want %v", got, want)
	}
}

func TestRegistryNamesEveryDeclaredCode(t *testing.T) {
	declared := declaredCodes(t)

	rows := make(map[Code]CodeInfo, len(declared))
	for _, info := range Codes() {
		if prior, dup := rows[info.Code]; dup {
			t.Errorf("codes %s and %s both claim exit status %d", prior.Name, info.Name, int(info.Code))
		}
		rows[info.Code] = info
	}

	for name, code := range declared {
		if _, ok := rows[code]; !ok {
			t.Errorf("constant %s (exit %d) has no registry row", name, int(code))
		}
	}

	values := make(map[Code]string, len(declared))
	for name, code := range declared {
		if prior, dup := values[code]; dup {
			t.Errorf("constants %s and %s share exit status %d", prior, name, int(code))
		}
		values[code] = name
	}

	for code, info := range rows {
		if _, ok := values[code]; !ok {
			t.Errorf("registry row %q describes exit status %d, which no constant declares", info.Name, int(code))
		}
	}
}

func TestCodesAscending(t *testing.T) {
	got := Codes()
	if len(got) == 0 {
		t.Fatal("registry is empty")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Code >= got[i].Code {
			t.Errorf("row %d (%d) does not precede row %d (%d)", i-1, int(got[i-1].Code), i, int(got[i].Code))
		}
	}
}

// Codes imposes ascending order rather than inheriting the registry's
// declaration order, so a row added anywhere in the table reaches the
// reference in the same place.
func TestCodesSortsRegardlessOfRegistryOrder(t *testing.T) {
	original := registry
	t.Cleanup(func() { registry = original })

	reversed := slices.Clone(original)
	slices.Reverse(reversed)
	registry = reversed
	head := reversed[0].Code

	got := Codes()
	if len(got) != len(original) {
		t.Fatalf("Codes returned %d rows, want %d", len(got), len(original))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Code >= got[i].Code {
			t.Fatalf("Codes is not ascending at row %d: %d then %d", i, int(got[i-1].Code), int(got[i].Code))
		}
	}
	if registry[0].Code != head {
		t.Error("Codes sorted the registry in place rather than a copy of it")
	}
}

func TestCodesReturnsCopy(t *testing.T) {
	first := Codes()
	first[0] = CodeInfo{Code: -1, Name: "clobbered", Summary: "Mutated by a caller."}

	second := Codes()
	if second[0].Name == "clobbered" {
		t.Fatal("mutating the result of Codes changed the registry")
	}
	if second[0].Code != CodeOK {
		t.Errorf("first row is exit %d, want %d", int(second[0].Code), int(CodeOK))
	}
}

func TestCodeInfoIsReferenceReady(t *testing.T) {
	names := make(map[string]Code)
	for _, info := range Codes() {
		if info.Name == "" {
			t.Errorf("exit status %d has an empty name", int(info.Code))
			continue
		}
		if info.Name != strings.ToLower(info.Name) {
			t.Errorf("name %q is not lowercase", info.Name)
		}
		if prior, dup := names[info.Name]; dup {
			t.Errorf("name %q describes both exit %d and exit %d", info.Name, int(prior), int(info.Code))
		}
		names[info.Name] = info.Code

		if info.Summary == "" {
			t.Errorf("%s has an empty summary", info.Name)
			continue
		}
		if !strings.HasSuffix(info.Summary, ".") {
			t.Errorf("%s summary is not a sentence: %q", info.Name, info.Summary)
		}
		if first, _ := utf8.DecodeRuneInString(info.Summary); !unicode.IsUpper(first) {
			t.Errorf("%s summary does not start with a capital: %q", info.Name, info.Summary)
		}
	}
}

func TestCodeString(t *testing.T) {
	tests := []struct {
		name string
		code Code
		want string
	}{
		{"ok", CodeOK, "ok"},
		{"findings", CodeFindings, "findings"},
		{"usage", CodeUsage, "usage"},
		{"path", CodePath, "path"},
		{"internal", CodeInternal, "internal"},
		{"interrupted", CodeInterrupted, "interrupted"},
		{"unregistered", Code(42), "exit 42"},
		{"negative", Code(-1), "exit -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.code.String(); got != tt.want {
				t.Errorf("Code(%d).String() = %q, want %q", int(tt.code), got, tt.want)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	info, ok := Lookup(CodePath)
	if !ok {
		t.Fatalf("CodePath is not registered")
	}
	if info.Code != CodePath || info.Name != "path" {
		t.Errorf("Lookup(CodePath) = %+v", info)
	}

	if got, ok := Lookup(Code(9999)); ok {
		t.Errorf("Lookup(9999) = %+v, want miss", got)
	} else if got != (CodeInfo{}) {
		t.Errorf("a missed lookup returned %+v, want the zero value", got)
	}
}

func TestExitFor(t *testing.T) {
	tests := []struct {
		name  string
		given []diag.Diagnostic
		want  Code
	}{
		{"none", nil, CodeOK},
		{"empty", []diag.Diagnostic{}, CodeOK},
		{
			"info only",
			[]diag.Diagnostic{
				{Code: diag.CodeLegacyFilename, Severity: diag.Info},
				{Code: diag.CodeUnknownMember, Severity: diag.Info},
			},
			CodeOK,
		},
		{
			"warning",
			[]diag.Diagnostic{{Code: diag.CodeStale, Severity: diag.Warning}},
			CodeFindings,
		},
		{
			"error",
			[]diag.Diagnostic{{Code: diag.CodeNotJSON, Severity: diag.Error}},
			CodeFindings,
		},
		{
			"warning after info",
			[]diag.Diagnostic{
				{Code: diag.CodeLegacyFilename, Severity: diag.Info},
				{Code: diag.CodeStale, Severity: diag.Warning},
			},
			CodeFindings,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitFor(tt.given); got != tt.want {
				t.Errorf("ExitFor = %s, want %s", got, tt.want)
			}
		})
	}
}
