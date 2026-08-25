// Package theme names the colours and glyphs agentfs draws with.
//
// The rest of agentfs asks for a semantic role: a pane asks for the directory
// role, never for blue. A colour literal repeated across the call sites that
// need it is not a definition — the meaning of "dim" is then held by the
// repetition of the string, and changing it means finding every copy. Naming a
// colour is this package's work alone, and TestNoOtherPackageNamesAColour holds
// the rest of the module to it.
//
// [Dark] is drawn for a near-black terminal background, #121212, and [Light]
// for a near-white one, #fafafa. Every foreground in both palettes clears the
// WCAG AA contrast ratio of 4.5:1 against whatever it lands on — the terminal's
// background, or the role's own fill where a role paints one. The muted roles
// are included: a dim row is still read, and a frame is still followed.
//
// Colour never carries meaning on its own. Roles that compete for the same
// column are drawn apart: no two agent statuses share a colour, nor two
// severities, JSON token classes or log levels, nor a search hit and the
// current one, nor a focused frame and a blurred one. Status, severity,
// recency and the four filesystem change kinds carry a distinct glyph on top of
// that, so [Plain] — which renders its input with no escape sequences at all,
// and is the palette golden files capture — loses the styling without losing
// the distinction. A change kind has no colour role at all: its glyph is the
// whole signal. [ASCIIGlyphs] draws the same within-column distinctions in
// printable ASCII, for a terminal or font that draws a replacement box in place
// of U+25CF.
//
// A Palette is a small handle onto an immutable table. Copying one is cheap
// enough to do per row, and a copy shares nothing a caller can reach, so a pane
// may hold its own palette with its own glyphs.
package theme

import (
	"charm.land/lipgloss/v2"
)

// Palette resolves semantic roles to styles.
//
// The zero Palette renders every role unstyled and carries no glyphs; [Plain]
// is the unstyled palette with glyphs.
type Palette struct {
	styles *[roleCount]lipgloss.Style
	glyphs *Glyphs
}

// Dark returns the palette for a dark terminal background.
func Dark() Palette { return darkPalette }

// Light returns the palette for a light terminal background.
func Light() Palette { return lightPalette }

// Plain returns a palette that renders no colour.
//
// Every role emits its input with no escape sequences, so a golden file records
// layout rather than styling. A string that has been through textx.Sanitize —
// one line, printable runes, no tabs — comes back byte for byte. A raw tab does
// not: lipgloss expands a tab to spaces whatever the style carries, which is
// why the render path sanitizes before it styles.
func Plain() Palette { return plainPalette }

// For returns the palette matching the terminal's background.
func For(isDark bool) Palette {
	if isDark {
		return darkPalette
	}
	return lightPalette
}

var (
	defaultGlyphs = UnicodeGlyphs()
	plainStyles   [roleCount]lipgloss.Style
	plainPalette  = Palette{styles: &plainStyles, glyphs: &defaultGlyphs}
	darkPalette   = build(true)
	lightPalette  = build(false)
)

// build resolves the appearance table against one terminal background.
func build(isDark bool) Palette {
	pick := lipgloss.LightDark(isDark)
	styles := new([roleCount]lipgloss.Style)
	for i, a := range &appearances {
		st := lipgloss.NewStyle()
		if a.fg.set() {
			st = st.Foreground(pick(lipgloss.Color(a.fg.light), lipgloss.Color(a.fg.dark)))
		}
		if a.bg.set() {
			st = st.Background(pick(lipgloss.Color(a.bg.light), lipgloss.Color(a.bg.dark)))
		}
		if a.bold {
			st = st.Bold(true)
		}
		if a.italic {
			st = st.Italic(true)
		}
		styles[i] = st
	}
	return Palette{styles: styles, glyphs: &defaultGlyphs}
}

// at returns the style for a table entry, unstyled when the palette is the zero
// value. Every accessor goes through it, so a Palette that was declared rather
// than constructed renders plainly instead of failing mid-frame.
func (p Palette) at(r role) lipgloss.Style {
	if p.styles == nil {
		return lipgloss.Style{}
	}
	return p.styles[r]
}

// Title returns the style for a pane heading.
func (p Palette) Title() lipgloss.Style { return p.at(roleTitle) }

// Dim returns the style for text that is present but secondary: counts,
// timestamps, the leading segments of a path that are not its base name.
func (p Palette) Dim() lipgloss.Style { return p.at(roleDim) }

