package cli

import (
	"strings"
	"testing"
)

// A name too wide for the column takes a line of its own, so its description
// still begins where every other one does rather than being pushed along the
// row and out of the column.
func TestALongFlagNameTakesItsOwnLine(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	writeFlag(newPrinter(&out), "a-name-wider-than-the-whole-column", "duration", "", "Bounds something.")

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want the name and then its description:\n%s", len(lines), out.String())
	}
	if strings.TrimSpace(lines[0]) != "-a-name-wider-than-the-whole-column duration" {
		t.Errorf("the name line reads %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], strings.Repeat(" ", flagColumn)) || strings.TrimSpace(lines[1]) != "Bounds something." {
		t.Errorf("the description does not begin at column %d: %q", flagColumn, lines[1])
	}
}

// A column too narrow to hold a word has nothing to break on, so the text stays
// whole rather than being shredded a character at a time.
func TestWrapKeepsTextWholeWhenTheColumnCannotHoldAWord(t *testing.T) {
	t.Parallel()
	got := wrap("some text here", 4)
	if len(got) != 1 || got[0] != "some text here" {
		t.Errorf("wrap = %q, want the text whole", got)
	}
}

// A description with no words still occupies its row, so the column does not
// close up around it.
func TestWrapOfNothingIsOneEmptyLine(t *testing.T) {
	t.Parallel()
	got := wrap("   \t ", 40)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("wrap = %q, want one empty line", got)
	}
}

// A flag's help is the first sentence of the table's summary, and a summary
// carrying no full stop is one sentence already.
func TestFirstSentenceStopsAtTheFirstFullStop(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"One sentence. And another.":    "One sentence.",
		"no full stop anywhere":         "no full stop anywhere",
		".leading stop":                 ".leading stop",
		"Bounds descent below the root": "Bounds descent below the root",
	}
	for in, want := range tests {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// The table opens a summary with the Go field name because it documents a
// struct. The flag list opens with the verb, because the reader is looking at
// the flag. The name is matched by case-insensitive comparison against the flag
// with its dashes dropped, so a field spelled as an acronym is trimmed by the
// same rule as one spelled in camel case. A summary that is the field name and
// nothing else leaves no description behind, and rendering it must still
// produce a row.
func TestDescribeOpensWithTheVerb(t *testing.T) {
	t.Parallel()
	tests := []struct {
		summary, flag, want string
	}{
		{"MaxDepth bounds descent below the root.", "max-depth", "Bounds descent below the root."},
		{"MaxNodes caps the tree.", "max-nodes", "Caps the tree."},
		{"Watch selects change detection.", "watch", "Selects change detection."},
		{"StaleAfter is the silence after which a workspace is marked stale.", "stale-after",
			"The silence after which a workspace is marked stale."},
		{"RedactKeys names the members masked before rendering.", "redact-keys",
			"Names the members masked before rendering."},
		{"ASCII restricts the frame to ASCII glyphs.", "ascii", "Restricts the frame to ASCII glyphs."},
		{"DedupTTL is the window repeated events collapse within.", "dedup-ttl",
			"The window repeated events collapse within."},
		{"Select the output format.", "format", "Select the output format."},
		{"MaxDepth is ", "max-depth", ""},
	}
	for _, tt := range tests {
		if got := describe(tt.summary, tt.flag); got != tt.want {
			t.Errorf("describe(%q, %q) = %q, want %q", tt.summary, tt.flag, got, tt.want)
		}
	}
}

// A refusal names the formats the reader's command accepts, so the list has to
// read as a phrase at every length: one format stands alone, several take the
// serial comma and a closing "or", and a command that renders nothing says so
// rather than trailing off after the colon.
func TestAFormatListReadsAsAPhraseAtEveryLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		formats []string
		want    string
	}{
		{"none", nil, "no format"},
		{"one", []string{"text"}, "text"},
		{"two", []string{"text", "json"}, "text or json"},
		{"three", []string{"text", "json", "ndjson"}, "text, json or ndjson"},
	}
	for _, tt := range tests {
		if got := formatList(tt.formats); got != tt.want {
			t.Errorf("formatList(%q) = %q, want %q", tt.formats, got, tt.want)
		}
	}
}
