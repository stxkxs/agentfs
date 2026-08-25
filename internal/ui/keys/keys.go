// Package keys is the binding table an agentfs key press is resolved against.
//
// One table drives four surfaces: dispatch, the context-sensitive footer, the
// full-help overlay and the generated key reference. There is no second place
// to declare a key, so a key absent from the table does nothing, appears in no
// footer and appears in no help text — which is what keeps the four surfaces
// from disagreeing about what a key does.
//
// Nothing here knows about a terminal framework. A press arrives as the string
// the terminal layer renders it to and leaves as an [Action] the application
// routes, so the table is exercised without a terminal and without a program
// loop.
package keys

// unknownName spells a vocabulary value that has no canonical name.
const unknownName = "unknown"

// Action is what a key press requests. It is framework-independent: the
// terminal layer translates a key message into an Action and routes it, so a
// change to the table never reaches into event handling.
type Action int

// The vocabulary of requests a binding can make.
const (
	// ActionNone is the zero action. It is what [Registry.Resolve] returns for
	// a key no binding claims, so a caller that drops the second result routes
	// nothing rather than routing the first action in the vocabulary.
	ActionNone Action = iota
	// ActionUp moves the selection one row toward the top.
	ActionUp
	// ActionDown moves the selection one row toward the bottom.
	ActionDown
	// ActionPageUp moves the selection half a viewport toward the top.
	ActionPageUp
	// ActionPageDown moves the selection half a viewport toward the bottom.
	ActionPageDown
	// ActionTop moves the selection to the first row.
	ActionTop
	// ActionBottom moves the selection to the last row.
	ActionBottom
	// ActionExpand reveals the children of the selected node.
	ActionExpand
	// ActionCollapse hides the children of the selected node.
	ActionCollapse
	// ActionOpen acts on the selection: the pane decides what opening means.
	ActionOpen
	// ActionNextPane moves focus to the pane after the focused one.
	ActionNextPane
	// ActionPrevPane moves focus to the pane before the focused one.
	ActionPrevPane
	// ActionSearch opens the search prompt over the file preview.
	ActionSearch
	// ActionNextMatch moves the selection to the match after the selection.
	ActionNextMatch
	// ActionPrevMatch moves the selection to the match before the selection.
	ActionPrevMatch
	// ActionClearSearch discards the query and the highlight it drives.
	ActionClearSearch
	// ActionToggleRuns shows or hides the run history.
	ActionToggleRuns
	// ActionReload rereads the workspace from disk.
	ActionReload
	// ActionToggleHelp shows or hides the full-help overlay.
	ActionToggleHelp
	// ActionToggleBudgets shows or hides the response-time budget overlay.
	ActionToggleBudgets
	// ActionFollow pins the viewport to the newest entry of a list that grows
	// from the top.
	ActionFollow
	// ActionCancel leaves the active mode or overlay, and discards the search
	// when neither is open.
	ActionCancel
	// ActionQuit ends the program.
	ActionQuit
)

// actionNames is the canonical spelling of each action, indexed by the action.
var actionNames = [...]string{
	ActionNone:          "none",
	ActionUp:            "up",
	ActionDown:          "down",
	ActionPageUp:        "page-up",
	ActionPageDown:      "page-down",
	ActionTop:           "top",
	ActionBottom:        "bottom",
	ActionExpand:        "expand",
	ActionCollapse:      "collapse",
	ActionOpen:          "open",
	ActionNextPane:      "next-pane",
	ActionPrevPane:      "prev-pane",
	ActionSearch:        "search",
	ActionNextMatch:     "next-match",
	ActionPrevMatch:     "prev-match",
	ActionClearSearch:   "clear-search",
	ActionToggleRuns:    "toggle-runs",
	ActionReload:        "reload",
	ActionToggleHelp:    "toggle-help",
	ActionToggleBudgets: "toggle-budgets",
	ActionFollow:        "follow",
	ActionCancel:        "cancel",
	ActionQuit:          "quit",
}

// String returns the action's canonical spelling. A value outside the
// vocabulary spells "unknown" rather than borrowing the zero action's name, so
// a value that came from nowhere reads differently from [ActionNone], which is
// what a key no binding claims resolves to.
func (a Action) String() string {
	if a < 0 || int(a) >= len(actionNames) {
		return unknownName
	}
	return actionNames[a]
}

// Scope is where a binding is live. A press is resolved against the focused
// scope first and the global scope second, so a scope can give a key a meaning
// of its own without the global table knowing about it.
type Scope int

// The scopes a binding can belong to.
const (
	// ScopeGlobal is the fallback every non-capturing scope resolves against.
	ScopeGlobal Scope = iota
	// ScopeTree is the workspace tree.
	ScopeTree
	// ScopePreview is the file preview.
	ScopePreview
	// ScopeFeed is the activity feed.
	ScopeFeed
	// ScopeRuns is the run history.
	ScopeRuns
	// ScopeSearch is the search prompt, which captures the keys it does not
	// bind.
	ScopeSearch
)

// scopeNames is the canonical spelling of each scope, indexed by the scope.
var scopeNames = [...]string{
	ScopeGlobal:  "global",
	ScopeTree:    "tree",
	ScopePreview: "preview",
	ScopeFeed:    "feed",
	ScopeRuns:    "runs",
	ScopeSearch:  "search",
}

// scopeOrder is the order the scopes are listed in, for [Registry.Help] and
// for the generated reference.
var scopeOrder = [...]Scope{
	ScopeGlobal,
	ScopeTree,
	ScopePreview,
	ScopeFeed,
	ScopeRuns,
	ScopeSearch,
}

// String returns the scope's canonical spelling, or "unknown" for a value
// outside the vocabulary.
func (s Scope) String() string {
	if s < 0 || int(s) >= len(scopeNames) {
		return unknownName
	}
	return scopeNames[s]
}

// Captures reports whether the scope consumes an unbound key as text rather
// than as a command.
//
// A capturing scope has no global fallback. The operator typing a query is
// typing characters, so a key the scope does not bind is a letter, not a
// command — resolving it globally would make "q" unwritable and quit the
// program instead.
func (s Scope) Captures() bool { return s == ScopeSearch }
