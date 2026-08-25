package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stxkxs/agentfs/internal/report"
)

// A JSON encoder escapes the C0 controls, so an ESC in a payload is inert. It
// does not escape the bidirectional and zero-width format characters, and a
// consumer printing a member of this envelope into a terminal is handed a
// right-to-left override that reverses everything after it.
func TestEnvelopeEscapesTheRunesATerminalActsOn(t *testing.T) {
	t.Parallel()

	e := report.NewEnvelope(report.KindScan, "test", "/ws", report.CodeOK)
	e.Data = map[string]string{
		"bidi":      "gpj.exe" + string(rune(0x202E)),
		"zeroWidth": "a" + string(rune(0x200B)) + "b",
		"escape":    "a" + string(rune(0x1B)) + "b",
		"tag":       "a" + string(rune(0xE0041)) + "b",
		"innocuous": "日本語 · plain",
	}

	var out strings.Builder
	if err := e.WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, r := range []rune{0x202E, 0x200B, 0x1B, 0xE0041} {
		if strings.ContainsRune(got, r) {
			t.Errorf("the envelope carries U+%04X raw:\n%s", r, got)
		}
	}
	// Escaping rather than replacing keeps the value recoverable, so a
	// consumer that decodes the JSON still receives what the workspace wrote.
	for _, want := range []string{"\\u202e", "\\u200b", "\\u001b", "\\udb40\\udc41"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("the envelope does not carry %s, so the value is not recoverable:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "日本語 · plain") {
		t.Errorf("ordinary text was escaped:\n%s", got)
	}

	// An escape that is not JSON would make a consumer fail to parse the
	// envelope rather than fail to render one rune of it.
	var back struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("the escaped envelope is not JSON: %v\n%s", err, got)
	}
	if back.Data["bidi"] != "gpj.exe"+string(rune(0x202E)) {
		t.Errorf("the decoded value is %q, want the rune the workspace wrote", back.Data["bidi"])
	}
	if back.Data["tag"] != "a"+string(rune(0xE0041))+"b" {
		t.Errorf("the decoded supplementary-plane value is %q", back.Data["tag"])
	}
}

func TestEscapingLeavesAnInnocuousEnvelopeUntouched(t *testing.T) {
	t.Parallel()

	e := report.NewEnvelope(report.KindValidate, "test", "/ws", report.CodeOK)
	e.Data = map[string]int{"documents": 3}

	var first, second strings.Builder
	if err := e.WriteJSON(&first); err != nil {
		t.Fatal(err)
	}
	if err := e.WriteJSON(&second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("encoding the same envelope twice produced different bytes")
	}
	if strings.Contains(first.String(), `\u`) {
		t.Errorf("an envelope with nothing to escape carries an escape:\n%s", first.String())
	}
}
