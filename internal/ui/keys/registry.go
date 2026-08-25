package keys

import (
	"slices"

	"github.com/stxkxs/agentfs/internal/textx"
)

// Binding maps keys to an action within a scope.
type Binding struct {
	// Keys are the spellings a terminal layer renders a press to, such as "j",
	// "shift+tab" or "ctrl+c". A press is matched byte for byte, so a spelling
	// the terminal layer never produces is a binding that never fires. The
	// first spelling leads, and the footer prints them in this order.
	//
	// A spelling is stored exactly as given, unlike Help: it is matched against
	// what the terminal reports, so a surface that draws one sanitizes it at the
	// point it is drawn.
	Keys []string
	// Action is what a press of any of Keys requests.
	Action Action
	// Scope is where the binding is live.
	Scope Scope
	// Help describes the action imperatively, lowercase and without a trailing
	// period, so one wording serves the footer, the overlay and the reference.
	Help string
	// Hidden keeps a binding live but out of the footer. An alias spelling
	// earns a line in the overlay without spending footer cells.
	Hidden bool
}

// clone returns a copy sharing no slice with b.
func (b Binding) clone() Binding {
	b.Keys = slices.Clone(b.Keys)
	return b
}

// Registry resolves a key within a scope. A registry is fixed once built, so
// one is safe to share across goroutines and safe to hold for the life of the
// program.
type Registry struct {
	bindings []Binding
	index    map[Scope]map[string]Action
}

// New returns a registry over bindings. The order given is the stable order of
// [Registry.Bindings] and the priority order of [Registry.Footer], so the keys
// an operator reaches for first belong at the front of the table.
//
// Within a scope the first binding to claim a key keeps it. A table that
// contradicts itself therefore resolves the same way on every run instead of
// following map order, and the contradiction stays visible in [Registry.Help]
// where it can be found.
//
// Help text is sanitized as the table is loaded, because it reaches the frame:
// a table assembled at run time cannot write a control sequence into the
// status line. Keys are matched against what the terminal reports and are
// stored exactly as given.
func New(bindings []Binding) *Registry {
	r := &Registry{
		bindings: make([]Binding, 0, len(bindings)),
		index:    make(map[Scope]map[string]Action),
	}
	for _, b := range bindings {
		b = b.clone()
		b.Help = textx.Sanitize(b.Help)
		r.bindings = append(r.bindings, b)
		scoped, ok := r.index[b.Scope]
		if !ok {
			scoped = make(map[string]Action)
			r.index[b.Scope] = scoped
		}
		for _, k := range b.Keys {
			if _, taken := scoped[k]; !taken {
				scoped[k] = b.Action
			}
		}
	}
	return r
}

// Resolve returns the action a press of key requests while scope holds focus,
// and whether any binding claims it.
//
// The scope's own bindings are consulted before the global ones, so a scope
// shadows a global key by naming it. A capturing scope has no global fallback:
// see [Scope.Captures].
func (r *Registry) Resolve(key string, scope Scope) (Action, bool) {
	if a, ok := r.index[scope][key]; ok {
		return a, true
	}
	if scope == ScopeGlobal || scope.Captures() {
		return ActionNone, false
	}
	if a, ok := r.index[ScopeGlobal][key]; ok {
		return a, true
	}
	return ActionNone, false
}

// Bindings returns every binding in table order, including the ones the footer
// hides. The result shares no memory with the registry, so a caller that
// sorts or rewrites it cannot change what a key does.
func (r *Registry) Bindings() []Binding {
	out := make([]Binding, len(r.bindings))
	for i, b := range r.bindings {
		out[i] = b.clone()
	}
	return out
}

// ForScope returns the visible bindings a press in s can reach: the scope's own
// in table order, then the global ones it falls back to, also in table order.
//
// A binding is spelled with the keys nothing ahead of it in that order has
// already claimed, and drops out once none are left. So a spelling the scope
// overrides, a spelling an earlier binding in the same scope took, and a
// binding with no spelling at all are all absent, and every key returned
// resolves to the action it is listed under: the footer cannot advertise a key
// that does something else where it is shown. A capturing scope has no
// fallback, so only its own bindings are returned.
func (r *Registry) ForScope(s Scope) []Binding {
	claimed := make(map[string]bool)
	out := r.advertised(s, claimed)
	if s == ScopeGlobal || s.Captures() {
		return out
	}
	return append(out, r.advertised(ScopeGlobal, claimed)...)
}

// advertised returns copies of the visible bindings declared in scope decl,
// each spelled with the keys claimed records as unclaimed. Every spelling
// walked is added to claimed, hidden bindings included, because a hidden
// spelling resolves to its own binding and to nothing later.
func (r *Registry) advertised(decl Scope, claimed map[string]bool) []Binding {
	out := make([]Binding, 0, len(r.bindings))
	for _, b := range r.bindings {
		if b.Scope != decl {
			continue
		}
		b = b.clone()
		live := b.Keys[:0]
		for _, k := range b.Keys {
			if claimed[k] {
				continue
			}
			claimed[k] = true
			live = append(live, k)
		}
		if b.Hidden || len(live) == 0 {
			continue
		}
		b.Keys = live
		out = append(out, b)
	}
	return out
}

// HelpSection is one scope's bindings, as the full-help overlay lists them.
type HelpSection struct {
	// Scope is the section's heading.
	Scope Scope
	// Bindings are the scope's own bindings in table order, hidden ones
	// included.
	Bindings []Binding
}

// Help returns every binding grouped by scope for the full-help overlay.
//
// Sections come in scope order, and a scope with no bindings of its own is
// left out rather than printed empty. Bindings the footer hides are listed
// here: the overlay is where an unadvertised key is discovered, and a key
// reachable but undocumented is the failure this package exists to prevent.
func (r *Registry) Help() []HelpSection {
	seen := make(map[Scope]bool, len(scopeOrder))
	order := make([]Scope, 0, len(scopeOrder))
	for _, s := range scopeOrder {
		seen[s] = true
		order = append(order, s)
	}
	for _, b := range r.bindings {
		if !seen[b.Scope] {
			seen[b.Scope] = true
			order = append(order, b.Scope)
		}
	}

	out := make([]HelpSection, 0, len(order))
	for _, s := range order {
		section := HelpSection{Scope: s}
		for _, b := range r.bindings {
			if b.Scope == s {
				section.Bindings = append(section.Bindings, b.clone())
			}
		}
		if len(section.Bindings) > 0 {
			out = append(out, section)
		}
	}
	return out
}
