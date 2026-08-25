package main

import (
	"fmt"
	"strings"

	"github.com/stxkxs/agentfs/internal/ui/keys"
)

// scopeNote names a key scope and states where it is live.
type scopeNote struct {
	scope keys.Scope
	// heading names the scope for a reader.
	heading string
	// where states when the scope's bindings resolve.
	where string
}

var scopeNotes = []scopeNote{
	{keys.ScopeGlobal, "Everywhere", "Live in every pane and every mode, unless the focused pane binds the same key."},
	{keys.ScopeTree, "Files", "Live while the workspace tree holds focus."},
	{keys.ScopePreview, "Preview", "Live while the file preview holds focus."},
	{keys.ScopeFeed, "Activity", "Live while the activity feed holds focus."},
	{keys.ScopeRuns, "Runs", "Live while the run history holds focus, which replaces the tree in run mode."},
	{keys.ScopeSearch, "Search prompt", "Live while the search prompt is open. Every other key is text, including keys that are bound elsewhere."},
}

// renderKeys writes the binding reference.
func renderKeys(b *strings.Builder, _ module) error {
	title(b, "Keys",
		"One table resolves a key press, fills the footer, fills the `?` overlay, and renders this "+
			"page. A key absent from it does nothing and appears nowhere, which is what keeps the four "+
			"surfaces from disagreeing about what a key does.")

	para(b, "A pane's own bindings are checked before the global ones, so a pane can shadow a global key. "+
		"Where it does, the pane's row says what the key does there.")

	registry := keys.Default()
	seen := map[keys.Scope]bool{}

	for _, note := range scopeNotes {
		bindings := scopeBindings(registry, note.scope)
		if len(bindings) == 0 {
			return fmt.Errorf("the scope %q binds nothing, so this page documents an empty pane", note.heading)
		}
		seen[note.scope] = true

		section(b, note.heading)
		para(b, note.where)

		t := newTable("Keys", "Action")
		for _, binding := range bindings {
			t.row(codeList(binding.Keys), binding.Help)
		}
		if err := t.write(b); err != nil {
			return err
		}
	}

	for _, binding := range registry.Bindings() {
		if !seen[binding.Scope] {
			return fmt.Errorf("the scope %v holds bindings and this page describes it nowhere", binding.Scope)
		}
	}
	return nil
}

// scopeBindings returns a scope's own visible bindings, in table order. A
// binding marked hidden is live and deliberately absent from the surfaces a
// reader browses, so it is absent here too.
func scopeBindings(r *keys.Registry, scope keys.Scope) []keys.Binding {
	var out []keys.Binding
	for _, binding := range r.Bindings() {
		if binding.Scope == scope && !binding.Hidden {
			out = append(out, binding)
		}
	}
	return out
}
