package theme

import (
	"reflect"
	"testing"

	"github.com/stxkxs/agentfs/internal/textx"
)

// namedGlyphs holds a glyph set by pointer, so a loop over the sets does not
// copy every mark in one to say which set it is looking at.
type namedGlyphs struct {
	name   string
	glyphs *Glyphs
}

func glyphSets() []namedGlyphs {
	unicode, ascii := UnicodeGlyphs(), ASCIIGlyphs()
	return []namedGlyphs{
		{"unicode", &unicode},
		{"ascii", &ascii},
	}
}

// axes groups the glyphs that compete for the same column. Two glyphs on
// different axes never appear in the same place, so only within-axis
// distinctness carries meaning.
func axes(g *Glyphs) map[string]map[string]string {
	return map[string]map[string]string{
		"status": {
			"running": g.Running, "idle": g.Idle, "blocked": g.Blocked,
			"error": g.Error, "done": g.Done, "unknown": g.Unknown,
		},
		"severity": {"info": g.Info, "warning": g.Warning, "severe": g.Severe},
		"tree":     {"collapsed": g.Collapsed, "expanded": g.Expanded, "leaf": g.Leaf},
		"mark": {
			"cursor": g.Cursor, "recent": g.Recent,
			"truncated": g.Truncated, "stale": g.Stale,
		},
		"change": {
			"created": g.Created, "modified": g.Modified,
			"removed": g.Removed, "renamed": g.Renamed,
		},
	}
}

func TestEveryGlyphIsPopulated(t *testing.T) {
	for _, set := range glyphSets() {
		v := reflect.ValueOf(*set.glyphs)
		for _, field := range reflect.VisibleFields(v.Type()) {
			if v.FieldByIndex(field.Index).String() == "" {
				t.Errorf("%s: %s is empty", set.name, field.Name)
			}
		}
	}
}

func TestEveryGlyphOccupiesOneCell(t *testing.T) {
	for _, set := range glyphSets() {
		v := reflect.ValueOf(*set.glyphs)
		for _, field := range reflect.VisibleFields(v.Type()) {
			g := v.FieldByIndex(field.Index).String()
			if w := textx.Width(g); w != 1 {
				t.Errorf("%s: %s = %q measures %d cells, want 1", set.name, field.Name, g, w)
			}
		}
	}
}

func TestEveryGlyphSurvivesSanitising(t *testing.T) {
	for _, set := range glyphSets() {
		v := reflect.ValueOf(*set.glyphs)
		for _, field := range reflect.VisibleFields(v.Type()) {
			g := v.FieldByIndex(field.Index).String()
			if got := textx.Sanitize(g); got != g {
				t.Errorf("%s: %s = %q sanitises to %q", set.name, field.Name, g, got)
			}
		}
	}
}

func TestGlyphsAreDistinctWithinTheirAxis(t *testing.T) {
	for _, set := range glyphSets() {
		for axis, members := range axes(set.glyphs) {
			seen := make(map[string]string, len(members))
			for name, g := range members {
				if prev, dup := seen[g]; dup {
					t.Errorf("%s/%s: %q and %q both draw %q", set.name, axis, prev, name, g)
				}
				seen[g] = name
			}
		}
	}
}

func TestASCIIGlyphsStayInPrintableASCII(t *testing.T) {
	v := reflect.ValueOf(ASCIIGlyphs())
	for _, field := range reflect.VisibleFields(v.Type()) {
		g := v.FieldByIndex(field.Index).String()
		for _, r := range g {
			if r < 0x20 || r > 0x7e {
				t.Errorf("%s = %q carries U+%04X, outside printable ASCII", field.Name, g, r)
			}
		}
	}
}

// TestPlainDistinguishesStatusAndChangeByGlyph is the accessibility floor: with
// no colour rendered, every state a row can report still arrives as its own
// mark.
func TestPlainDistinguishesStatusAndChangeByGlyph(t *testing.T) {
	p := Plain().WithGlyphs(UnicodeGlyphs())
	g := p.Glyphs()

	statuses := map[StatusRole]string{}
	for i := range statusRoleCount {
		r := StatusRole(i)
		statuses[r] = p.Status(r).Render(p.StatusGlyph(r))
	}
	seen := make(map[string]StatusRole, len(statuses))
	for r, drawn := range statuses {
		if drawn == "" {
			t.Errorf("status %s draws nothing", r)
		}
		if prev, dup := seen[drawn]; dup {
			t.Errorf("status %s and %s both draw %q", prev, r, drawn)
		}
		seen[drawn] = r
	}

	changes := map[string]string{
		"created":  p.Body().Render(g.Created),
		"modified": p.Body().Render(g.Modified),
		"removed":  p.Body().Render(g.Removed),
		"renamed":  p.Body().Render(g.Renamed),
	}
	drawn := make(map[string]string, len(changes))
	for name, mark := range changes {
		if prev, dup := drawn[mark]; dup {
			t.Errorf("change %s and %s both draw %q", prev, name, mark)
		}
		drawn[mark] = name
	}
}

func TestPaletteResolvesGlyphRoles(t *testing.T) {
	p := Dark()
	g := p.Glyphs()
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"running", p.StatusGlyph(RoleRunning), g.Running},
		{"idle", p.StatusGlyph(RoleIdle), g.Idle},
		{"blocked", p.StatusGlyph(RoleBlocked), g.Blocked},
		{"error", p.StatusGlyph(RoleError), g.Error},
		{"done", p.StatusGlyph(RoleDone), g.Done},
		{"unknown", p.StatusGlyph(RoleUnknown), g.Unknown},
		{"status below", p.StatusGlyph(-1), g.Unknown},
		{"status above", p.StatusGlyph(StatusRole(statusRoleCount)), g.Unknown},
		{"info", p.SeverityGlyph(RoleInfo), g.Info},
		{"warning", p.SeverityGlyph(RoleWarning), g.Warning},
		{"severe", p.SeverityGlyph(RoleSevere), g.Severe},
		{"severity below", p.SeverityGlyph(-1), g.Info},
		{"severity above", p.SeverityGlyph(SeverityRole(severityRoleCount)), g.Info},
	} {
		if tc.got != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	var zero Palette
	if got := zero.StatusGlyph(RoleRunning); got != "" {
		t.Errorf("zero Palette drew %q for a running agent", got)
	}
	if got := zero.SeverityGlyph(RoleSevere); got != "" {
		t.Errorf("zero Palette drew %q for a severe finding", got)
	}
}

// TestAxesCoverEveryGlyph keeps [axes] honest: a mark added to [Glyphs] and not
// placed on an axis would never be checked for distinctness.
func TestAxesCoverEveryGlyph(t *testing.T) {
	const offAxis = 1 // VerticalBar draws a separator, not a state.
	placed := 0
	unicode := UnicodeGlyphs()
	for _, members := range axes(&unicode) {
		placed += len(members)
	}
	if want := reflect.TypeFor[Glyphs]().NumField() - offAxis; placed != want {
		t.Errorf("axes place %d glyphs, Glyphs declares %d states", placed, want)
	}
}
