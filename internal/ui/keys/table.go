package keys

import "sync"

// defaultBindings is the shipped table.
//
// Order is priority order: the footer drops from the end when the line is too
// narrow, so the keys an operator reaches for most sit at the front. An alias
// spelling gets its own hidden binding rather than joining the leading one, so
// that the footer spends one key's worth of cells while the overlay still lists
// every way to ask for the action.
//
// A scope entry that repeats a global key overrides it. Such an entry is
// visible and carries help of its own, because that footer line is the only
// place an operator learns the key means something else here.
var defaultBindings = []Binding{
	{Keys: []string{"j"}, Action: ActionDown, Scope: ScopeGlobal, Help: "move down"},
	{Keys: []string{"down"}, Action: ActionDown, Scope: ScopeGlobal, Help: "move down", Hidden: true},
	{Keys: []string{"k"}, Action: ActionUp, Scope: ScopeGlobal, Help: "move up"},
	{Keys: []string{"up"}, Action: ActionUp, Scope: ScopeGlobal, Help: "move up", Hidden: true},
	{Keys: []string{"tab"}, Action: ActionNextPane, Scope: ScopeGlobal, Help: "focus the next pane"},
	{Keys: []string{"shift+tab"}, Action: ActionPrevPane, Scope: ScopeGlobal, Help: "focus the previous pane"},
	{Keys: []string{"?"}, Action: ActionToggleHelp, Scope: ScopeGlobal, Help: "toggle the key list"},
	{Keys: []string{"q"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit"},
	{Keys: []string{"ctrl+c"}, Action: ActionQuit, Scope: ScopeGlobal, Help: "quit", Hidden: true},
	{Keys: []string{"esc"}, Action: ActionCancel, Scope: ScopeGlobal, Help: "leave the mode or discard the search"},
	{Keys: []string{"ctrl+d"}, Action: ActionPageDown, Scope: ScopeGlobal, Help: "scroll down half a page"},
	{Keys: []string{"pgdown"}, Action: ActionPageDown, Scope: ScopeGlobal, Help: "scroll down half a page", Hidden: true},
	{Keys: []string{"ctrl+u"}, Action: ActionPageUp, Scope: ScopeGlobal, Help: "scroll up half a page"},
	{Keys: []string{"pgup"}, Action: ActionPageUp, Scope: ScopeGlobal, Help: "scroll up half a page", Hidden: true},
	{Keys: []string{"g"}, Action: ActionTop, Scope: ScopeGlobal, Help: "jump to the top"},
	{Keys: []string{"home"}, Action: ActionTop, Scope: ScopeGlobal, Help: "jump to the top", Hidden: true},
	{Keys: []string{"G"}, Action: ActionBottom, Scope: ScopeGlobal, Help: "jump to the bottom"},
	{Keys: []string{"end"}, Action: ActionBottom, Scope: ScopeGlobal, Help: "jump to the bottom", Hidden: true},
	{Keys: []string{"R"}, Action: ActionToggleRuns, Scope: ScopeGlobal, Help: "toggle the run history"},
	{Keys: []string{"r"}, Action: ActionReload, Scope: ScopeGlobal, Help: "reload the workspace"},
	{Keys: []string{"b"}, Action: ActionToggleBudgets, Scope: ScopeGlobal, Help: "toggle the response-time budgets"},

	{Keys: []string{"l"}, Action: ActionExpand, Scope: ScopeTree, Help: "expand the directory"},
	{Keys: []string{"right"}, Action: ActionExpand, Scope: ScopeTree, Help: "expand the directory", Hidden: true},
	{Keys: []string{"h"}, Action: ActionCollapse, Scope: ScopeTree, Help: "collapse the directory"},
	{Keys: []string{"left"}, Action: ActionCollapse, Scope: ScopeTree, Help: "collapse the directory", Hidden: true},
	{Keys: []string{"enter"}, Action: ActionOpen, Scope: ScopeTree, Help: "open the selected file"},

	{Keys: []string{"/"}, Action: ActionSearch, Scope: ScopePreview, Help: "search the file"},
	{Keys: []string{"n"}, Action: ActionNextMatch, Scope: ScopePreview, Help: "jump to the next match"},
	{Keys: []string{"N"}, Action: ActionPrevMatch, Scope: ScopePreview, Help: "jump to the previous match"},
	{Keys: []string{"h"}, Action: ActionPrevPane, Scope: ScopePreview, Help: "return to the tree"},
	{Keys: []string{"left"}, Action: ActionPrevPane, Scope: ScopePreview, Help: "return to the tree", Hidden: true},

	{Keys: []string{"enter"}, Action: ActionOpen, Scope: ScopeFeed, Help: "open the file the entry names"},
	{Keys: []string{"f"}, Action: ActionFollow, Scope: ScopeFeed, Help: "follow the newest entry"},

	{Keys: []string{"enter"}, Action: ActionOpen, Scope: ScopeRuns, Help: "open the selected run"},

	{Keys: []string{"enter", "ctrl+n"}, Action: ActionNextMatch, Scope: ScopeSearch, Help: "jump to the next match"},
	{Keys: []string{"ctrl+p"}, Action: ActionPrevMatch, Scope: ScopeSearch, Help: "jump to the previous match"},
	{Keys: []string{"esc"}, Action: ActionClearSearch, Scope: ScopeSearch, Help: "discard the query"},
}

// defaultRegistry builds the shipped table once. A registry is fixed once
// built, so every caller shares one.
var defaultRegistry = sync.OnceValue(func() *Registry { return New(defaultBindings) })

// Default returns the registry over the shipped table. Every call returns the
// same registry.
//
// The search prompt binds the match keys to their control-modified spellings.
// An unmodified letter there is a character the operator typed, so "n" belongs
// in the query rather than in the dispatcher.
func Default() *Registry { return defaultRegistry() }
