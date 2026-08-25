package buildinfo_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/cli"
	"github.com/stxkxs/agentfs/internal/report"
)

// snakeCase is the spelling every member name in a one-shot result takes.
var snakeCase = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// A consumer reads the envelope, not the command that produced it. A payload
// that spells its members differently from the frame around them makes the
// convention a property of which subcommand ran, which is a thing a consumer
// has to learn one command at a time.
func TestVersionEnvelopeIsSnakeCaseThroughout(t *testing.T) {
	t.Parallel()

	var out, errs strings.Builder
	code := cli.Run(t.Context(), cli.Env{
		Args:   []string{"agentfs", "version", "--format", "json"},
		Stdout: &out,
		Stderr: &errs,
	})
	if code != report.CodeOK {
		t.Fatalf("agentfs version --format json exited %v: %s", code, errs.String())
	}

	var envelope any
	if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}

	names := members(envelope, nil)
	// The frame carries schema, kind, version, exit and data; the payload
	// carries the five facts of an identity. A payload that stopped being
	// emitted would leave the loop below asserting nothing.
	if len(names) < 10 {
		t.Fatalf("the envelope carries %d members, %v", len(names), names)
	}
	for _, name := range names {
		if !snakeCase.MatchString(name) {
			t.Errorf("the version envelope carries the member %q, which is not snake case", name)
		}
	}
}

// members collects every object member name in a decoded document, at any depth.
func members(v any, into []string) []string {
	switch node := v.(type) {
	case map[string]any:
		for name, child := range node {
			into = append(into, name)
			into = members(child, into)
		}
	case []any:
		for _, child := range node {
			into = members(child, into)
		}
	}
	return into
}
