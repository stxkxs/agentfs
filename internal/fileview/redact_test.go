package fileview_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/fileview"
	"github.com/stxkxs/agentfs/internal/fsx"
)

var secretKeys = []string{"token", "api_key", "authorization"}

func TestRedactMasksTheNamedMembers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"plain member", `{"token": "abc123"}`, `{"token": "` + fileview.Mask + `"}`},
		{"no space", `{"token":"abc123"}`, `{"token":"` + fileview.Mask + `"}`},
		{"among others", `{"task": "index", "token": "abc123", "step": 3}`,
			`{"task": "index", "token": "` + fileview.Mask + `", "step": 3}`},
		{"case folded", `{"TOKEN": "abc123"}`, `{"TOKEN": "` + fileview.Mask + `"}`},
		{"separator folded", `{"api-key": "abc123"}`, `{"api-key": "` + fileview.Mask + `"}`},
		{"camel folded", `{"apiKey": "abc123"}`, `{"apiKey": "` + fileview.Mask + `"}`},
		{"unrelated member", `{"task": "abc123"}`, `{"task": "abc123"}`},
		{"a number is left alone", `{"token": 42}`, `{"token": 42}`},
		{"an object is left alone", `{"token": {"a": 1}}`, `{"token": {"a": 1}}`},
		{"escaped quote in the value", `{"token": "a\"b"}`, `{"token": "` + fileview.Mask + `"}`},
		{"not a member", `the token is abc123`, `the token is abc123`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := fileview.Redact(tc.in, secretKeys); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A mask whose length tracked the secret would leak its length.
func TestTheMaskIsAFixedWidth(t *testing.T) {
	t.Parallel()
	short := fileview.Redact(`{"token": "a"}`, secretKeys)
	long := fileview.Redact(`{"token": "`+strings.Repeat("a", 400)+`"}`, secretKeys)
	if len(short) != len(long) {
		t.Fatalf("a one-character secret renders %d bytes and a four-hundred-character one %d",
			len(short), len(long))
	}
}

func TestRedactWithNoKeysChangesNothing(t *testing.T) {
	t.Parallel()
	const line = `{"token": "abc123"}`
	if got := fileview.Redact(line, nil); got != line {
		t.Fatalf("Redact with no keys returned %q", got)
	}
}

// The flag exists so a credential in a state document is not put on a shared
// screen by the tool watching it, which means it has to reach the preview.
func TestLoadRedactsThroughTheWindow(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"state.json": {Data: []byte(`{"schema":"agentfs/v1","status":"running","token":"s3cr3t"}`)},
	}
	w := fileview.Load(fsx.New("ws", fsys), "state.json", fileview.Options{RedactKeys: secretKeys})

	var text string
	for _, line := range w.Lines() {
		text += line.Text
	}
	if strings.Contains(text, "s3cr3t") {
		t.Fatalf("the secret reached the preview:\n%s", text)
	}
	if !strings.Contains(text, fileview.Mask) {
		t.Fatalf("nothing was masked:\n%s", text)
	}
	if !strings.Contains(text, "running") {
		t.Fatalf("redaction removed a member it was not asked for:\n%s", text)
	}
}

// Spans index the redacted text, so a renderer walking them addresses bytes
// that are there.
func TestRedactedLinesCarryValidSpans(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"state.json": {Data: []byte(`{"token":"s3cr3t","task":"index"}`)},
	}
	w := fileview.Load(fsx.New("ws", fsys), "state.json", fileview.Options{RedactKeys: secretKeys})

	for _, line := range w.Lines() {
		for _, span := range line.Spans {
			if span.Start < 0 || span.End > len(line.Text) || span.Start > span.End {
				t.Errorf("span %v is out of range for %q", span, line.Text)
			}
		}
	}
}

func FuzzRedact(f *testing.F) {
	f.Add(`{"token": "abc"}`)
	f.Add(`{"token": `)
	f.Add(`{"a\"b": "c"}`)
	f.Add(`"""""`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, line string) {
		got := fileview.Redact(line, secretKeys)
		if strings.Contains(line, "\n") || strings.Contains(got, "\n") {
			t.Skip()
		}
		// Redaction only ever replaces a value, so it can neither drop the
		// members around it nor grow without bound.
		if len(got) > len(line)+len(fileview.Mask)*len(line) {
			t.Fatalf("Redact(%q) grew to %d bytes", line, len(got))
		}
		if fileview.Redact(got, secretKeys) != got {
			t.Fatalf("Redact is not idempotent for %q", line)
		}
	})
}
