package diag

import "testing"

// A pointer is built from names the document chose, and read back by the
// unescaping this package applies. Escaping is contracted to be that reader's
// inverse: whatever a name holds, the token it becomes decodes to the name
// again, or the pointer addresses a member other than the one the finding is
// about.
func TestEscapeTokenIsTheInverseOfPointerParsing(t *testing.T) {
	t.Parallel()
	names := []string{
		"",
		"plain",
		"a/b",
		"c~d",
		"c~0d",
		"c~1d",
		"~",
		"/",
		"~/~",
		"a~0b/c~1d",
		"//~~",
	}
	for _, name := range names {
		tokens, ok := parsePointer("/" + EscapeToken(name))
		if !ok {
			t.Errorf("the pointer built from %q is not a pointer", name)
			continue
		}
		if len(tokens) != 1 {
			t.Errorf("%q escaped to %d reference tokens, want one: %q", name, len(tokens), tokens)
			continue
		}
		if tokens[0] != name {
			t.Errorf("%q escaped and read back as %q", name, tokens[0])
		}
	}
}
