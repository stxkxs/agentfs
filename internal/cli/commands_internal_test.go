package cli

import (
	"testing"

	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/ui/theme"
)

// Colour is a choice about the reader, not about the run: never means no escape
// sequence wherever the output goes, always means styling even into a pager,
// and auto asks the terminal.
func TestThePaletteResolvesTheColourChoiceAgainstTheTerminal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		color       string
		interactive bool
		dark        bool
		want        theme.Palette
	}{
		{"never, on a terminal", config.ColorNever, true, true, theme.Plain()},
		{"always, into a pipe", config.ColorAlways, false, true, theme.Dark()},
		{"always, into a pipe on a light terminal", config.ColorAlways, false, false, theme.Light()},
		{"auto, into a pipe", config.ColorAuto, false, true, theme.Plain()},
		{"auto, on a dark terminal", config.ColorAuto, true, true, theme.Dark()},
		{"auto, on a light terminal", config.ColorAuto, true, false, theme.Light()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			cfg.Color = tt.color
			env := Env{Interactive: tt.interactive, DarkBackground: tt.dark}

			if got := palette(env, cfg); got != tt.want {
				t.Errorf("color=%s resolved to the wrong palette", tt.color)
			}
		})
	}
}

// A font that draws the default marks as replacement boxes costs the glyph set,
// not the colour: every distinction the palette makes is carried by both, so
// dropping one must not drop the other.
func TestTheASCIIChoiceSwapsTheGlyphsAndKeepsTheStyling(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Color = config.ColorAlways
	env := Env{DarkBackground: true}

	if got := palette(env, cfg).Glyphs(); got != theme.UnicodeGlyphs() {
		t.Error("the default marks are not the unicode set")
	}

	cfg.ASCII = true
	ascii := palette(env, cfg)
	if got := ascii.Glyphs(); got != theme.ASCIIGlyphs() {
		t.Error("the ascii setting did not swap the marks")
	}
	if got, want := ascii.Title().Render("x"), theme.Dark().Title().Render("x"); got != want {
		t.Errorf("the ascii setting changed the styling: %q, want %q", got, want)
	}
}

// mustLookup resolves a name the command table is expected to hold. A caller
// passes a literal, so an absent name is a table that no longer carries what
// the code around it assumes rather than a condition to report at runtime.
func TestLookingUpAnAbsentCommandIsRefused(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"scan", "watch"} {
		if got := mustLookup(name); got.Name != name {
			t.Errorf("mustLookup(%q) resolved to %q", name, got.Name)
		}
	}

	defer func() {
		if recover() == nil {
			t.Error("mustLookup returned for a name the table does not hold")
		}
	}()
	mustLookup("no-such-command")
}
