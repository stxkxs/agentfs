package pane

import (
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/textx"
	"github.com/stxkxs/agentfs/internal/ui/layout"
	"github.com/stxkxs/agentfs/internal/ui/render"
	"github.com/stxkxs/agentfs/internal/ui/theme"
	"github.com/stxkxs/agentfs/internal/workspace"
)

// AgentBar renders one line summarizing every detected agent.
//
// Presence and status are separate: "the agent declares itself idle" and "the
// agent declared nothing" are different facts, and a bar that renders both as
// unknown tells an operator nothing about which they are looking at.
type AgentBar struct{}

// View renders the bar into r.
func (AgentBar) View(agents []workspace.Agent, r layout.Rect, p theme.Palette) []string {
	if len(agents) == 0 {
		return render.Rows([]string{textx.Fit(p.Dim().Render("  no agents detected"), r.W)}, r)
	}

	sep := p.Dim().Render("  " + p.Glyphs().VerticalBar + "  ")
	sepWidth := textx.Width(sep)

	var b strings.Builder
	b.WriteString("  ")
	used := 2
	shown := 0

	for _, a := range agents {
		cell := agentCell(a, p)
		need := textx.Width(cell)
		if shown > 0 {
			need += sepWidth
		}
		// Reserve room for the overflow note, so the bar never claims to show
		// every agent when it dropped some.
		remaining := len(agents) - shown
		reserve := 0
		if remaining > 1 {
			reserve = textx.Width(overflowNote(remaining-1, p))
		}
		if used+need+reserve > r.W {
			break
		}
		if shown > 0 {
			b.WriteString(sep)
		}
		b.WriteString(cell)
		used += need
		shown++
	}

	if shown < len(agents) {
		b.WriteString(overflowNote(len(agents)-shown, p))
	}
	return render.Rows([]string{textx.Fit(b.String(), r.W)}, r)
}

func overflowNote(n int, p theme.Palette) string {
	return p.Dim().Render("  +" + strconv.Itoa(n) + " more")
}

// agentCell renders one agent: name, a glyph and word for its state, and the
// task it named.
func agentCell(a workspace.Agent, p theme.Palette) string {
	g := p.Glyphs()
	role := statusRole(a.Status())

	var marker, word string
	switch a.Presence {
	case workspace.PresenceAbsent:
		marker, word = g.Unknown, "no state"
	case workspace.PresenceUnreadable:
		marker, word = severityGlyph(g, theme.RoleSevere), "unreadable"
	case workspace.PresenceInvalid:
		marker, word = severityGlyph(g, theme.RoleSevere), "invalid state"
	case workspace.PresenceSettling:
		marker, word = statusGlyph(g, role), a.Status().String()+" "+g.Truncated
	case workspace.PresenceStale:
		marker, word = statusGlyph(g, role), a.Status().String()+" "+g.Stale
	case workspace.PresenceDeclared:
		marker, word = statusGlyph(g, role), a.Status().String()
	default:
		marker, word = g.Unknown, "unknown"
	}

	style := p.Status(role)
	if !a.Presence.Trustworthy() && a.Presence != workspace.PresenceStale {
		style = p.Severity(theme.RoleWarning)
	}

	var b strings.Builder
	b.WriteString(p.Title().Render(textx.Sanitize(a.Name)))
	b.WriteByte(' ')
	b.WriteString(style.Render(marker + " " + word))
	if detail := agentDetail(a); detail != "" {
		b.WriteByte(' ')
		b.WriteString(p.Dim().Render("(" + detail + ")"))
	}
	return b.String()
}

// agentDetail names the task and position an agent declared.
func agentDetail(a workspace.Agent) string {
	var parts []string
	if a.State.Task != "" {
		parts = append(parts, textx.Sanitize(a.State.Task))
	}
	if s := a.State.Step.String(); s != "" {
		step := "step " + textx.Sanitize(s)
		if a.State.StepsTotal > 0 {
			step += "/" + strconv.Itoa(a.State.StepsTotal)
		}
		parts = append(parts, step)
	}
	if a.State.Problem != "" {
		parts = append(parts, textx.Sanitize(a.State.Problem))
	}
	return strings.Join(parts, " ")
}

// statusRole maps a declared status onto the palette's status role.
func statusRole(s agentstate.Status) theme.StatusRole {
	switch s {
	case agentstate.StatusRunning:
		return theme.RoleRunning
	case agentstate.StatusIdle:
		return theme.RoleIdle
	case agentstate.StatusBlocked:
		return theme.RoleBlocked
	case agentstate.StatusError:
		return theme.RoleError
	case agentstate.StatusDone:
		return theme.RoleDone
	case agentstate.StatusUnknown:
		return theme.RoleUnknown
	default:
		return theme.RoleUnknown
	}
}
