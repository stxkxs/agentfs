package fileview

import (
	"strings"
	"unicode"
)

// Mask stands in for a redacted value. It is a fixed width regardless of what
// it replaced, because a mask whose length tracked the secret would leak its
// length.
const Mask = "••••••"

// Redact replaces the value of every JSON member whose name matches one of
// keys, comparing without regard to case, underscores or hyphens — a document
// naming `api_key`, `apiKey` and `API-KEY` means the same member three ways.
//
// It operates on the line's text before the line is lexed, so the spans a
// renderer receives index the redacted text rather than the original. A
// redaction applied afterwards would leave the spans addressing bytes that are
// no longer there.
//
// Only a quoted string value is masked. A number, a boolean or a nested object
// is left alone: masking those would change the document's shape on screen, and
// a credential is a string.
func Redact(line string, keys []string) string {
	if len(keys) == 0 || line == "" {
		return line
	}

	var b strings.Builder
	b.Grow(len(line))
	rest := line

	for {
		key, before, after, ok := nextMember(rest)
		if !ok {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(before)

		gap, value, tail, quoted := stringValue(after)
		b.WriteString(gap)
		switch {
		case !quoted:
			// A number, a boolean or a nested value is left as it was: masking
			// it would change the document's shape on screen, and a credential
			// is a string.
		case matches(key, keys):
			b.WriteString(`"` + Mask + `"`)
		default:
			b.WriteString(value)
		}
		rest = tail
	}
}

// nextMember finds the next `"name":` in s, returning the member name, the text
// up to and including the colon, and the text after it.
func nextMember(s string) (key, before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		end := closingQuote(s, i)
		if end < 0 {
			return "", "", "", false
		}
		rest := s[end+1:]
		trimmed := strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(trimmed, ":") {
			i = end
			continue
		}
		consumed := len(rest) - len(trimmed) + 1
		return s[i+1 : end], s[:end+1+consumed], s[end+1+consumed:], true
	}
	return "", "", "", false
}

// stringValue reads a quoted value at the head of s. It returns the whitespace
// before the value, the value including its quotes, what follows, and whether a
// quoted value was there at all. The gap is returned rather than skipped so a
// redacted line keeps the spacing of the one it replaced.
func stringValue(s string) (gap, value, tail string, ok bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	gap = s[:i]
	if i >= len(s) || s[i] != '"' {
		return gap, "", s[i:], false
	}
	end := closingQuote(s, i)
	if end < 0 {
		return gap, "", s[i:], false
	}
	return gap, s[i : end+1], s[end+1:], true
}

// closingQuote returns the index of the quote closing the one at start,
// honouring backslash escapes, or -1 when the string does not close.
func closingQuote(s string, start int) int {
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// matches reports whether a member name names one of the keys, ignoring case
// and the separators a name is spelled with.
func matches(member string, keys []string) bool {
	needle := fold(member)
	for _, k := range keys {
		if fold(k) == needle {
			return true
		}
	}
	return false
}

// fold reduces a member name to what identifies it: its letters and digits,
// lowercased.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
