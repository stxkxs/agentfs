package keys

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	keyEnter = "enter"
	keyEsc   = "esc"
	keyQuit  = "q"

	// clobbered is what a test writes into a returned binding to prove the
	// registry did not hand out its own memory.
	clobbered = "clobbered"
)

// scan returns the action a scope's own bindings give key, reading the table
// top to bottom the way [New] indexes it.
func scan(bindings []Binding, key string, scope Scope) (Action, bool) {
	for _, b := range bindings {
		if b.Scope == scope && slices.Contains(b.Keys, key) {
			return b.Action, true
		}
	}
	return ActionNone, false
}

// referenceResolve is the resolution rule restated as a linear search, so a
// property test compares the indexed answer against the table itself rather
// than against the index that produced it.
func referenceResolve(bindings []Binding, key string, scope Scope) (Action, bool) {
	if a, ok := scan(bindings, key, scope); ok {
		return a, true
	}
	if scope == ScopeGlobal || scope.Captures() {
		return ActionNone, false
	}
	return scan(bindings, key, ScopeGlobal)
}

// tableKeys returns every spelling the table mentions, sorted.
func tableKeys(bindings []Binding) []string {
	seen := make(map[string]bool)
	for _, b := range bindings {
		for _, k := range b.Keys {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestResolveMatchesTheTable(t *testing.T) {
	t.Parallel()

	r := Default()
	for _, key := range append(tableKeys(defaultBindings), "z", "ctrl+z", "") {
		for _, scope := range scopeOrder {
			wantAction, wantOK := referenceResolve(defaultBindings, key, scope)
			gotAction, gotOK := r.Resolve(key, scope)
			if gotAction != wantAction || gotOK != wantOK {
				t.Errorf("Resolve(%q, %v) = %v, %t; want %v, %t",
					key, scope, gotAction, gotOK, wantAction, wantOK)
			}
		}
	}
}

func TestResolvePrefersTheScopeOverGlobal(t *testing.T) {
	t.Parallel()

	r := Default()
	if a, ok := r.Resolve(keyEsc, ScopeGlobal); a != ActionCancel || !ok {
		t.Fatalf("global esc = %v, %t; want cancel, true", a, ok)
	}
	if a, ok := r.Resolve(keyEsc, ScopeSearch); a != ActionClearSearch || !ok {
		t.Errorf("search esc = %v, %t; want clear-search, true", a, ok)
	}
	if a, ok := r.Resolve(keyEnter, ScopeSearch); a != ActionNextMatch || !ok {
		t.Errorf("search enter = %v, %t; want next-match, true", a, ok)
	}
	if a, ok := r.Resolve("h", ScopePreview); a != ActionPrevPane || !ok {
		t.Errorf("preview h = %v, %t; want prev-pane, true", a, ok)
	}
}

func TestResolveFallsBackToGlobal(t *testing.T) {
	t.Parallel()

	r := Default()
	for _, scope := range []Scope{ScopeTree, ScopePreview, ScopeFeed, ScopeRuns} {
		if a, ok := r.Resolve(keyQuit, scope); a != ActionQuit || !ok {
			t.Errorf("%v q = %v, %t; want quit, true", scope, a, ok)
		}
	}
}

func TestResolveInGlobalScopeIgnoresScopedBindings(t *testing.T) {
	t.Parallel()

	r := Default()
	for _, key := range []string{"l", "right"} {
		if a, ok := r.Resolve(key, ScopeGlobal); ok {
			t.Errorf("global %q = %v, true; a tree key must not be live everywhere", key, a)
		}
	}
}

func TestSearchScopeSwallowsUnboundKeys(t *testing.T) {
	t.Parallel()

	r := Default()
	bound := map[string]Action{
		keyEsc:   ActionClearSearch,
		keyEnter: ActionNextMatch,
		"ctrl+n": ActionNextMatch,
		"ctrl+p": ActionPrevMatch,
	}
	for key, want := range bound {
		if a, ok := r.Resolve(key, ScopeSearch); a != want || !ok {
			t.Errorf("search %q = %v, %t; want %v, true", key, a, ok, want)
		}
	}
	for _, key := range append(tableKeys(defaultBindings), "a", "Z", "1", " ") {
		if _, taken := bound[key]; taken {
			continue
		}
		if a, ok := r.Resolve(key, ScopeSearch); ok {
			t.Errorf("search %q = %v, true; an unbound key is text the operator typed", key, a)
		}
	}
}

func TestNewKeepsTheFirstClaimOnAKey(t *testing.T) {
	t.Parallel()

	r := New([]Binding{
		{Keys: []string{"x"}, Action: ActionTop, Scope: ScopeGlobal, Help: "first claim"},
		{Keys: []string{"x"}, Action: ActionBottom, Scope: ScopeGlobal, Help: "second claim"},
	})
	if a, ok := r.Resolve("x", ScopeGlobal); a != ActionTop || !ok {
		t.Errorf(`Resolve("x") = %v, %t; want top, true`, a, ok)
	}
	if got := len(r.Bindings()); got != 2 {
		t.Errorf("Bindings() kept %d bindings, want 2: a losing claim stays visible in help", got)
	}
}

func TestNewSanitizesHelp(t *testing.T) {
	t.Parallel()

	r := New([]Binding{
		{Keys: []string{"x"}, Action: ActionTop, Scope: ScopeGlobal, Help: "clear\x1b[2Jthe\nscreen"},
	})
	got := r.Bindings()[0].Help
	if strings.ContainsAny(got, "\x1b\n") {
		t.Errorf("Help = %q, want the escape and the line break neutralized", got)
	}
	if !strings.Contains(got, "clear") || !strings.Contains(got, "screen") {
		t.Errorf("Help = %q, want the readable text kept", got)
	}
}

func TestBindingsIsStableAndOwnedByTheCaller(t *testing.T) {
	t.Parallel()

	r := Default()
	first := r.Bindings()
	if len(first) != len(defaultBindings) {
		t.Fatalf("Bindings() returned %d, want %d", len(first), len(defaultBindings))
	}
	for i, b := range first {
		if b.Action != defaultBindings[i].Action || b.Scope != defaultBindings[i].Scope {
			t.Fatalf("Bindings()[%d] = %v/%v, want %v/%v",
				i, b.Scope, b.Action, defaultBindings[i].Scope, defaultBindings[i].Action)
		}
	}

	first[0].Keys[0] = clobbered
	first[0].Help = clobbered
	second := r.Bindings()
	if second[0].Keys[0] == clobbered || second[0].Help == clobbered {
		t.Error("Bindings() shares memory with the registry: a caller can rewrite the table")
	}
	if a, ok := r.Resolve(second[0].Keys[0], ScopeGlobal); !ok || a != second[0].Action {
		t.Errorf("Resolve(%q) = %v, %t after a caller rewrote its copy", second[0].Keys[0], a, ok)
	}
}

// contradictoryBindings claim keys twice within a scope and once across
// scopes, so a table that disagrees with itself is exercised alongside the
// shipped one. A registry over them advertises only what [Registry.Resolve]
// answers with; the losing claims survive in [Registry.Help].
var contradictoryBindings = []Binding{
	{Keys: []string{"x", "y"}, Action: ActionTop, Scope: ScopeGlobal, Help: "go to the top"},
	{Keys: []string{"x"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit"},
	{Keys: []string{"z"}, Action: ActionReload, Scope: ScopeGlobal, Help: "reload"},
	{Keys: nil, Action: ActionFollow, Scope: ScopeGlobal, Help: "follow the tail"},
	{Keys: []string{"y"}, Action: ActionOpen, Scope: ScopeTree, Help: "open the selected file"},
	{Keys: []string{"y"}, Action: ActionCollapse, Scope: ScopeTree, Help: "collapse the directory"},
	{Keys: []string{"z"}, Action: ActionExpand, Scope: ScopeSearch, Help: "expand the directory"},
}

// tables are the binding sets every registry property is asserted over: the
// shipped one, and one written to disagree with itself.
var tables = map[string][]Binding{
	"default":       defaultBindings,
	"contradictory": contradictoryBindings,
}

// advertisedRef restates [Registry.ForScope] as a search over the table, so a
// property test compares the registry against the table rather than against
// the index built from it. Each spelling belongs to the first binding a press
// in s is resolved against; a binding left with none is not advertised.
func advertisedRef(bindings []Binding, s Scope) []Binding {
	order := make([]int, 0, len(bindings))
	for i, b := range bindings {
		if b.Scope == s {
			order = append(order, i)
		}
	}
	if s != ScopeGlobal && !s.Captures() {
		for i, b := range bindings {
			if b.Scope == ScopeGlobal {
				order = append(order, i)
			}
		}
	}

	owner := make(map[string]int, len(bindings))
	for _, i := range order {
		for _, k := range bindings[i].Keys {
			if _, taken := owner[k]; !taken {
				owner[k] = i
			}
		}
	}

	out := make([]Binding, 0, len(order))
	for _, i := range order {
		b := bindings[i]
		if b.Hidden {
			continue
		}
		live := make([]string, 0, len(b.Keys))
		for _, k := range b.Keys {
			if owner[k] == i && !slices.Contains(live, k) {
				live = append(live, k)
			}
		}
		if len(live) == 0 {
			continue
		}
		b.Keys = live
		out = append(out, b)
	}
	return out
}

func TestForScopeListsOnlyWhatResolves(t *testing.T) {
	t.Parallel()

	for name, bindings := range tables {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := New(bindings)
			for _, scope := range scopeOrder {
				for _, b := range r.ForScope(scope) {
					if b.Hidden {
						t.Errorf("ForScope(%v) returned a hidden binding for %v", scope, b.Keys)
					}
					if b.Help == "" {
						t.Errorf("ForScope(%v) returned %v with no help", scope, b.Keys)
					}
					if len(b.Keys) == 0 {
						t.Errorf("ForScope(%v) advertises %v with no key to press", scope, b.Action)
					}
					for _, k := range b.Keys {
						a, ok := r.Resolve(k, scope)
						if !ok || a != b.Action {
							t.Errorf("ForScope(%v) advertises %q as %v, but Resolve gives %v, %t",
								scope, k, b.Action, a, ok)
						}
					}
				}
			}
		})
	}
}

func TestForScopeMatchesTheTable(t *testing.T) {
	t.Parallel()

	for name, bindings := range tables {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := New(bindings)
			// A scope outside the vocabulary falls back the way a named one
			// does, so the footer of a pane added later is never blank.
			for _, scope := range append(slices.Clone(scopeOrder[:]), Scope(-3), ScopeSearch+7) {
				got, want := r.ForScope(scope), advertisedRef(bindings, scope)
				if !slices.EqualFunc(got, want, func(a, b Binding) bool {
					return a.Action == b.Action && a.Scope == b.Scope &&
						a.Help == b.Help && slices.Equal(a.Keys, b.Keys)
				}) {
					t.Errorf("ForScope(%v) = %v, want %v", scope, got, want)
				}
			}
		})
	}
}

func TestForScopeDropsAClaimAnEarlierBindingWon(t *testing.T) {
	t.Parallel()

	r := New(contradictoryBindings)
	for _, b := range r.ForScope(ScopeGlobal) {
		if b.Action == ActionQuit {
			t.Errorf("ForScope(global) advertises %v for quit, a claim %q already lost", b.Keys, "x")
		}
		if b.Action == ActionFollow {
			t.Errorf("ForScope(global) advertises follow, which no key requests")
		}
		if b.Action == ActionTop && !slices.Equal(b.Keys, []string{"x", "y"}) {
			t.Errorf("ForScope(global) lists top as %v, want [x y]", b.Keys)
		}
	}
	for _, b := range r.ForScope(ScopeTree) {
		if b.Action == ActionCollapse {
			t.Errorf("ForScope(tree) advertises %v for collapse, a claim %q already lost", b.Keys, "y")
		}
		if b.Action == ActionTop && !slices.Equal(b.Keys, []string{"x"}) {
			t.Errorf("ForScope(tree) lists top as %v, want [x]: the tree claims %q", b.Keys, "y")
		}
	}
}

func TestForScopePutsScopeBindingsFirst(t *testing.T) {
	t.Parallel()

	got := Default().ForScope(ScopeTree)
	for i, b := range got {
		if b.Scope == ScopeGlobal {
			for _, rest := range got[i:] {
				if rest.Scope != ScopeGlobal {
					t.Fatalf("ForScope(tree) put %v after a global binding", rest.Scope)
				}
			}
			return
		}
	}
	t.Fatal("ForScope(tree) returned no global bindings to fall back to")
}

func TestForScopeDropsGlobalKeysTheScopeClaims(t *testing.T) {
	t.Parallel()

	r := New([]Binding{
		{Keys: []string{"x", "y"}, Action: ActionTop, Scope: ScopeGlobal, Help: "go to the top"},
		{Keys: []string{"z"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit"},
		{Keys: []string{"x"}, Action: ActionBottom, Scope: ScopeTree, Help: "go to the bottom"},
		{Keys: []string{"z"}, Action: ActionReload, Scope: ScopeTree, Help: "reload"},
	})
	var top, quit []string
	for _, b := range r.ForScope(ScopeTree) {
		switch b.Action {
		case ActionTop:
			top = b.Keys
		case ActionQuit:
			quit = b.Keys
		default:
		}
	}
	if !slices.Equal(top, []string{"y"}) {
		t.Errorf("the partly claimed global binding lists %v, want [y]", top)
	}
	if quit != nil {
		t.Errorf("the fully claimed global binding is still advertised as %v", quit)
	}
}

func TestForScopeGivesACapturingScopeNoFallback(t *testing.T) {
	t.Parallel()

	for _, b := range Default().ForScope(ScopeSearch) {
		if b.Scope != ScopeSearch {
			t.Errorf("ForScope(search) advertises a %v binding for %v, which the prompt swallows",
				b.Scope, b.Keys)
		}
	}
}

func TestHelpCoversEveryBindingOnce(t *testing.T) {
	t.Parallel()

	r := Default()
	counted := 0
	seenScope := make(map[Scope]bool)
	for _, section := range r.Help() {
		if seenScope[section.Scope] {
			t.Errorf("Help() listed %v twice", section.Scope)
		}
		seenScope[section.Scope] = true
		if len(section.Bindings) == 0 {
			t.Errorf("Help() listed %v with no bindings", section.Scope)
		}
		for _, b := range section.Bindings {
			if b.Scope != section.Scope {
				t.Errorf("Help() filed a %v binding under %v", b.Scope, section.Scope)
			}
			counted++
		}
	}
	if counted != len(defaultBindings) {
		t.Errorf("Help() listed %d bindings, the table has %d", counted, len(defaultBindings))
	}
}

func TestHelpListsHiddenBindings(t *testing.T) {
	t.Parallel()

	for _, section := range Default().Help() {
		for _, b := range section.Bindings {
			if b.Hidden && slices.Contains(b.Keys, "ctrl+c") {
				return
			}
		}
	}
	t.Error("Help() omits hidden bindings, so an unadvertised key is undiscoverable")
}

func TestHelpFollowsScopeOrder(t *testing.T) {
	t.Parallel()

	want := []Scope{ScopeGlobal, ScopeTree, ScopePreview, ScopeFeed, ScopeRuns, ScopeSearch}
	got := make([]Scope, 0, len(want))
	for _, section := range Default().Help() {
		got = append(got, section.Scope)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Help() sections = %v, want %v", got, want)
	}
}

func TestHelpListsAScopeOutsideTheVocabulary(t *testing.T) {
	t.Parallel()

	extra := ScopeSearch + 3
	r := New([]Binding{
		{Keys: []string{"x"}, Action: ActionTop, Scope: extra, Help: "go to the top"},
		{Keys: []string{"y"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit"},
	})
	sections := r.Help()
	if len(sections) != 2 {
		t.Fatalf("Help() returned %d sections, want 2", len(sections))
	}
	if sections[0].Scope != ScopeGlobal || sections[1].Scope != extra {
		t.Errorf("Help() sections = %v, %v; want global then the trailing scope",
			sections[0].Scope, sections[1].Scope)
	}
}

func TestDefaultIsShared(t *testing.T) {
	t.Parallel()

	first, second := Default(), Default()
	if first != second {
		t.Error("Default() builds a registry per call")
	}
}

func TestRegistryServesGoroutinesAtOnce(t *testing.T) {
	t.Parallel()

	r := Default()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scope := scopeOrder[i%len(scopeOrder)]
			for range 200 {
				if a, ok := r.Resolve(keyEsc, scope); !ok || a == ActionNone {
					t.Errorf("Resolve(%q, %v) = %v, %t under concurrent use", keyEsc, scope, a, ok)
					return
				}
				owned := r.Bindings()
				owned[0].Keys[0] = clobbered
				owned[0].Help = clobbered
				r.ForScope(scope)[0].Keys[0] = clobbered
				r.Help()[0].Bindings[0].Help = clobbered
				r.Footer(scope, 80)
			}
		}()
	}
	wg.Wait()

	if a, ok := r.Resolve(defaultBindings[0].Keys[0], ScopeTree); !ok || a != defaultBindings[0].Action {
		t.Errorf("Resolve(%q) = %v, %t after concurrent readers rewrote their copies",
			defaultBindings[0].Keys[0], a, ok)
	}
}

func FuzzResolve(f *testing.F) {
	for _, key := range tableKeys(defaultBindings) {
		f.Add(key, 0)
	}
	f.Add("", -1)
	f.Add("ctrl+alt+shift+meta+a", 99)
	f.Add("\x1b[200~", 5)

	r := Default()
	f.Fuzz(func(t *testing.T, key string, rawScope int) {
		scope := Scope(rawScope)
		action, ok := r.Resolve(key, scope)
		wantAction, wantOK := referenceResolve(defaultBindings, key, scope)
		if action != wantAction || ok != wantOK {
			t.Fatalf("Resolve(%q, %d) = %v, %t; want %v, %t",
				key, rawScope, action, ok, wantAction, wantOK)
		}
		if !ok && action != ActionNone {
			t.Fatalf("Resolve(%q, %d) reported no binding but returned %v", key, rawScope, action)
		}
		if ok && action.String() == unknownName {
			t.Fatalf("Resolve(%q, %d) returned an action outside the vocabulary", key, rawScope)
		}
	})
}
