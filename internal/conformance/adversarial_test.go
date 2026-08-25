package conformance_test

import (
	"path"
	"slices"
	"strconv"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// minimumAdversarialCases is the floor on cases whose document carries a rune a
// terminal acts on rather than draws.
//
// The floor is derived from the paths such a rune takes to an operator rather
// than from a tally of what the corpus holds. A hostile rune reaches a screen
// through the pointer a diagnostic names, through the value it quotes back,
// through the first line it quotes when the document is not JSON at all, and
// through the state a pane renders for a document that raises nothing. Each is
// a separate path with its own way to lose its neutralization, and one case per
// path is a path covered by accident rather than on purpose. Two per path is
// the floor, so eight. A corpus that drifts back to shape and vocabulary
// violations alone falls below it.
const minimumAdversarialCases = 8

// TestTheCorpusCarriesAdversarialCases asserts the corpus evaluates the decoder
// against the documents a hostile writer produces, not only against the ones a
// careless writer produces.
//
// A wrong type and a status outside the vocabulary are what an integrator gets
// wrong. A terminal escape in a member name is what someone puts there on
// purpose, and it is a different defect class: what the decoder owes is a
// diagnostic whose text is safe to print, and no amount of coverage over wrong
// types exercises that.
func TestTheCorpusCarriesAdversarialCases(t *testing.T) {
	var carrying []string
	for _, c := range loadCases(t) {
		if len(documentRunes(c.src)) > 0 {
			carrying = append(carrying, c.name)
		}
	}
	if len(carrying) < minimumAdversarialCases {
		t.Errorf("%d cases carry a rune a terminal acts on, want at least %d: %v",
			len(carrying), minimumAdversarialCases, carrying)
	}
}

// TestNoCaseRendersATerminalControl asserts no document in the corpus produces
// a diagnostic an operator's terminal acts on.
//
// This is the corpus-level statement of the invariant [diag.About] holds: every
// member a workspace can influence is neutralized at construction, so a
// diagnostic is safe wherever it is printed. Holding it over the corpus as well
// as in the constructor's own tests makes it an assertion about the documents a
// workspace presents rather than about the strings a unit test thought to pass
// in.
//
// Each case is read twice, once at the path the corpus stores it under and once
// under a directory name carrying the sequences an agent can put in one, so the
// path a diagnostic stamps is driven as well as the members the document
// supplies. Of the five members [textMembers] scans, that leaves the hint: the
// decoder composes every hint from contract constants, so no state document
// reaches one, and a corpus of state documents cannot hold [diag.About] to
// neutralizing it.
func TestNoCaseRendersATerminalControl(t *testing.T) {
	for _, c := range loadCases(t) {
		t.Run(c.name, func(t *testing.T) {
			for _, docPath := range []string{c.docPath(), hostileDirectory(c.docPath())} {
				_, ds := agentstate.Decode(docPath, c.src, decodeOptions())
				for _, d := range ds {
					for _, member := range textMembers(d) {
						for _, r := range unsafeRunes(member.text) {
							t.Errorf("%s carries U+%04X in its %s: %q", d.Code, r, member.name, member.text)
						}
					}
				}
			}
		})
	}
}

// hostileDirectory returns p under a directory an agent named to reach the
// operator's terminal.
//
// A document's path is workspace input the same way a member name is: agentfs
// reads state at <agent>/<run>/state.json, so every segment above the file name
// is a directory an agent created and a diagnostic stamps back. A case cannot
// carry that in its own directory name — a corpus checked out on a filesystem
// that refuses the bytes is a corpus that will not load — so the gate supplies
// the segment the workspace would. The file name is left alone, because the
// decoder reads it to recognize a compatibility filename.
func hostileDirectory(p string) string {
	return path.Join("\x1b]52;c;cGF5bG9hZA==\x07\u202eagent\u200d\ufe0f", p)
}

// textMember is one diagnostic member that carries text, named for a failure
// message.
type textMember struct {
	name string
	text string
}

// textMembers returns every member of a diagnostic that holds text. A
// diagnostic's severity, position and code are not workspace input; everything
// else is.
func textMembers(d diag.Diagnostic) []textMember {
	return []textMember{
		{"path", d.Path},
		{"pointer", d.Pointer},
		{"message", d.Message},
		{"hint", d.Hint},
		{"value", d.Value},
	}
}

// actsOnTerminal reports whether a terminal acts on r rather than drawing it.
//
// The set is named by property rather than enumerated, for the reason the
// sanitizer in package textx gives for naming it the same way: an enumeration
// lists the characters somebody thought of, and the ones nobody thought of are
// the ones an attacker reaches for. Naming it here independently of the code it
// gates is what keeps this a check rather than a restatement, because a
// sanitizer that stopped replacing a category would still satisfy a gate that
// read its answer from the sanitizer.
func actsOnTerminal(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		// C0, DEL and C1. ESC opens every sequence that moves a cursor, clears
		// a frame or writes the system clipboard.
		return true
	case unicode.Is(unicode.Cf, r):
		// Every format character, the category the bidirectional overrides and
		// the zero-width joiners belong to.
		return true
	case unicode.Is(unicode.Cs, r):
		// A surrogate is not text.
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		// A variation selector is a mark rather than a format character, and
		// changes how the rune before it renders without occupying a cell.
		return true
	default:
		return false
	}
}

