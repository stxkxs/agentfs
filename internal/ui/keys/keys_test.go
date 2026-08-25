package keys

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestActionString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action Action
		want   string
	}{
		{ActionNone, "none"},
		{ActionUp, "up"},
		{ActionDown, "down"},
		{ActionPageUp, "page-up"},
		{ActionPageDown, "page-down"},
		{ActionTop, "top"},
		{ActionBottom, "bottom"},
		{ActionExpand, "expand"},
		{ActionCollapse, "collapse"},
		{ActionOpen, "open"},
		{ActionNextPane, "next-pane"},
		{ActionPrevPane, "prev-pane"},
		{ActionSearch, "search"},
		{ActionNextMatch, "next-match"},
		{ActionPrevMatch, "prev-match"},
		{ActionClearSearch, "clear-search"},
		{ActionToggleRuns, "toggle-runs"},
		{ActionReload, "reload"},
		{ActionToggleHelp, "toggle-help"},
		{ActionToggleBudgets, "toggle-budgets"},
		{ActionFollow, "follow"},
		{ActionCancel, "cancel"},
		{ActionQuit, "quit"},
		{Action(-1), unknownName},
		{ActionQuit + 1, unknownName},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.action.String(); got != c.want {
				t.Errorf("Action(%d).String() = %q, want %q", int(c.action), got, c.want)
			}
		})
	}
}

func TestActionSpellingsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]Action, len(actionNames))
	for i := range actionNames {
		a := Action(i)
		if prior, dup := seen[a.String()]; dup {
			t.Errorf("%v and %v both spell %q", prior, a, a.String())
		}
		seen[a.String()] = a
	}
	if _, taken := seen[unknownName]; taken {
		t.Errorf("an action spells %q, which stops distinguishing a value outside the vocabulary", unknownName)
	}
}

func TestScopeString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope Scope
		want  string
	}{
		{ScopeGlobal, "global"},
		{ScopeTree, "tree"},
		{ScopePreview, "preview"},
		{ScopeFeed, "feed"},
		{ScopeRuns, "runs"},
		{ScopeSearch, "search"},
		{Scope(-1), unknownName},
		{ScopeSearch + 1, unknownName},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			if got := c.scope.String(); got != c.want {
				t.Errorf("Scope(%d).String() = %q, want %q", int(c.scope), got, c.want)
			}
		})
	}
}

func TestScopeOrderCoversEveryScope(t *testing.T) {
	t.Parallel()

	if len(scopeOrder) != len(scopeNames) {
		t.Fatalf("scopeOrder lists %d scopes, the vocabulary has %d", len(scopeOrder), len(scopeNames))
	}
	seen := make(map[Scope]bool, len(scopeOrder))
	for _, s := range scopeOrder {
		if seen[s] {
			t.Errorf("scopeOrder lists %v twice", s)
		}
		seen[s] = true
	}
	for i := range scopeNames {
		if !seen[Scope(i)] {
			t.Errorf("scopeOrder omits %v, so its bindings reach no help section", Scope(i))
		}
	}
}

func TestScopeCaptures(t *testing.T) {
	t.Parallel()

	for _, s := range scopeOrder {
		want := s == ScopeSearch
		if got := s.Captures(); got != want {
			t.Errorf("%v.Captures() = %t, want %t", s, got, want)
		}
	}
}

// TestPackageReachesForNoTerminalFramework enforces the boundary the package
// doc states: resolution is framework-independent, so the only module this
// package imports is the text-measuring one the footer needs. A terminal
// framework here would put the table behind a program loop and out of reach of
// a plain test.
func TestPackageReachesForNoTerminalFramework(t *testing.T) {
	t.Parallel()

	const textxPath = "github.com/stxkxs/agentfs/internal/textx"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	fset := token.NewFileSet()
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote %s: %v", name, spec.Path.Value, err)
			}
			if path == textxPath {
				continue
			}
			// A path whose leading element carries a dot names a module; one
			// without names a standard library package.
			if root, _, _ := strings.Cut(path, "/"); strings.Contains(root, ".") {
				t.Errorf("%s imports %q; keys resolves a press without a terminal framework", name, path)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no source file was parsed, so the import boundary went unchecked")
	}
}
