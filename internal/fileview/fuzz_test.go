package fileview

import (
	"strings"
	"testing"
	"unicode/utf8"

	"testing/fstest"

	"github.com/stxkxs/agentfs/internal/textx"
)

// lexerSeeds are inputs each lexer fuzz target starts from: well-formed lines,
// lines cut off mid-token as a document read while it is written is, and lines
// carrying the bytes a workspace is not trusted with.
var lexerSeeds = []string{
	"",
	" ",
	`{"status": "running", "step": 3}`,
	`{"status": "runn`,
	"status: running # note",
	"- name: 'agent'",
	"2026-08-24T10:00:00Z ERROR run failed",
	`level=warn msg="held"`,
	"héllo wörld",
	"日本語",
	"\x1b]52;c;YWdlbnRmcw==\x07",
	"\xff\xfe\x00",
	strings.Repeat("{", 64),
	`"unterminated`,
}

func fuzzLexer(f *testing.F, kind Kind) {
	f.Helper()
	for _, seed := range lexerSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for _, line := range []string{raw, textx.Sanitize(raw)} {
			spans := Highlight(kind, line)
			checkSpans(t, kind, line, spans)
			if again := Highlight(kind, line); !equalSpans(spans, again) {
				t.Fatalf("Highlight(%v, %q) is not deterministic: %+v then %+v", kind, line, spans, again)
			}
		}
	})
}

func equalSpans(a, b []Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func FuzzLexJSON(f *testing.F) { fuzzLexer(f, KindJSON) }

func FuzzLexNDJSON(f *testing.F) { fuzzLexer(f, KindNDJSON) }

func FuzzLexYAML(f *testing.F) { fuzzLexer(f, KindYAML) }

func FuzzLexLog(f *testing.F) { fuzzLexer(f, KindLog) }

func FuzzHighlightEveryKind(f *testing.F) {
	for _, seed := range lexerSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		line := textx.Sanitize(raw)
		for _, kind := range allKinds {
			checkSpans(t, kind, line, Highlight(kind, line))
		}
	})
}

func FuzzDetectKind(f *testing.F) {
	f.Add("state.json", "{\"a\":1}")
	f.Add("agent.log", "INFO up")
	f.Add("config.yaml", "a: b")
	f.Add("blob", "\x00\x01")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, name, head string) {
		kind := DetectKind(name, []byte(head))
		if kind.String() == "" {
			t.Fatalf("DetectKind(%q, %q) = %d, which names nothing", name, head, kind)
		}
		if kind < KindPlain || kind > KindBinary {
			t.Fatalf("DetectKind(%q, %q) = %d, outside the vocabulary", name, head, kind)
		}
	})
}

func FuzzLoad(f *testing.F) {
	f.Add("one\ntwo\n", "three\n")
	f.Add("{\"a\": 1}\n", "{\"b\": 2}\n")
	f.Add("\x1b]52;c;YWdlbnRmcw==\x07one\n", "\x1b[2Jtwo\n")
	f.Add("\x00binary\n", "more\n")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, body, added string) {
		for _, tail := range []bool{false, true} {
			m := mapFS(map[string]string{logName: body})
			root, _ := countingRoot(m)
			w := Load(root, logName, Options{MaxBytes: 48, Tail: tail})
			checkWindow(t, w, 48)

			appendTo(m, logName, added)
			if err := w.Follow(root, logName); err != nil {
				t.Fatalf("Follow: %v", err)
			}
			checkWindow(t, w, 48)
		}
	})
}

// checkWindow asserts what every window guarantees whatever it was built from:
// it holds no more than it was asked to, and every line it offers is safe to
// render and correctly spanned.
func checkWindow(t *testing.T, w *Window, maxBytes int64) {
	t.Helper()
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if int64(len(w.buf)) > maxBytes {
		t.Fatalf("held %d bytes, want at most %d", len(w.buf), maxBytes)
	}
	if len(w.Lines()) > MaxLines {
		t.Fatalf("held %d lines, want at most %d", len(w.Lines()), MaxLines)
	}
	for i, line := range w.Lines() {
		if !utf8.ValidString(line.Text) {
			t.Fatalf("line %d = %q is not valid UTF-8", i, line.Text)
		}
		if strings.ContainsAny(line.Text, "\x00\x1b\n\r") {
			t.Fatalf("line %d = %q carries a byte a terminal acts on", i, line.Text)
		}
		checkSpans(t, w.Kind(), line.Text, line.Spans)
	}
}

func FuzzFollowRotation(f *testing.F) {
	f.Add("aaaa\nbbbb\n", "cccc\ndddd\n")
	f.Add("one\n", "")
	f.Add("", "two\n")
	f.Fuzz(func(t *testing.T, before, after string) {
		m := mapFS(map[string]string{logName: before})
		root, _ := countingRoot(m)
		w := Load(root, logName, Options{MaxBytes: 64, Tail: true})
		checkWindow(t, w, 64)

		m[logName] = &fstest.MapFile{Data: []byte(after)}
		if err := w.Follow(root, logName); err != nil {
			t.Fatalf("Follow: %v", err)
		}
		checkWindow(t, w, 64)
		if w.Size() != int64(len(after)) {
			t.Fatalf("Size() = %d, want %d", w.Size(), len(after))
		}
	})
}
