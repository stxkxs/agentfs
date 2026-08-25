package fileview

import "strings"

// The markers that open and close a YAML document.
const (
	yamlStartMarker = "---"
	yamlEndMarker   = "..."
)

// lexYAML returns spans for one line of a YAML document.
//
// The lexer is per line and structure-free: it recognizes the block mapping
// key, the sequence dash, flow punctuation and scalars, and stops at a comment.
// Indentation carries YAML's structure and a single line does not know its own
// context, so a role that would require the enclosing block is not assigned
// rather than guessed.
func lexYAML(line string) []Span {
	i := skipSpace(line, 0)
	if i >= len(line) || line[i] == '#' {
		return nil
	}
	spans := make([]Span, 0, 8)
	if end, ok := yamlMarker(line, i); ok {
		return addSpan(spans, i, end, RolePunct)
	}
	for i < len(line) && line[i] == '-' && (i+1 == len(line) || line[i+1] == ' ') {
		spans = addSpan(spans, i, i+1, RolePunct)
		i = skipSpace(line, i+1)
	}
	if i >= len(line) {
		return spans
	}
	if keyEnd, ok := scanYAMLKey(line, i); ok {
		spans = addSpan(spans, i, keyEnd, RoleKey)
		spans = addSpan(spans, keyEnd, keyEnd+1, RolePunct)
		i = keyEnd + 1
	}
	return lexYAMLValue(line, i, spans)
}

// yamlMarker returns the end of a document marker at i, and reports whether one
// is there.
func yamlMarker(s string, i int) (end int, ok bool) {
	rest := strings.TrimRight(s[i:], " \t")
	if rest == yamlStartMarker || rest == yamlEndMarker {
		return i + len(yamlStartMarker), true
	}
	return i, false
}

// scanYAMLKey returns the index of the colon that ends the mapping key
// beginning at i, and reports whether the line opens a mapping at all. YAML
// separates a key from its value with a colon followed by whitespace or the end
// of the line, so a colon inside a scalar does not end a key.
func scanYAMLKey(s string, i int) (keyEnd int, ok bool) {
	j := i
	if s[j] == '"' || s[j] == '\'' {
		j = scanQuoted(s, j)
	}
	for j < len(s) {
		if s[j] == ':' && (j+1 == len(s) || s[j+1] == ' ' || s[j+1] == '\t') {
			return j, j > i
		}
		if s[j] == '#' && j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
			return i, false
		}
		j++
	}
	return i, false
}

// lexYAMLValue appends the spans of the value beginning at i.
func lexYAMLValue(line string, i int, spans []Span) []Span {
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case startsYAMLComment(line, i):
			return spans
		case c == '"' || c == '\'':
			end := scanQuoted(line, i)
			spans = addSpan(spans, i, end, RoleString)
			i = end
		case isYAMLFlowPunct(c):
			spans = addSpan(spans, i, i+1, RolePunct)
			i++
		case startsYAMLNumber(line, i):
			end := scanScalarWord(line, i)
			spans = addSpan(spans, i, end, RoleNumber)
			i = end
		default:
			end := scanScalarWord(line, i)
			if role, ok := yamlScalarRole(line[i:end]); ok {
				spans = addSpan(spans, i, end, role)
			}
			i = end
		}
	}
	return spans
}

// startsYAMLComment reports whether a comment begins at i. YAML opens a comment
// with a hash that follows whitespace, so a hash inside a scalar is content.
func startsYAMLComment(s string, i int) bool {
	return s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t')
}

// startsYAMLNumber reports whether a numeric scalar begins at i.
func startsYAMLNumber(s string, i int) bool {
	if isDigit(s[i]) {
		return true
	}
	return s[i] == '-' && i+1 < len(s) && isDigit(s[i+1])
}

func isYAMLFlowPunct(c byte) bool {
	switch c {
	case '[', ']', '{', '}', ',':
		return true
	default:
		return false
	}
}

func isYAMLStop(c byte) bool {
	return c == ' ' || c == '\t' || isYAMLFlowPunct(c)
}

// scanScalarWord returns the end of the bare scalar beginning at i, which is
// the next flow delimiter or the end of the line. The callers reach it only at
// a byte that is not a delimiter, so the result is greater than i and the loop
// that calls it advances.
func scanScalarWord(s string, i int) int {
	j := i
	for j < len(s) && !isYAMLStop(s[j]) {
		j++
	}
	return j
}

// yamlScalarRole reports the role of a bare scalar, and false for one whose
// meaning depends on a schema the line does not carry.
func yamlScalarRole(word string) (role Role, ok bool) {
	if word == "~" {
		return RoleNull, true
	}
	if len(word) > 5 {
		return RolePlain, false
	}
	role, ok = yamlScalars[strings.ToLower(word)]
	return role, ok
}

var yamlScalars = map[string]Role{
	"true":  RoleBool,
	"false": RoleBool,
	"yes":   RoleBool,
	"no":    RoleBool,
	"on":    RoleBool,
	"off":   RoleBool,
	"null":  RoleNull,
}
