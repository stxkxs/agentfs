package diag

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Locate resolves an RFC 6901 JSON Pointer to a 1-based line and column within
// src. It returns 0, 0 when src is not JSON or the pointer names no member,
// so a caller can emit a positionless diagnostic rather than a wrong position.
func Locate(src []byte, pointer string) (line, column int) {
	off, ok := offsetOf(src, pointer)
	if !ok {
		return 0, 0
	}
	return lineColumn(src, off)
}

// EscapeToken encodes one member name as an RFC 6901 reference token: a tilde
// becomes ~0 and a slash becomes ~1. It is the inverse of the unescaping
// [Locate] applies, so a pointer built from it resolves to the member the name
// belongs to.
//
// A member name is chosen by the document. A name carrying a slash spliced into
// a pointer raw reads as a path through members the document does not have, and
// a name carrying a tilde escape reads as the member that escape decodes to —
// a different member of the same object.
func EscapeToken(name string) string {
	// The tilde is encoded first, or the tilde the slash's encoding introduces
	// is encoded in turn and the token decodes to a name the document has not
	// declared.
	name = strings.ReplaceAll(name, "~", "~0")
	return strings.ReplaceAll(name, "/", "~1")
}

// parsePointer splits an RFC 6901 pointer into its unescaped reference tokens.
func parsePointer(pointer string) ([]string, bool) {
	if pointer == "" {
		return nil, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		parts[i] = strings.ReplaceAll(p, "~0", "~")
	}
	return parts, true
}

func offsetOf(src []byte, pointer string) (int, bool) {
	want, ok := parsePointer(pointer)
	if !ok {
		return 0, false
	}
	if len(want) == 0 {
		return 0, true
	}
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	return seek(dec, src, want, 0)
}

// seek descends the value the decoder is positioned at, following want. valStart
// is the offset of that value within src.
func seek(dec *json.Decoder, src []byte, want []string, valStart int) (int, bool) {
	if len(want) == 0 {
		return valStart, true
	}
	tok, err := dec.Token()
	if err != nil {
		return 0, false
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return 0, false
	}
	switch delim {
	case '{':
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return 0, false
			}
			key, _ := keyTok.(string)
			childStart := valueStart(src, int(dec.InputOffset()))
			if key == want[0] {
				return seek(dec, src, want[1:], childStart)
			}
			if err := skipValue(dec); err != nil {
				return 0, false
			}
		}
		return 0, false
	case '[':
		for i := 0; dec.More(); i++ {
			childStart := valueStart(src, int(dec.InputOffset()))
			if strconv.Itoa(i) == want[0] {
				return seek(dec, src, want[1:], childStart)
			}
			if err := skipValue(dec); err != nil {
				return 0, false
			}
		}
		return 0, false
	default:
		return 0, false
	}
}

// skipValue consumes exactly one complete value, including a nested composite.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if _, ok := tok.(json.Delim); !ok {
		return nil
	}
	for depth := 1; depth > 0; {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// valueStart advances past the separator and whitespace that follow a key or a
// comma, landing on the first byte of the value itself.
func valueStart(src []byte, from int) int {
	i := from
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r', ':', ',':
			i++
		default:
			return i
		}
	}
	return from
}

func lineColumn(src []byte, off int) (line, column int) {
	if off < 0 || off > len(src) {
		return 0, 0
	}
	line = 1 + bytes.Count(src[:off], []byte{'\n'})
	lineStart := bytes.LastIndexByte(src[:off], '\n') + 1
	return line, off - lineStart + 1
}
