package pane

import "github.com/stxkxs/agentfs/internal/ui/theme"

// statusGlyph returns the marker for a status role.
//
// Every distinction the palette draws in colour is drawn again here, because
// the plain palette draws no colour and it is the palette the golden frames
// render under: a distinction carried by colour alone is invisible to the tests
// that exist to protect it, and to a monochrome terminal.
func statusGlyph(g theme.Glyphs, r theme.StatusRole) string {
	switch r {
	case theme.RoleRunning:
		return g.Running
	case theme.RoleIdle:
		return g.Idle
	case theme.RoleBlocked:
		return g.Blocked
	case theme.RoleError:
		return g.Error
	case theme.RoleDone:
		return g.Done
	case theme.RoleUnknown:
		return g.Unknown
	default:
		return g.Unknown
	}
}

// severityGlyph returns the marker for a severity role.
func severityGlyph(g theme.Glyphs, r theme.SeverityRole) string {
	switch r {
	case theme.RoleInfo:
		return g.Info
	case theme.RoleWarning:
		return g.Warning
	case theme.RoleSevere:
		return g.Severe
	default:
		return g.Info
	}
}
