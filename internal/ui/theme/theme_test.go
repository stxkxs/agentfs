package theme

import (
	"image/color"
	"reflect"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/textx"
)

// namedRole pairs a public accessor with the table entry it must resolve to.
// Every role the palette exposes appears here, so a role added without a colour,
// or wired to the wrong table entry, fails the suite rather than renders.
type namedRole struct {
	name  string
	entry role
	style func(Palette) lipgloss.Style
}

func allRoles() []namedRole {
	roles := []namedRole{
		{"title", roleTitle, Palette.Title},
		{"dim", roleDim, Palette.Dim},
		{"body", roleBody, Palette.Body},
		{"accent", roleAccent, Palette.Accent},
		{"directory", roleDirectory, Palette.Directory},
		{"cursor", roleCursor, Palette.Cursor},
		{"recent", roleRecent, Palette.Recent},
		{"border.focused", roleBorderFocused, func(p Palette) lipgloss.Style { return p.Border(true) }},
		{"border.blurred", roleBorderBlurred, func(p Palette) lipgloss.Style { return p.Border(false) }},
		{"match.current", roleMatchCurrent, func(p Palette) lipgloss.Style { return p.Match(true) }},
		{"match.other", roleMatch, func(p Palette) lipgloss.Style { return p.Match(false) }},
	}
	for i := range statusRoleCount {
		r := StatusRole(i)
		roles = append(roles, namedRole{
			"status." + r.String(), roleStatusRunning + role(i),
			func(p Palette) lipgloss.Style { return p.Status(r) },
		})
	}
	for i := range severityRoleCount {
		r := SeverityRole(i)
		roles = append(roles, namedRole{
			"severity." + r.String(), roleSeverityInfo + role(i),
			func(p Palette) lipgloss.Style { return p.Severity(r) },
		})
	}
	for i := range jsonRoleCount {
		r := JSONRole(i)
		roles = append(roles, namedRole{
			"json." + r.String(), roleJSONKey + role(i),
			func(p Palette) lipgloss.Style { return p.JSON(r) },
		})
	}
	for i := range logRoleCount {
		r := LogRole(i)
		roles = append(roles, namedRole{
			"log." + r.String(), roleLogTrace + role(i),
			func(p Palette) lipgloss.Style { return p.LogLevel(r) },
		})
	}
	return roles
}

func TestAllRolesCoverTheTable(t *testing.T) {
	seen := make(map[role]string, roleCount)
	for _, r := range allRoles() {
		if prev, dup := seen[r.entry]; dup {
			t.Errorf("roles %q and %q both claim table entry %d", prev, r.name, r.entry)
		}
		seen[r.entry] = r.name
	}
	if len(seen) != int(roleCount) {
		t.Errorf("public roles cover %d table entries, want %d", len(seen), roleCount)
	}
}

func TestAccessorsResolveToTheirTableEntry(t *testing.T) {
	for _, p := range []struct {
		name    string
		palette Palette
		isDark  bool
	}{
		{"dark", Dark(), true},
		{"light", Light(), false},
	} {
		for _, r := range allRoles() {
			t.Run(p.name+"/"+r.name, func(t *testing.T) {
				a := appearances[r.entry]
				got := r.style(p.palette)
				if want := pickHex(a.fg, p.isDark); !sameColor(got.GetForeground(), lipgloss.Color(want)) {
					t.Errorf("foreground = %v, want %s", got.GetForeground(), want)
				}
				if a.bg.set() {
					if want := pickHex(a.bg, p.isDark); !sameColor(got.GetBackground(), lipgloss.Color(want)) {
						t.Errorf("background = %v, want %s", got.GetBackground(), want)
					}
				}
				if got.GetBold() != a.bold {
					t.Errorf("bold = %v, want %v", got.GetBold(), a.bold)
				}
				if got.GetItalic() != a.italic {
					t.Errorf("italic = %v, want %v", got.GetItalic(), a.italic)
				}
			})
		}
	}
}

func TestColouredPalettesAreComplete(t *testing.T) {
	var zero lipgloss.Style
	for _, p := range []struct {
		name    string
		palette Palette
	}{
		{"dark", Dark()},
		{"light", Light()},
	} {
		for _, r := range allRoles() {
			got := r.style(p.palette)
			if reflect.DeepEqual(got, zero) {
				t.Errorf("%s/%s is the zero Style", p.name, r.name)
			}
			if got.Render("x") == "x" {
				t.Errorf("%s/%s renders unstyled", p.name, r.name)
			}
		}
	}
}

