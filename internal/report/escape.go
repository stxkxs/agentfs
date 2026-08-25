package report

import (
	"bytes"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// escapeInvisible rewrites the runes a terminal acts on into their \uXXXX
// escapes.
//
// A JSON encoder escapes the C0 controls, so an ESC in a payload is already
// inert. It does not escape the bidirectional and zero-width format
// characters, which pass through raw — and a consumer that prints a member of
// this envelope into a terminal is handed a right-to-left override that
// reverses everything after it.
//
// Escaping rather than replacing keeps the value recoverable: a consumer that
// decodes the JSON gets the rune the workspace wrote, and one that cats the
// stream sees six ASCII characters. A path member has to stay openable, so it
// cannot be masked the way display text is.
func escapeInvisible(b []byte) []byte {
	if !bytes.ContainsFunc(b, invisible) {
		return b
	}

	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if invisible(r) {
			out = appendEscape(out, r)
			i += size
			continue
		}
		out = append(out, b[i:i+size]...)
		i += size
	}
	return out
}

// appendEscape writes r as a JSON \u escape. A rune outside the basic
// multilingual plane has no single-unit form, so it is written as the
// surrogate pair JSON defines — a five-digit escape is not JSON, and a
// consumer would fail to parse the envelope rather than fail to render one
// rune of it.
func appendEscape(out []byte, r rune) []byte {
	if r <= 0xFFFF {
		return fmt.Appendf(out, `\u%04x`, r)
	}
	r -= 0x10000
	high := 0xD800 + (r >> 10)
	low := 0xDC00 + (r & 0x3FF)
	return fmt.Appendf(out, `\u%04x\u%04x`, high, low)
}

// invisible reports whether a rune reorders or hides the text around it.
//
// The set is named by Unicode category rather than enumerated: an
// enumeration covers the characters somebody thought of, and a consumer
// printing this envelope is exposed to the ones nobody did.
func invisible(r rune) bool {
	return unicode.In(r, unicode.Cf) ||
		(r >= 0xFE00 && r <= 0xFE0F) ||
		(r >= 0xE0100 && r <= 0xE01EF) ||
		unicode.Is(unicode.Cs, r)
}
