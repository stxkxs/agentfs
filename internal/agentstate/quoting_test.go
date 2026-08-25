package agentstate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
)

// findCode returns the first diagnostic carrying code.
func findCode(ds []diag.Diagnostic, code diag.Code) (diag.Diagnostic, bool) {
	for _, d := range ds {
		if d.Code == code {
			return d, true
		}
	}
	return diag.Diagnostic{}, false
}

// statusFinding decodes a document declaring status and returns the vocabulary
// finding together with its message split at the quotation marks: what precedes
// the quoted run, the run itself, and what follows. A message carrying anything
// other than one quoted run has already lost the boundary between agentfs's
// words and the workspace's.
func statusFinding(t *testing.T, status string) (d diag.Diagnostic, before, run, after string) {
	t.Helper()

	value, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encoding the status: %v", err)
	}
	_, ds := agentstate.Decode("a/state.json", []byte(`{"schema":"agentfs/v1","status":`+string(value)+`}`), agentstate.Options{})

	d, ok := findCode(ds, diag.CodeStatusUnknown)
	if !ok {
		t.Fatalf("a status of %q raised %v, want AFS3002", status, codes(ds))
	}
	if n := strings.Count(d.Message, `"`); n != 2 {
		t.Fatalf("a status of %q produced a message carrying %d quotation marks: %s", status, n, d.Message)
	}
	i := strings.Index(d.Message, `"`)
	j := strings.LastIndex(d.Message, `"`)
	return d, d.Message[:i], d.Message[i+1 : j], d.Message[j+1:]
}

// A message is agentfs's sentence with the workspace's text quoted inside it.
// The words outside the quotation marks are agentfs's own, and no document
// reaches them.
//
// Two mechanisms would let one. [textx.Sanitize] consumes an escape sequence
// forward to the terminator that ends it, so a value ending in an unterminated
// sequence consumes the words spliced after it. And the quotation mark delimits
// the run, so a value carrying one closes agentfs's quotation and continues in
// agentfs's voice — an operator reads the result as a sentence agentfs wrote.
func TestAQuotedValueCannotReachAgentfsOwnWords(t *testing.T) {
	t.Parallel()

	const esc = "\x1b"
	hostile := []string{
		esc + "]",                               // an unterminated string sequence
		"deploying to prod" + esc + "]",         // a status of the workspace's choosing, then one
		esc + "[",                               // an unterminated control sequence
		`running" is fine. The status "running`, // a verdict in agentfs's voice
	}

	_, wantBefore, run, wantAfter := statusFinding(t, "x")
	if run != "x" || wantBefore == "" || wantAfter == "" {
		t.Fatalf("a status of %q is framed as %q %q %q", "x", wantBefore, run, wantAfter)
	}

	for _, status := range hostile {
		d, before, run, after := statusFinding(t, status)
		if before != wantBefore || after != wantAfter {
			t.Errorf("a status of %q framed the finding as %q…%q, want %q…%q",
				status, before, after, wantBefore, wantAfter)
		}
		if run == "" {
			t.Errorf("a status of %q is quoted as nothing, so the finding names no value", status)
		}
		if run != d.Value {
			t.Errorf("a status of %q is quoted as %q in the message and %q in the value", status, run, d.Value)
		}
	}
}

// The two halves of one finding are neutralized the same way, so a reader
// resolving it reads one text rather than two renderings of it. They are
// bounded separately, so a value past its own ceiling agrees with the message
// by its head.
func TestAQuotedValueAgreesWithTheMessageThatQuotesIt(t *testing.T) {
	t.Parallel()

	for _, runes := range []int{1, 100, 119, 120, 121, 200, 400} {
		status := strings.Repeat("x", runes)
		d, _, run, _ := statusFinding(t, status)
		if run != status {
			t.Errorf("a status of %d runes is quoted as %q", runes, run)
		}
		if head := strings.TrimSuffix(d.Value, "…"); !strings.HasPrefix(run, head) {
			t.Errorf("a status of %d runes is quoted as %q in the message and %q in the value",
				runes, run, d.Value)
		}
	}
}
