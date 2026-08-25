package keys

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// globalClaim returns the global binding that owns a spelling.
func globalClaim(key string) (Binding, bool) {
	for _, b := range defaultBindings {
		if b.Scope == ScopeGlobal && slices.Contains(b.Keys, key) {
			return b, true
		}
	}
	return Binding{}, false
}

func TestDefaultClaimsEachKeyOncePerScope(t *testing.T) {
	t.Parallel()

	owner := make(map[Scope]map[string]Binding, len(scopeOrder))
	for _, b := range defaultBindings {
		claimed, ok := owner[b.Scope]
		if !ok {
			claimed = make(map[string]Binding)
			owner[b.Scope] = claimed
		}
		for _, k := range b.Keys {
			if prior, taken := claimed[k]; taken {
				t.Errorf("%v binds %q to both %v and %v; the later claim is unreachable",
					b.Scope, k, prior.Action, b.Action)
			}
			claimed[k] = b
		}
	}
}

func TestDefaultDeclaresEveryScopeOverride(t *testing.T) {
	t.Parallel()

	// Every scope binding on a key the global table also claims. A key missing
	// from this set resolves differently from what the operator learned
	// elsewhere, with nothing on screen saying so.
	want := map[string]bool{
		"search esc": true,
	}
	got := make(map[string]bool, len(want))

	for _, b := range defaultBindings {
		if b.Scope == ScopeGlobal {
			continue
		}
		for _, k := range b.Keys {
			outer, shadowed := globalClaim(k)
			if !shadowed {
				continue
			}
			got[b.Scope.String()+" "+k] = true
			if b.Hidden {
				t.Errorf("%v hides its override of %q, so the footer keeps showing %q",
					b.Scope, k, outer.Help)
			}
			if b.Help == outer.Help {
				t.Errorf("%v overrides %q with the global wording %q, so the footer reads the same either way",
					b.Scope, k, b.Help)
			}
		}
	}

	for id := range want {
		if !got[id] {
			t.Errorf("%q overrides no global key; drop it from the expected set", id)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("%q overrides a global key without being declared here", id)
		}
	}
}

func TestDefaultBindingsAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, b := range defaultBindings {
		id := b.Scope.String() + " " + strings.Join(b.Keys, keySeparator)
		if len(b.Keys) == 0 {
			t.Errorf("%v binds %v to no key", b.Scope, b.Action)
		}
		if b.Action == ActionNone {
			t.Errorf("%s requests no action", id)
		}
		seen := make(map[string]bool, len(b.Keys))
		for _, k := range b.Keys {
			if k == "" {
				t.Errorf("%s carries an empty spelling", id)
			}
			if seen[k] {
				t.Errorf("%s lists %q twice", id, k)
			}
			seen[k] = true
			if strings.TrimSpace(k) != k {
				t.Errorf("%s pads %q, which no terminal reports", id, k)
			}
		}
	}
}

func TestDefaultHelpReadsTheSameEverywhere(t *testing.T) {
	t.Parallel()

	for _, b := range defaultBindings {
		id := b.Scope.String() + " " + strings.Join(b.Keys, keySeparator)
		if b.Help == "" {
			t.Errorf("%s carries no help, so it reaches the footer and the overlay unlabelled", id)
			continue
		}
		if strings.TrimSpace(b.Help) != b.Help {
			t.Errorf("%s pads its help: %q", id, b.Help)
		}
		if strings.HasSuffix(b.Help, ".") {
			t.Errorf("%s ends its help with a period: %q", id, b.Help)
		}
		if first, _ := utf8.DecodeRuneInString(b.Help); unicode.IsUpper(first) {
			t.Errorf("%s capitalizes its help: %q", id, b.Help)
		}
	}
}

func TestDefaultHidesOnlyAliases(t *testing.T) {
	t.Parallel()

	advertised := make(map[Scope]map[Action]bool, len(scopeOrder))
	for _, b := range defaultBindings {
		if b.Hidden {
			continue
		}
		actions, ok := advertised[b.Scope]
		if !ok {
			actions = make(map[Action]bool)
			advertised[b.Scope] = actions
		}
		actions[b.Action] = true
	}
	for _, b := range defaultBindings {
		if b.Hidden && !advertised[b.Scope][b.Action] {
			t.Errorf("%v hides %v behind %v with nothing advertised for it", b.Scope, b.Action, b.Keys)
		}
	}
}

func TestDefaultBindsEveryAction(t *testing.T) {
	t.Parallel()

	bound := make(map[Action]bool, len(actionNames))
	for _, b := range defaultBindings {
		bound[b.Action] = true
	}
	for i := range actionNames {
		a := Action(i)
		if a == ActionNone {
			continue
		}
		if !bound[a] {
			t.Errorf("no key requests %v, so the application can never be asked for it", a)
		}
	}
}

func TestDefaultCoversTheDocumentedKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key    string
		scope  Scope
		action Action
	}{
		{"j", ScopeTree, ActionDown},
		{"down", ScopeTree, ActionDown},
		{"k", ScopeTree, ActionUp},
		{"up", ScopeTree, ActionUp},
		{"h", ScopeTree, ActionCollapse},
		{"left", ScopeTree, ActionCollapse},
		{"l", ScopeTree, ActionExpand},
		{"right", ScopeTree, ActionExpand},
		{keyEnter, ScopeTree, ActionOpen},
		{keyEnter, ScopeFeed, ActionOpen},
		{keyEnter, ScopeRuns, ActionOpen},
		{"g", ScopeTree, ActionTop},
		{"G", ScopeTree, ActionBottom},
		{"ctrl+u", ScopeTree, ActionPageUp},
		{"ctrl+d", ScopeTree, ActionPageDown},
		{"pgup", ScopeFeed, ActionPageUp},
		{"pgdown", ScopeFeed, ActionPageDown},
		{"tab", ScopePreview, ActionNextPane},
		{"shift+tab", ScopePreview, ActionPrevPane},
		{"/", ScopePreview, ActionSearch},
		{"n", ScopePreview, ActionNextMatch},
		{"N", ScopePreview, ActionPrevMatch},
		{keyEsc, ScopeRuns, ActionCancel},
		{keyEsc, ScopeTree, ActionCancel},
		{keyEsc, ScopePreview, ActionCancel},
		{keyEsc, ScopeSearch, ActionClearSearch},
		{"R", ScopeRuns, ActionToggleRuns},
		{"r", ScopeRuns, ActionReload},
		{"?", ScopeGlobal, ActionToggleHelp},
		{"b", ScopeGlobal, ActionToggleBudgets},
		{"f", ScopeFeed, ActionFollow},
		{keyQuit, ScopePreview, ActionQuit},
		{"ctrl+c", ScopePreview, ActionQuit},
	}
	r := Default()
	for _, c := range cases {
		t.Run(c.scope.String()+" "+c.key, func(t *testing.T) {
			t.Parallel()
			got, ok := r.Resolve(c.key, c.scope)
			if !ok {
				t.Fatalf("Resolve(%q, %v) found no binding", c.key, c.scope)
			}
			if got != c.action {
				t.Errorf("Resolve(%q, %v) = %v, want %v", c.key, c.scope, got, c.action)
			}
		})
	}
}
