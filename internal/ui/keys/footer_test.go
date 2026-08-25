package keys

import (
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/textx"
)

// hintsFor renders the footer hints for a scope, in priority order.
func hintsFor(r *Registry, s Scope) []string {
	bindings := r.ForScope(s)
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, hint(b))
	}
	return out
}

func TestFooterFillsExactlyTheWidthGiven(t *testing.T) {
	t.Parallel()

	registries := map[string]*Registry{
		"default": Default(),
		"empty":   New(nil),
		"wide":    New([]Binding{{Keys: []string{"世"}, Action: ActionOpen, Scope: ScopeGlobal, Help: "開く"}}),
		// A help string ending in a mark that joins what follows it: the pad
		// the line is filled with is the next character, so the mark takes a
		// space into its own cluster.
		"joining": New([]Binding{{Keys: []string{"x"}, Action: ActionOpen, Scope: ScopeGlobal, Help: "open؁"}}),
	}
	for name, r := range registries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, scope := range scopeOrder {
				for width := 1; width <= 200; width++ {
					got := r.Footer(scope, width)
					if w := textx.Width(got); w != width {
						t.Fatalf("Footer(%v, %d) is %d cells wide: %q", scope, width, w, got)
					}
				}
			}
		})
	}
}

func TestFooterIsEmptyWithoutCellsToFill(t *testing.T) {
	t.Parallel()

	r := Default()
	for _, width := range []int{0, -1, -200} {
		if got := r.Footer(ScopeTree, width); got != "" {
			t.Errorf("Footer(tree, %d) = %q, want the empty string", width, got)
		}
	}
}

func TestFooterHoldsEveryHintWhenTheLineIsLongEnough(t *testing.T) {
	t.Parallel()

	r := Default()
	for _, scope := range scopeOrder {
		hints := hintsFor(r, scope)
		want := strings.Join(hints, hintSeparator)
		got := strings.TrimRight(r.Footer(scope, textx.Width(want)+20), " ")
		if got != want {
			t.Errorf("Footer(%v, wide) = %q, want %q", scope, got, want)
		}
	}
}

func TestFooterDropsTheLowestPriorityHintFirst(t *testing.T) {
	t.Parallel()

	r := Default()
	hints := hintsFor(r, ScopeGlobal)
	if len(hints) < 3 {
		t.Fatalf("the global scope offers %d hints, too few to test priority", len(hints))
	}
	two := strings.Join(hints[:2], hintSeparator)
	width := textx.Width(two)

	if got := r.Footer(ScopeGlobal, width); got != two {
		t.Errorf("Footer(global, %d) = %q, want %q", width, got, two)
	}
	if got := strings.TrimRight(r.Footer(ScopeGlobal, width-1), " "); got != hints[0] {
		t.Errorf("Footer(global, %d) = %q, want the leading hint %q dropped to", width-1, got, hints[0])
	}
	if got := r.Footer(ScopeGlobal, width+1); !strings.HasPrefix(got, two) {
		t.Errorf("Footer(global, %d) = %q, want it to start with %q", width+1, got, two)
	}
}

func TestFooterClipsWhenNotEvenOneHintFits(t *testing.T) {
	t.Parallel()

	r := Default()
	lead := hintsFor(r, ScopeGlobal)[0]
	width := textx.Width(lead) - 1
	got := r.Footer(ScopeGlobal, width)
	if !strings.HasPrefix(got, lead[:1]) {
		t.Errorf("Footer(global, %d) = %q, want the leading hint clipped rather than dropped", width, got)
	}
	if got := r.Footer(ScopeGlobal, 1); got != textx.Ellipsis {
		t.Errorf("Footer(global, 1) = %q, want %q", got, textx.Ellipsis)
	}
}

func TestFooterJoinsTheSpellingsOfOneBinding(t *testing.T) {
	t.Parallel()

	got := Default().Footer(ScopeSearch, 120)
	want := "enter" + keySeparator + "ctrl+n jump to the next match"
	if !strings.HasPrefix(got, want) {
		t.Errorf("Footer(search, 120) = %q, want it to start with %q", got, want)
	}
	if !strings.Contains(got, hintSeparator+"esc discard the query") {
		t.Errorf("Footer(search, 120) = %q, want the query hint separated by %q", got, hintSeparator)
	}
}

func TestFooterAdvertisesOnlyLiveKeys(t *testing.T) {
	t.Parallel()

	r := Default()
	got := r.Footer(ScopeTree, 400)
	if !strings.Contains(got, "enter open the selected file") {
		t.Errorf("Footer(tree, 400) = %q, want the tree wording for enter", got)
	}
	if strings.Contains(got, "search the file") {
		t.Errorf("Footer(tree, 400) = %q, want a key the tree does not resolve left off it", got)
	}
}

func TestFooterNeverBreaksTheLine(t *testing.T) {
	t.Parallel()

	r := New([]Binding{
		{Keys: []string{"x\ny"}, Action: ActionOpen, Scope: ScopeGlobal, Help: "open\rthe\x1b[Jfile"},
	})
	got := r.Footer(ScopeGlobal, 40)
	if strings.ContainsAny(got, "\n\r\x1b") {
		t.Errorf("Footer = %q, want the control characters neutralized", got)
	}
}

func FuzzFooterWidth(f *testing.F) {
	f.Add("j", "move down", 1)
	f.Add("ctrl+n,ctrl+p", "jump to the next match", 40)
	f.Add("世界", "開く", 7)
	f.Add("\x1b[31m", "red\x00help", 200)
	f.Add("", "", 0)
	f.Add("x", strings.Repeat("long ", 40), 199)

	f.Fuzz(func(t *testing.T, spellings, help string, rawWidth int) {
		width := rawWidth % 400
		r := New([]Binding{
			{Keys: strings.Split(spellings, ","), Action: ActionOpen, Scope: ScopeGlobal, Help: help},
			{Keys: []string{"q"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit"},
		})
		got := r.Footer(ScopeGlobal, width)
		if width <= 0 {
			if got != "" {
				t.Fatalf("Footer(global, %d) = %q, want the empty string", width, got)
			}
			return
		}
		if w := textx.Width(got); w != width {
			t.Fatalf("Footer(global, %d) is %d cells wide: %q", width, w, got)
		}
		if strings.ContainsAny(got, "\n\r\x1b") {
			t.Fatalf("Footer(global, %d) = %q, want the control characters neutralized", width, got)
		}
	})
}