// Body returns the style for ordinary text.
func (p Palette) Body() lipgloss.Style { return p.at(roleBody) }

// Accent returns the style for the one element in a view that should be looked
// at first.
func (p Palette) Accent() lipgloss.Style { return p.at(roleAccent) }

// Directory returns the style for a directory name in a tree.
func (p Palette) Directory() lipgloss.Style { return p.at(roleDirectory) }

// Cursor returns the style for the selected row.
func (p Palette) Cursor() lipgloss.Style { return p.at(roleCursor) }

// Recent returns the style for a row touched inside the recency window.
func (p Palette) Recent() lipgloss.Style { return p.at(roleRecent) }

// Border returns the frame colour for a pane, focused or not.
//
// The style carries a foreground and no shape: which sides a pane draws, and
// with which runes, is the layout's decision, and the layout renders those
// runes through this style. Composing lipgloss's own BorderStyle on top does
// not colour a frame — lipgloss reads a border's colour from BorderForeground,
// and this style's foreground would reach the content instead.
func (p Palette) Border(focused bool) lipgloss.Style {
	if focused {
		return p.at(roleBorderFocused)
	}
	return p.at(roleBorderBlurred)
}

// Status returns the style for an agent state. A role outside the vocabulary
// resolves to the unknown state.
func (p Palette) Status(r StatusRole) lipgloss.Style {
	if r < 0 || int(r) >= statusRoleCount {
		return p.at(roleStatusUnknown)
	}
	return p.at(roleStatusRunning + role(r))
}

// Severity returns the style for a diagnostic weight. A role outside the
// vocabulary resolves to info, the level that claims the least.
func (p Palette) Severity(r SeverityRole) lipgloss.Style {
	if r < 0 || int(r) >= severityRoleCount {
		return p.at(roleSeverityInfo)
	}
	return p.at(roleSeverityInfo + role(r))
}

// Match returns the style for a search hit. The current hit is distinguished
// from the rest, because a search that highlights every hit alike has not
// answered where the cursor lands next.
func (p Palette) Match(current bool) lipgloss.Style {
	if current {
		return p.at(roleMatchCurrent)
	}
	return p.at(roleMatch)
}

// JSON returns the style for a token class in a rendered document. A role
// outside the vocabulary resolves to punctuation, which claims no type.
func (p Palette) JSON(r JSONRole) lipgloss.Style {
	if r < 0 || int(r) >= jsonRoleCount {
		return p.at(roleJSONPunct)
	}
	return p.at(roleJSONKey + role(r))
}

// LogLevel returns the style for a log line's level. A role outside the
// vocabulary resolves to the info level.
func (p Palette) LogLevel(r LogRole) lipgloss.Style {
	if r < 0 || int(r) >= logRoleCount {
		return p.at(roleLogInfo)
	}
	return p.at(roleLogTrace + role(r))
}

// Glyphs returns the marks this palette draws with.
func (p Palette) Glyphs() Glyphs {
	if p.glyphs == nil {
		return Glyphs{}
	}
	return *p.glyphs
}

// WithGlyphs returns a copy of the palette drawing with g, for a terminal whose
// font renders the default marks as replacement boxes. The glyph set is taken by
// value so that a caller cannot reach into a palette it has already handed out.
func (p Palette) WithGlyphs(g Glyphs) Palette {
	p.glyphs = &g
	return p
}

// StatusGlyph returns the mark for an agent state, which is how the state
// survives a palette that renders no colour. A role outside the vocabulary
// resolves to the unknown mark.
func (p Palette) StatusGlyph(r StatusRole) string {
	g := p.Glyphs()
	switch r {
	case RoleRunning:
		return g.Running
	case RoleIdle:
		return g.Idle
	case RoleBlocked:
		return g.Blocked
	case RoleError:
		return g.Error
	case RoleDone:
		return g.Done
	case RoleUnknown:
		return g.Unknown
	default:
		return g.Unknown
	}
}

// SeverityGlyph returns the mark for a diagnostic weight. A role outside the
// vocabulary resolves to the info mark.
func (p Palette) SeverityGlyph(r SeverityRole) string {
	g := p.Glyphs()
	switch r {
	case RoleInfo:
		return g.Info
	case RoleWarning:
		return g.Warning
	case RoleSevere:
		return g.Severe
	default:
		return g.Info
	}
}