func TestDarkAndLightDiffer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		style func(Palette) lipgloss.Style
	}{
		{"body", Palette.Body},
		{"dim", Palette.Dim},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dark, light := tc.style(Dark()), tc.style(Light())
			if sameColor(dark.GetForeground(), light.GetForeground()) {
				t.Errorf("dark and light agree on %v", dark.GetForeground())
			}
		})
	}
}

func TestForSelectsByBackground(t *testing.T) {
	for _, tc := range []struct {
		isDark bool
		want   Palette
	}{
		{true, Dark()},
		{false, Light()},
	} {
		if got := For(tc.isDark); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("For(%v) chose the other palette", tc.isDark)
		}
	}
}

func TestPlainRendersInputUnchanged(t *testing.T) {
	inputs := []string{
		"",
		"x",
		"agent/state.json",
		"  leading and trailing  ",
		"…truncated",
		"● running",
		"\x1b[31mred\x1b[0m",
		"héllo wörld",
		"a\tb",
		"a\nb",
	}
	for _, r := range allRoles() {
		style := r.style(Plain())
		for _, raw := range inputs {
			in := textx.Sanitize(raw)
			if got := style.Render(in); got != in {
				t.Errorf("Plain().%s.Render(%q) = %q", r.name, in, got)
			}
		}
	}
}

// TestPlainExpandsATab pins the one input a Plain style does not hand back
// unchanged, which is the limit documented on [Plain]. lipgloss expands a tab
// whatever the style carries, so the identity the golden files rest on is a
// property of sanitized text; the widths agreeing is what makes a tab that
// slipped past textx.Sanitize cost the same cells as one that did not.
func TestPlainExpandsATab(t *testing.T) {
	const raw = "a\tb"
	want := "a" + strings.Repeat(" ", textx.TabWidth) + "b"
	if got := Plain().Body().Render(raw); got != want {
		t.Errorf("Plain().Body().Render(%q) = %q, want %q", raw, got, want)
	}
}

// TestStatusRolesMirrorTheContractVocabulary holds the copy [StatusRole] makes
// of the document contract faithful. The shipped package does not import
// agentstate, so a status added to the contract would otherwise reach the
// screen with no colour and no glyph; the test imports what the package will
// not.
func TestStatusRolesMirrorTheContractVocabulary(t *testing.T) {
	declarable := make([]string, 0, statusRoleCount)
	for i := range statusRoleCount {
		if r := StatusRole(i); r != RoleUnknown {
			declarable = append(declarable, r.String())
		}
	}
	if want := agentstate.Vocabulary(); !slices.Equal(declarable, want) {
		t.Errorf("status roles spell %v, the contract vocabulary is %v", declarable, want)
	}
	if got, want := RoleUnknown.String(), agentstate.StatusUnknown.String(); got != want {
		t.Errorf("the undecodable state is %q here and %q in the contract", got, want)
	}
}

func TestPlainIsTheZeroStyleForEveryRole(t *testing.T) {
	var zero lipgloss.Style
	for _, r := range allRoles() {
		if got := r.style(Plain()); !reflect.DeepEqual(got, zero) {
			t.Errorf("Plain().%s carries styling", r.name)
		}
	}
}

func TestZeroPaletteRendersUnchanged(t *testing.T) {
	var p Palette
	for _, r := range allRoles() {
		if got := r.style(p).Render("x"); got != "x" {
			t.Errorf("zero Palette %s rendered %q", r.name, got)
		}
	}
	if got := p.Glyphs(); got != (Glyphs{}) {
		t.Errorf("zero Palette carries glyphs: %+v", got)
	}
}

func TestRolesOutsideTheVocabularyResolve(t *testing.T) {
	p := Dark()
	same := func(name string, got, want lipgloss.Style) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s did not resolve to the documented fallback", name)
		}
	}
	same("status below", p.Status(-1), p.Status(RoleUnknown))
	same("status above", p.Status(StatusRole(statusRoleCount)), p.Status(RoleUnknown))
	same("severity below", p.Severity(-1), p.Severity(RoleInfo))
	same("severity above", p.Severity(SeverityRole(severityRoleCount)), p.Severity(RoleInfo))
	same("json below", p.JSON(-1), p.JSON(RolePunct))
	same("json above", p.JSON(JSONRole(jsonRoleCount)), p.JSON(RolePunct))
	same("log below", p.LogLevel(-1), p.LogLevel(RoleInfoLevel))
	same("log above", p.LogLevel(LogRole(logRoleCount)), p.LogLevel(RoleInfoLevel))
}

