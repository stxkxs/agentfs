package fileview

import "unicode/utf8"

// Role names the syntactic part a byte range of a line plays.
type Role int

// The roles a lexer assigns. RolePlain is the absence of a role: a byte range
// no lexer claimed carries it implicitly and gets no span, and a lexer emits
// RolePlain only to claim a whole line it could not tokenize.
const (
	RolePlain Role = iota
	RoleKey
	RoleString
	RoleNumber
	RoleBool
	RoleNull
	RolePunct
	RoleTrace
	RoleDebug
	RoleInfo
	RoleWarn
	RoleError
)

// plainName is the name both an unstyled role and an unstructured kind carry:
// content the viewer displays without claiming anything about its shape.
const plainName = "plain"

// String returns the lowercase name of the role.
func (r Role) String() string {
	switch r {
	case RolePlain:
		return plainName
	case RoleKey:
		return "key"
	case RoleString:
		return "string"
	case RoleNumber:
		return "number"
	case RoleBool:
		return "bool"
	case RoleNull:
		return "null"
	case RolePunct:
		return "punct"
	case RoleTrace:
		return "trace"
	case RoleDebug:
		return "debug"
	case RoleInfo:
		return "info"
	case RoleWarn:
		return "warn"
	case RoleError:
		return "error"
	default:
		return plainName
	}
}

// Span marks a byte range of a line as a syntactic role. Start is inclusive and
// End exclusive, both indexing the line's sanitized text.
type Span struct {
	Start, End int
	Role       Role
}

// Len returns the span's length in bytes.
func (s Span) Len() int { return s.End - s.Start }

// Highlight returns the spans for one line of the given kind.
//
// The result is ascending, non-overlapping, within the bounds of line, aligned
// to rune boundaries, and no longer than [MaxSpans], for every kind and every
// input. A renderer can therefore slice line by a span without checking it,
// which is the property that keeps a lexer bug from becoming a panic on
// workspace bytes, and its per-line cost is bounded by the ceiling rather than
// by the line's length.
func Highlight(kind Kind, line string) []Span {
	if line == "" {
		return nil
	}
	var spans []Span
	switch kind {
	case KindJSON, KindNDJSON:
		spans = lexJSON(line)
	case KindYAML:
		spans = lexYAML(line)
	case KindLog:
		spans = lexLog(line)
	case KindPlain, KindBinary:
		return nil
	default:
		return nil
	}
	return clamp(line, spans)
}

// clamp drops every span that would let a caller slice line incorrectly: out of
// bounds, empty, out of order, overlapping its predecessor, or landing inside a
// rune.
func clamp(line string, spans []Span) []Span {
	out := spans[:0]
	prev := 0
	for _, s := range spans {
		if s.Start < prev || s.End <= s.Start || s.End > len(line) {
			continue
		}
		if !utf8.RuneStart(line[s.Start]) {
			continue
		}
		if s.End < len(line) && !utf8.RuneStart(line[s.End]) {
			continue
		}
		out = append(out, s)
		prev = s.End
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// addSpan appends a span unless it is empty or the line is already at the span
// ceiling. Every lexer reaches its spans through here, so the ceiling holds for
// each of them without any of them carrying a counter.
func addSpan(spans []Span, start, end int, role Role) []Span {
	if end <= start || len(spans) >= MaxSpans {
		return spans
	}
	return append(spans, Span{Start: start, End: end, Role: role})
}

// plainSpan is the whole line under no syntactic role, which is what a lexer
// returns for a line it cannot tokenize.
func plainSpan(line string) []Span {
	return []Span{{Start: 0, End: len(line), Role: RolePlain}}
}

// runeLen returns the width in bytes of the rune at i, and one byte for a byte
// that begins no valid rune.
func runeLen(s string, i int) int {
	_, n := utf8.DecodeRuneInString(s[i:])
	return n
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// skipSpace returns the index of the first byte at or after i that is not a
// space or a tab.
func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// scanQuoted returns the end of the quoted scalar beginning at i. An
// unterminated quote runs to the end of the line, which is the shape a line
// read while it is being written has.
func scanQuoted(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); {
		switch {
		case s[j] == '\\' && q == '"':
			j += 2
		case s[j] == q:
			return j + 1
		default:
			j++
		}
	}
	return len(s)
}
