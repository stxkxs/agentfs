package layout

import "strconv"

// Mode selects which panes the frame carries. [ModeBrowse] and [ModeRuns] share
// one arrangement and differ only in what the left pane lists, so switching
// between them moves no boundary; [ModeHelp] and [ModeBudgets] are overlay
// modes, each giving the whole content region to a single pane.
type Mode int

// The modes a frame is computed for.
const (
	// ModeBrowse: the left pane lists the workspace tree.
	ModeBrowse Mode = iota
	// ModeRuns: the left pane lists an agent's runs.
	ModeRuns
	// ModeHelp: the content region is one help pane.
	ModeHelp
	// ModeBudgets: the content region is one pane listing the response-time
	// budgets the session recorded.
	ModeBudgets
)

// String returns the mode's lowercase name. A value outside the vocabulary
// renders as its number rather than as a mode it is not.
func (m Mode) String() string {
	switch m {
	case ModeBrowse:
		return "browse"
	case ModeRuns:
		return "runs"
	case ModeHelp:
		return "help"
	case ModeBudgets:
		return "budgets"
	default:
		return "mode(" + strconv.Itoa(int(m)) + ")"
	}
}

// Modes returns every mode, in vocabulary order. A caller cycling modes and a
// test sweeping them read the enum from here rather than restating it.
func Modes() []Mode { return []Mode{ModeBrowse, ModeRuns, ModeHelp, ModeBudgets} }
