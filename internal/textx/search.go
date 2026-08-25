package textx

import (
	"unicode"
	"unicode/utf8"
)

// Span is a half-open byte range within a string.
type Span struct {
	Start int
	End   int
}

// Len returns the span's length in bytes.
func (s Span) Len() int { return s.End - s.Start }

// FindAll returns every non-overlapping case-insensitive occurrence of sub in
// s, as byte spans valid in s itself.
//
// The spans are found by folding rune-by-rune against s rather than by indexing
// a lowercased copy. A lowercased copy is a different string: case folding
// changes byte length for runes such as U+0130 LATIN CAPITAL LETTER I WITH DOT
// ABOVE, so an offset taken from the copy addresses the wrong byte of the
// original and can split a rune. For the same reason a span's length is not
// necessarily len(sub).
func FindAll(s, sub string) []Span {
	if sub == "" || s == "" {
		return nil
	}
	var out []Span
	for i := 0; i < len(s); {
		if end, ok := matchAt(s, i, sub); ok {
			out = append(out, Span{Start: i, End: end})
			if end > i {
				i = end
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return out
}

// Contains reports whether sub occurs in s under case folding.
func Contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i < len(s); {
		if _, ok := matchAt(s, i, sub); ok {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return false
}

// matchAt reports whether sub occurs at byte offset i of s under case folding,
// and returns the exclusive end offset of the match within s.
func matchAt(s string, i int, sub string) (int, bool) {
	si, ni := i, 0
	for ni < len(sub) {
		if si >= len(s) {
			return 0, false
		}
		sr, sw := utf8.DecodeRuneInString(s[si:])
		nr, nw := utf8.DecodeRuneInString(sub[ni:])
		if !equalFoldRune(sr, nr) {
			return 0, false
		}
		si += sw
		ni += nw
	}
	return si, true
}

// equalFoldRune reports whether a and b are equal under Unicode simple case
// folding, matching the rule [strings.EqualFold] applies.
func equalFoldRune(a, b rune) bool {
	if a == b {
		return true
	}
	if b < a {
		a, b = b, a
	}
	if b < utf8.RuneSelf {
		return 'A' <= a && a <= 'Z' && b == a+'a'-'A'
	}
	r := unicode.SimpleFold(a)
	for r != a && r < b {
		r = unicode.SimpleFold(r)
	}
	return r == b
}
