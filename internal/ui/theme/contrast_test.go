package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestAppearanceTableIsComplete holds that every role names a colour on both
// backgrounds. A role left blank would build into the zero style, which renders
// whatever the terminal happened to be using.
func TestAppearanceTableIsComplete(t *testing.T) {
	for i, a := range appearances {
		r := role(i)
		if !a.fg.set() {
			t.Errorf("table entry %d has no foreground", r)
		}
		for _, hex := range []string{a.fg.light, a.fg.dark, a.bg.light, a.bg.dark} {
			if hex == "" {
				continue
			}
			if _, err := parseHex(hex); err != nil {
				t.Errorf("table entry %d: %v", r, err)
			}
		}
	}
}

// TestBackgroundsAreDistinguishable holds the two reference backgrounds apart,
// so a contrast figure computed against one is not accidentally a figure
// against the other.
func TestBackgroundsAreDistinguishable(t *testing.T) {
	if got := contrast(t, darkBackground, lightBackground); got < minContrast {
		t.Errorf("the reference backgrounds contrast at %.2f:1", got)
	}
}

// TestEveryForegroundClearsTheContrastFloor is the legibility invariant the
// package comment states. A role that paints its own background is measured
// against that fill; every other role against the terminal's.
func TestEveryForegroundClearsTheContrastFloor(t *testing.T) {
	for _, bg := range []struct {
		name     string
		isDark   bool
		terminal string
	}{
		{"dark", true, darkBackground},
		{"light", false, lightBackground},
	} {
		for _, r := range allRoles() {
			a := appearances[r.entry]
			behind := bg.terminal
			if a.bg.set() {
				behind = pickHex(a.bg, bg.isDark)
			}
			fg := pickHex(a.fg, bg.isDark)
			if got := contrast(t, fg, behind); got < minContrast {
				t.Errorf("%s/%s: %s on %s contrasts at %.2f:1, want %.1f:1",
					bg.name, r.name, fg, behind, got, minContrast)
			}
		}
	}
}

func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, err := luminance(a)
	if err != nil {
		t.Fatalf("%v", err)
	}
	lb, err := luminance(b)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance returns the WCAG 2.1 relative luminance of an sRGB colour.
func luminance(hex string) (float64, error) {
	rgb, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	channel := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(rgb[0]) + 0.7152*channel(rgb[1]) + 0.0722*channel(rgb[2]), nil
}

func parseHex(hex string) ([3]uint8, error) {
	var rgb [3]uint8
	if len(hex) != 7 || !strings.HasPrefix(hex, "#") {
		return rgb, fmt.Errorf("%q is not a #rrggbb colour", hex)
	}
	for i := range rgb {
		v, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
		if err != nil {
			return rgb, fmt.Errorf("%q is not a #rrggbb colour", hex)
		}
		rgb[i] = uint8(v)
	}
	return rgb, nil
}