func TestWithGlyphsLeavesTheSourceAlone(t *testing.T) {
	base := Dark()
	swapped := base.WithGlyphs(ASCIIGlyphs())
	if base.Glyphs() != UnicodeGlyphs() {
		t.Error("WithGlyphs mutated the palette it was called on")
	}
	if swapped.Glyphs() != ASCIIGlyphs() {
		t.Error("WithGlyphs did not take")
	}
	if !reflect.DeepEqual(swapped.Body(), base.Body()) {
		t.Error("WithGlyphs disturbed the colours")
	}
}

// TestConstructorsHandBackOneTable holds the property that lets a render loop
// call a constructor per row: a call resolves the appearance table, it does not
// rebuild it. Comparing the tables by value would pass either way, so the
// comparison is on the handle.
func TestConstructorsHandBackOneTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() Palette
	}{
		{"Dark", Dark},
		{"Light", Light},
		{"Plain", Plain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := tc.make(), tc.make()
			if first.styles != second.styles {
				t.Error("two calls built two tables")
			}
			if !reflect.DeepEqual(first, second) {
				t.Error("two calls disagree")
			}
		})
	}
	if Dark().styles == Light().styles {
		t.Error("Dark and Light share one table")
	}
	if Dark().styles == Plain().styles {
		t.Error("Dark and Plain share one table")
	}
}

func TestRoleNames(t *testing.T) {
	for _, tc := range []struct {
		got  string
		want string
	}{
		{RoleRunning.String(), "running"},
		{RoleUnknown.String(), "unknown"},
		{StatusRole(-1).String(), "status(-1)"},
		{StatusRole(statusRoleCount).String(), "status(6)"},
		{RoleWarning.String(), "warning"},
		{SeverityRole(-1).String(), "severity(-1)"},
		{SeverityRole(severityRoleCount).String(), "severity(3)"},
		{RoleNull.String(), "null"},
		{JSONRole(-1).String(), "json(-1)"},
		{JSONRole(jsonRoleCount).String(), "json(6)"},
		{RoleInfoLevel.String(), "info"},
		{LogRole(-1).String(), "log(-1)"},
		{LogRole(logRoleCount).String(), "log(5)"},
	} {
		if tc.got != tc.want {
			t.Errorf("String() = %q, want %q", tc.got, tc.want)
		}
	}
}

// FuzzPlainRendersSanitisedInputUnchanged holds the property the golden files
// rest on: text that has been through textx.Sanitize comes back from a Plain
// style byte for byte, whatever the workspace wrote.
func FuzzPlainRendersSanitisedInputUnchanged(f *testing.F) {
	for _, seed := range []string{"", "x", "a\tb", "a\nb", "\x1b]52;c;YQ==\a", "\u202eexe.txt", "日本語", "  "} {
		f.Add(seed)
	}
	roles := allRoles()
	f.Fuzz(func(t *testing.T, raw string) {
		in := textx.Sanitize(raw)
		for _, r := range roles {
			if got := r.style(Plain()).Render(in); got != in {
				t.Fatalf("Plain().%s.Render(%q) = %q", r.name, in, got)
			}
		}
	})
}

// FuzzRoleLookupIsTotal holds that no role value, however it was arrived at,
// takes a lookup outside the table: a render must not fail mid-frame.
func FuzzRoleLookupIsTotal(f *testing.F) {
	for _, seed := range []int{-1 << 31, -1, 0, 3, 1 << 20} {
		f.Add(seed)
	}
	var zero lipgloss.Style
	f.Fuzz(func(t *testing.T, i int) {
		p := Dark()
		styled := func(name string, got lipgloss.Style) {
			t.Helper()
			if reflect.DeepEqual(got, zero) {
				t.Fatalf("%s(%d) resolved to the zero Style", name, i)
			}
		}
		styled("status", p.Status(StatusRole(i)))
		styled("severity", p.Severity(SeverityRole(i)))
		styled("json", p.JSON(JSONRole(i)))
		styled("log", p.LogLevel(LogRole(i)))

		if p.StatusGlyph(StatusRole(i)) == "" {
			t.Fatalf("StatusGlyph(%d) is empty", i)
		}
		if p.SeverityGlyph(SeverityRole(i)) == "" {
			t.Fatalf("SeverityGlyph(%d) is empty", i)
		}
	})
}

func pickHex(t tone, isDark bool) string {
	if isDark {
		return t.dark
	}
	return t.light
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
