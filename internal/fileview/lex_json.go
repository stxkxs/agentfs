package fileview

// lexJSON returns spans for one line of a JSON document.
//
// A state document is read while an agent is writing it, so a line that does
// not tokenize is the expected case rather than an error: an unterminated
// string, a half-written number, a byte outside the grammar. Such a line yields
// one plain span, because a partial highlight of a partial line reads as
// corruption while unhighlighted text reads as text.
func lexJSON(line string) []Span {
	spans := make([]Span, 0, 8)
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '"':
			end, ok := scanJSONString(line, i)
			if !ok {
				return plainSpan(line)
			}
			role := RoleString
			if followedByColon(line, end) {
				role = RoleKey
			}
			spans = addSpan(spans, i, end, role)
			i = end
		case isJSONPunct(c):
			spans = addSpan(spans, i, i+1, RolePunct)
			i++
		case c == '-' || isDigit(c):
			end, ok := scanJSONNumber(line, i)
			if !ok {
				return plainSpan(line)
			}
			spans = addSpan(spans, i, end, RoleNumber)
			i = end
		default:
			end, role, ok := scanJSONLiteral(line, i)
			if !ok {
				return plainSpan(line)
			}
			spans = addSpan(spans, i, end, role)
			i = end
		}
	}
	return spans
}

func isJSONPunct(c byte) bool {
	switch c {
	case '{', '}', '[', ']', ':', ',':
		return true
	default:
		return false
	}
}

// scanJSONString returns the index just past the string beginning at i, and
// reports whether the string is terminated within the line. A JSON string
// cannot contain a raw newline, so an unterminated one means the line is
// incomplete.
func scanJSONString(s string, i int) (end int, ok bool) {
	for j := i + 1; j < len(s); {
		switch s[j] {
		case '\\':
			j += 2
		case '"':
			return j + 1, true
		default:
			j++
		}
	}
	return i, false
}

// followedByColon reports whether the next non-blank byte at or after i is a
// name separator, which is what makes the string before it a member name.
func followedByColon(s string, i int) bool {
	i = skipSpace(s, i)
	return i < len(s) && s[i] == ':'
}

// scanJSONNumber returns the index just past the number beginning at i, and
// reports whether the bytes form a complete JSON number.
func scanJSONNumber(s string, i int) (end int, ok bool) {
	j := i
	if j < len(s) && s[j] == '-' {
		j++
	}
	j, ok = scanDigits(s, j)
	if !ok {
		return i, false
	}
	if j < len(s) && s[j] == '.' {
		if j, ok = scanDigits(s, j+1); !ok {
			return i, false
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		j++
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j, ok = scanDigits(s, j); !ok {
			return i, false
		}
	}
	return j, true
}

// scanDigits returns the index just past the digit run at i, and reports
// whether the run holds at least one digit.
func scanDigits(s string, i int) (end int, ok bool) {
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	return j, j > i
}

// scanJSONLiteral returns the index just past the keyword beginning at i, its
// role, and whether a keyword is there at all.
func scanJSONLiteral(s string, i int) (end int, role Role, ok bool) {
	for _, lit := range jsonLiterals {
		if len(s)-i >= len(lit.word) && s[i:i+len(lit.word)] == lit.word {
			return i + len(lit.word), lit.role, true
		}
	}
	return i, RolePlain, false
}

var jsonLiterals = []struct {
	word string
	role Role
}{
	{"true", RoleBool},
	{"false", RoleBool},
	{"null", RoleNull},
}