// unsafeRunes returns the runes in s a terminal acts on, reporting each byte
// that is not part of a valid encoding as [utf8.RuneError]. A rune that is
// already U+FFFD is a stand-in a sanitizer wrote and is left alone, so the
// result names bytes rather than the marks that replaced them.
func unsafeRunes(s string) []rune {
	var out []rune
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			out = append(out, utf8.RuneError)
		case actsOnTerminal(r):
			out = append(out, r)
		}
		i += size
	}
	return out
}

// documentRunes returns the runes a document carries that a terminal acts on,
// reading an escape as the rune it names.
//
// A document is scanned as bytes rather than as a decoded value because the two
// differ exactly where an adversarial document lives: encoding/json folds an
// unpaired surrogate escape to U+FFFD, so by the time a document carrying one
// is a Go value it is indistinguishable from one that never did. In well-formed
// JSON a backslash appears only inside a string, so reading escapes without
// tracking string state reads the escapes the document holds and no others —
// provided every escape is consumed whole, which is what keeps a document that
// merely spells the six characters of a `\u001b` escape from being read as
// one carrying an ESC.
//
// JSON's insignificant whitespace is layout rather than content, so a tab, a
// line feed and a carriage return between tokens do not count. The grammar
// admits neither inside a string, so a document that carries one carries it as
// an escape, which does count.
func documentRunes(src []byte) []rune {
	var out []rune
	for i := 0; i < len(src); {
		if r, width, ok := jsonEscape(src[i:]); ok {
			if actsOnTerminal(r) {
				out = append(out, r)
			}
			i += width
			continue
		}
		r, size := utf8.DecodeRune(src[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			out = append(out, utf8.RuneError)
		case r == '\t', r == '\n', r == '\r':
		case actsOnTerminal(r):
			out = append(out, r)
		}
		i += size
	}
	return out
}

// jsonEscape reads the escape at the start of b, returning the rune it names
// and the bytes it occupies. A surrogate pair is read as its two halves, which
// is what makes an unpaired half visible.
//
// The two-character escapes are read as well as \uXXXX, for two reasons. An
// escaped control is a control the value carries, so it belongs in the answer.
// And an escaped backslash read as one backslash leaves the following u and
// four hex digits looking like an escape, so a document spelling the six
// characters of `\u001b` would be counted as one carrying the rune — a false
// positive a floor gate cannot afford, because it is satisfied by documents
// that carry nothing.
func jsonEscape(b []byte) (escaped rune, width int, ok bool) {
	if len(b) < 2 || b[0] != '\\' {
		return 0, 0, false
	}
	if b[1] == 'u' {
		const size = len(`\uXXXX`)
		if len(b) < size {
			return 0, 0, false
		}
		n, err := strconv.ParseUint(string(b[2:size]), 16, 32)
		if err != nil {
			return 0, 0, false
		}
		return rune(n), size, true
	}
	switch b[1] {
	case '"':
		return '"', 2, true
	case '\\':
		return '\\', 2, true
	case '/':
		return '/', 2, true
	case 'b':
		return '\b', 2, true
	case 'f':
		return '\f', 2, true
	case 'n':
		return '\n', 2, true
	case 'r':
		return '\r', 2, true
	case 't':
		return '\t', 2, true
	default:
		return 0, 0, false
	}
}

// TestUnsafeRuneScanIsExact asserts the two scans the gates above are built on
// answer correctly on inputs whose answer is known independently: a diagnostic
// built from hostile text carries nothing, a document holding a hostile rune is
// found to hold it, and a clean document is not mistaken for one.
//
// A gate whose predicate matches nothing passes on every corpus, so the
// predicate is tested before it is trusted.
func TestUnsafeRuneScanIsExact(t *testing.T) {
	t.Run("a diagnostic built from hostile text carries nothing", func(t *testing.T) {
		for _, in := range []string{
			"\x1b]52;c;cGF5bG9hZA==\x07clipboard",
			"\x1b[2Jcleared",
			"\x1bPq#0;2;0;0;0\x1b\\sixel",
			"before\x00after",
			"\u202egnp.exe\u202c",
			"co\u200dst\ufe0f",
			"\x7f\u061c",
			string([]byte{0xff, 0xc3, 0x28}),
		} {
			d := diag.About(diag.CodeNotJSON, in, in, in, in, in)
			for _, member := range textMembers(d) {
				if got := unsafeRunes(member.text); len(got) > 0 {
					t.Errorf("the %s built from %q carries %U", member.name, in, got)
				}
			}
		}
	})

	t.Run("a document's hostile runes are found", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
			want rune
		}{
			{"escaped ESC", `{"a":"\u001b[2J"}`, 0x1B},
			{"escaped BEL", `{"a":"\u0007"}`, 0x07},
			{"escaped right-to-left override", `{"a":"\u202e"}`, 0x202E},
			{"escaped Arabic letter mark", `{"a":"\u061c"}`, 0x061C},
			{"escaped zero-width joiner", `{"a":"\u200d"}`, 0x200D},
			{"escaped variation selector", `{"a":"\ufe0f"}`, 0xFE0F},
			{"unpaired surrogate", `{"a":"\ud800"}`, 0xD800},
			{"escaped line feed", `{"a":"first\nsecond"}`, '\n'},
			{"raw ESC ahead of the document", "\x1b]52;c;x\x07{}", 0x1B},
			{"invalid UTF-8", "{\"a\":\"\xff\"}", utf8.RuneError},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if got := documentRunes([]byte(tc.src)); !slices.Contains(got, tc.want) {
					t.Errorf("scanning %q found %U, want U+%04X among them", tc.src, got, tc.want)
				}
			})
		}
	})

	t.Run("a clean document carries nothing", func(t *testing.T) {
		src := "{\n\t\"schema\": \"agentfs/v1\",\n\t\"status\": \"running\",\n\t\"task\": \"Index \\u00e9t\\u00e9\"\n}\n"
		if got := documentRunes([]byte(src)); len(got) > 0 {
			t.Errorf("scanning a clean document found %U", got)
		}
	})

	// A floor is satisfied by whatever the scan counts, so a scan that reads an
	// escaped backslash as the start of an escape is a floor a corpus of
	// harmless documents meets: the six characters that spell an ESC would be
	// counted as one. The count is asserted rather than the presence, because
	// the defect is a document being read as carrying more than it does.
	t.Run("an escaped backslash does not open an escape", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  string
			want int
		}{
			{"a backslash before a u", `{"a":"C:\\users"}`, 0},
			{"a spelled-out escape", `{"a":"\\u001b is how ESC is written"}`, 0},
			{"a string terminator, whose second byte is a backslash", `{"a":"\u001bP\u001b\\"}`, 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := documentRunes([]byte(tc.src))
				escapes := 0
				for _, r := range got {
					if r == 0x1B {
						escapes++
					}
				}
				if escapes != tc.want {
					t.Errorf("scanning %q found %d ESC, want %d (all of %U)", tc.src, escapes, tc.want, got)
				}
			})
		}
	})
}
