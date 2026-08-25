package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// colourAxes groups the roles that compete for the same column. Two roles on
// different axes are told apart by where they sit — a log level and the body
// text of the same line are never mistaken for one another — so only
// within-axis distinctness carries meaning, and requiring more would forbid the
// accent and the focused frame from agreeing on purpose.
func colourAxes() map[string][]role {
	return map[string][]role{
		"text":      {roleTitle, roleBody, roleDim, roleAccent, roleDirectory, roleRecent},
		"highlight": {roleCursor, roleMatch, roleMatchCurrent},
		"frame":     {roleBorderBlurred, roleBorderFocused},
		"status": {
			roleStatusRunning, roleStatusIdle, roleStatusBlocked,
			roleStatusError, roleStatusDone, roleStatusUnknown,
		},
		"severity": {roleSeverityInfo, roleSeverityWarning, roleSeveritySevere},
		"json": {
			roleJSONKey, roleJSONString, roleJSONNumber,
			roleJSONBool, roleJSONNull, roleJSONPunct,
		},
		"log": {roleLogTrace, roleLogDebug, roleLogInfo, roleLogWarn, roleLogError},
	}
}

// roleLabels maps a table entry to the name the public accessor goes by, so a
// distinctness failure names the two roles a reader would have to tell apart.
func roleLabels() map[role]string {
	labels := make(map[role]string, roleCount)
	for _, r := range allRoles() {
		labels[r.entry] = r.name
	}
	return labels
}

// TestColoursAreDistinctWithinTheirAxis is the other half of the accessibility
// floor: glyphs keep the states apart when colour is off, and this keeps them
// apart when it is on. Two roles that draw alike collapse into one signal on a
// coloured terminal however different their names are.
func TestColoursAreDistinctWithinTheirAxis(t *testing.T) {
	labels := roleLabels()
	for _, bg := range []struct {
		name   string
		isDark bool
	}{
		{"dark", true},
		{"light", false},
	} {
		for axis, members := range colourAxes() {
			seen := make(map[string]role, len(members))
			for _, r := range members {
				a := appearances[r]
				drawn := fmt.Sprintf("fg=%s bg=%s bold=%v italic=%v",
					pickHex(a.fg, bg.isDark), pickHex(a.bg, bg.isDark), a.bold, a.italic)
				if prev, dup := seen[drawn]; dup {
					t.Errorf("%s/%s: %s and %s both draw %s",
						bg.name, axis, labels[prev], labels[r], drawn)
				}
				seen[drawn] = r
			}
		}
	}
}

// TestColourAxesCoverTheTable keeps [colourAxes] honest: a role added to the
// table and not placed on an axis would never be checked against the roles it
// shares a column with.
func TestColourAxesCoverTheTable(t *testing.T) {
	placed := make(map[role]string, roleCount)
	for axis, members := range colourAxes() {
		for _, r := range members {
			if prev, dup := placed[r]; dup {
				t.Errorf("table entry %d sits on both the %s and %s axes", r, prev, axis)
			}
			placed[r] = axis
		}
	}
	if len(placed) != int(roleCount) {
		t.Errorf("colour axes place %d roles, the table holds %d", len(placed), roleCount)
	}
}

// colourLiteral matches the ways a colour is named in Go: a lipgloss colour
// constructor, or an sRGB hex spelling.
var colourLiteral = regexp.MustCompile(`lipgloss\.(Color|ANSIColor|RGBColor|CompleteColor|AdaptiveColor)\b|#[0-9a-fA-F]{6}\b`)

// TestNoOtherPackageNamesAColour enforces the rule the package comment states.
// Stated and not enforced, the rule is a request, and one lipgloss.Color("8")
// written in a pane puts the meaning of a role back into a string literal that
// the next pane copies. Test files are exempt: an expected escape sequence is
// an assertion about output, not a colour choice.
func TestNoOtherPackageNamesAColour(t *testing.T) {
	const moduleRoot = "../../.." // the package directory is internal/ui/theme.
	self, err := os.Getwd()
	if err != nil {
		t.Fatalf("locating the package directory: %v", err)
	}

	walkErr := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if abs, absErr := filepath.Abs(path); absErr == nil && abs == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			if hit := colourLiteral.FindString(line); hit != "" {
				t.Errorf("%s:%d names a colour (%s); ask theme for a role instead", path, i+1, hit)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", moduleRoot, walkErr)
	}
}
