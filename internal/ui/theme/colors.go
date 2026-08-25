package theme

// The reference terminal backgrounds the table is measured against.
const (
	darkBackground  = "#121212"
	lightBackground = "#fafafa"
)

// minContrast is the WCAG 2.1 AA contrast ratio for body text, and the floor
// every entry in the table holds to. Muting a role below it trades legibility
// for hierarchy, and hierarchy has glyphs, weight and position to work with.
const minContrast = 4.5

// tone is one colour expressed for each terminal background. Holding both
// spellings in one value is what makes a role impossible to define for a dark
// terminal and forget for a light one.
type tone struct {
	light string
	dark  string
}

// set reports whether the tone names a colour on both backgrounds.
func (t tone) set() bool { return t.light != "" && t.dark != "" }

// appearance is one role's complete drawing instruction. An empty bg leaves the
// terminal's own background showing through.
type appearance struct {
	fg     tone
	bg     tone
	bold   bool
	italic bool
}

// appearances maps every role to its colour. A role is given an appearance by
// adding a row, which is the only way a colour enters agentfs.
var appearances = [roleCount]appearance{
	roleTitle:     {fg: tone{light: "#0d0d0d", dark: "#f2f2f2"}, bold: true},
	roleDim:       {fg: tone{light: "#6a6a6a", dark: "#9a9a9a"}},
	roleBody:      {fg: tone{light: "#1f1f1f", dark: "#dadada"}},
	roleAccent:    {fg: tone{light: "#0b5fce", dark: "#8ab4f8"}},
	roleDirectory: {fg: tone{light: "#0a4fa3", dark: "#82aaff"}, bold: true},

	// The cursor and the search matches paint a background, so their
	// foregrounds are read against that fill rather than against the terminal.
	roleCursor:        {fg: tone{light: "#fafafa", dark: "#101010"}, bg: tone{light: "#0b5fce", dark: "#8ab4f8"}, bold: true},
	roleMatch:         {fg: tone{light: "#1f1f1f", dark: "#101010"}, bg: tone{light: "#ffe066", dark: "#ffd479"}},
	roleMatchCurrent:  {fg: tone{light: "#1f1f1f", dark: "#101010"}, bg: tone{light: "#ffab4d", dark: "#ffb86c"}, bold: true},
	roleRecent:        {fg: tone{light: "#8a4b00", dark: "#ffb86c"}},
	roleBorderBlurred: {fg: tone{light: "#6a6a6a", dark: "#8a8a8a"}},
	roleBorderFocused: {fg: tone{light: "#0b5fce", dark: "#8ab4f8"}},

	roleStatusRunning: {fg: tone{light: "#106b2e", dark: "#7ee787"}},
	roleStatusIdle:    {fg: tone{light: "#5a6472", dark: "#a0a8b7"}},
	roleStatusBlocked: {fg: tone{light: "#7a5200", dark: "#ffd479"}},
	roleStatusError:   {fg: tone{light: "#b42318", dark: "#ff8a90"}, bold: true},
	roleStatusDone:    {fg: tone{light: "#0b5fce", dark: "#79c0ff"}},
	roleStatusUnknown: {fg: tone{light: "#6a6a6a", dark: "#9a9a9a"}},

	roleSeverityInfo:    {fg: tone{light: "#0b5fce", dark: "#79c0ff"}},
	roleSeverityWarning: {fg: tone{light: "#7a5200", dark: "#ffd479"}},
	roleSeveritySevere:  {fg: tone{light: "#b42318", dark: "#ff8a90"}, bold: true},

	roleJSONKey:    {fg: tone{light: "#0a4fa3", dark: "#8ab4f8"}},
	roleJSONString: {fg: tone{light: "#106b2e", dark: "#7ee787"}},
	roleJSONNumber: {fg: tone{light: "#8a4b00", dark: "#ffb86c"}},
	roleJSONBool:   {fg: tone{light: "#6b21a8", dark: "#d2a8ff"}},
	roleJSONNull:   {fg: tone{light: "#5a6472", dark: "#a0a8b7"}, italic: true},
	roleJSONPunct:  {fg: tone{light: "#6a6a6a", dark: "#9a9a9a"}},

	roleLogTrace: {fg: tone{light: "#6a6a6a", dark: "#8a8a8a"}},
	roleLogDebug: {fg: tone{light: "#5a6472", dark: "#a0a8b7"}},
	roleLogInfo:  {fg: tone{light: "#1f1f1f", dark: "#dadada"}},
	roleLogWarn:  {fg: tone{light: "#7a5200", dark: "#ffd479"}},
	roleLogError: {fg: tone{light: "#b42318", dark: "#ff8a90"}, bold: true},
}
