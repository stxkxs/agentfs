package fileview

import "strings"

// lexLog returns spans for one line of a log.
//
// A log line has no grammar, so the lexer marks only what a reader scans for:
// the severity word, the key of a logfmt pair, quoted text, and the digit-led
// tokens a timestamp and a duration are made of. Everything else keeps no role,
// which leaves the message itself unstyled and the fields around it legible.
func lexLog(line string) []Span {
	spans := make([]Span, 0, 8)
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '"':
			end := scanQuoted(line, i)
			spans = addSpan(spans, i, end, RoleString)
			i = end
		case isDigit(c):
			end := scanLogNumber(line, i)
			spans = addSpan(spans, i, end, RoleNumber)
			i = end
		case isLetter(c) || c == '_':
			end := scanLogToken(line, i)
			spans = appendLogWord(spans, line, i, end)
			i = end
		default:
			i += runeLen(line, i)
		}
	}
	return spans
}

// appendLogWord gives the word between start and end its role: the key of a
// logfmt pair, a severity, a literal, or none.
func appendLogWord(spans []Span, line string, start, end int) []Span {
	if end < len(line) && line[end] == '=' {
		return addSpan(spans, start, end, RoleKey)
	}
	if role, ok := logWordRole(line[start:end]); ok {
		return addSpan(spans, start, end, role)
	}
	return spans
}

// scanLogToken returns the end of the unquoted token beginning at i. The token
// bytes include the separators of a timestamp and of a dotted name, so
// 2024-01-02T03:04:05Z and request.duration each scan as one token.
func scanLogToken(s string, i int) int {
	j := i
	for j < len(s) && isLogTokenByte(s[j]) {
		j++
	}
	return j
}

// scanLogNumber returns the end of the digit-led token beginning at i. It keeps
// the separators of a clock time, so a timestamp is one span rather than one
// per field, and it releases a trailing colon, which belongs to the label after
// it rather than to the number.
func scanLogNumber(s string, i int) int {
	j := i
	for j < len(s) && (isLogTokenByte(s[j]) || s[j] == ':') {
		j++
	}
	for j > i && s[j-1] == ':' {
		j--
	}
	return j
}

func isLogTokenByte(c byte) bool {
	if isLetter(c) || isDigit(c) {
		return true
	}
	switch c {
	case '_', '-', '.', '+', '/', '@':
		return true
	default:
		return false
	}
}

// logWordRole reports the role of a bare word, and false for one that is part
// of the message rather than of the record's frame.
func logWordRole(word string) (role Role, ok bool) {
	if len(word) > 8 {
		return RolePlain, false
	}
	lower := strings.ToLower(word)
	if role, ok = logLevels[lower]; ok {
		return role, true
	}
	role, ok = logLiterals[lower]
	return role, ok
}

var logLevels = map[string]Role{
	"trace":   RoleTrace,
	"trc":     RoleTrace,
	"debug":   RoleDebug,
	"dbg":     RoleDebug,
	"info":    RoleInfo,
	"inf":     RoleInfo,
	"notice":  RoleInfo,
	"warn":    RoleWarn,
	"warning": RoleWarn,
	"wrn":     RoleWarn,
	"error":   RoleError,
	"err":     RoleError,
	"fatal":   RoleError,
	"panic":   RoleError,
	"crit":    RoleError,
}

var logLiterals = map[string]Role{
	"true":  RoleBool,
	"false": RoleBool,
	"null":  RoleNull,
	"nil":   RoleNull,
}
