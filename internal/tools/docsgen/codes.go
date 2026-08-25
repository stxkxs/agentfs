package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/report"
)

// renderExitCodes writes the exit-code contract.
func renderExitCodes(b *strings.Builder, _ module) error {
	title(b, "Exit codes",
		"Every agentfs command terminates with one of these. A caller branches on the number rather "+
			"than on the output, and a one-shot command repeats it inside its JSON envelope so a "+
			"consumer reading a captured result reaches the same verdict as one that watched the process.")

	t := newTable("Code", "Name", "Meaning")
	for _, c := range report.Codes() {
		t.row(code(strconv.Itoa(int(c.Code))), code(c.Name), c.Summary)
	}
	if err := t.write(b); err != nil {
		return err
	}

	section(b, "Using them as a gate")
	para(b, "`validate` separates \"the workspace is conformant\" from \"the workspace could not be read\", "+
		"which is what lets it run unattended: a findings status is a result to act on, and a path "+
		"status is a problem with the invocation.")
	fence(b, "sh", `agentfs validate ./workspace
case $? in
  0) echo "conformant" ;;
  1) echo "findings — see the diagnostics above" ;;
  2) echo "the invocation was malformed" ;;
  3) echo "the workspace could not be read" ;;
esac`)
	return nil
}

// diagnosticFamily names a code range and what it covers.
type diagnosticFamily struct {
	// prefix is the digit following AFS that opens the family.
	prefix string
	// heading names the family.
	heading string
	// summary states what the family covers.
	summary string
}

var diagnosticFamilies = []diagnosticFamily{
	{"AFS1", "1xxx — the document",
		"Whether the bytes are a state document at all: well-formedness, the declared contract " +
			"version, the members the contract does not define, and the size ceiling."},
	{"AFS2", "2xxx — member types",
		"Whether a member holds the JSON type the contract declares for it."},
	{"AFS3", "3xxx — member meaning",
		"Whether a member's value means anything: a status inside the vocabulary, a timestamp that " +
			"names an instant, an ordinal inside its total."},
	{"AFS4", "4xxx — observation",
		"What reading the document produced, as distinct from what it contains: unreadable, stale, " +
			"or observed mid-write."},
	{"AFS5", "5xxx — ceilings",
		"A limit agentfs reached. These describe what agentfs could not hold or observe, so they " +
			"name a condition of the observer rather than a defect in the workspace."},
}

// renderDiagnostics writes the diagnostic registry.
func renderDiagnostics(b *strings.Builder, _ module) error {
	title(b, "Diagnostics",
		"Every finding agentfs reports carries one of these codes. A code is permanent: it is retired "+
			"rather than reused, so a consumer that suppresses one never has it come to mean something "+
			"else. Branch on the code and the JSON Pointer; the message and the hint are prose for a "+
			"person and carry no contract.")

	para(b, "The severity column is the level a code is raised at unless a flag moves it. A document "+
		"carrying an error is not usable as state; a warning or an info document is usable and annotated.")

	para(b, "A code marked semantic is one agentfs raises from what it observed rather than from the "+
		"document's shape, so no JSON Schema validator can produce it. The conformance suite excludes "+
		"exactly these when it checks that the schema and the decoder agree.")

	codes := diag.Codes()
	for _, family := range diagnosticFamilies {
		var rows []diag.CodeInfo
		for _, info := range codes {
			if strings.HasPrefix(string(info.Code), family.prefix) {
				rows = append(rows, info)
			}
		}
		if len(rows) == 0 {
			return fmt.Errorf("no code carries the prefix %q, so the family %q describes nothing",
				family.prefix, family.heading)
		}

		section(b, family.heading)
		para(b, family.summary)

		t := newTable("Code", "Severity", "Semantic", "Condition")
		for _, info := range rows {
			t.row(code(string(info.Code)), info.Severity.String(), yesNo(info.Semantic), info.Summary)
		}
		if err := t.write(b); err != nil {
			return err
		}
	}

	if err := coverEveryCode(codes); err != nil {
		return err
	}

	section(b, "What a diagnostic carries")
	fence(b, "json", `{
  "code": "AFS3002",
  "severity": "error",
  "path": "agent-broken/state.json",
  "pointer": "/status",
  "line": 1,
  "column": 33,
  "message": "The status \"not running\" is not in the contract vocabulary.",
  "hint": "Use one of: running, idle, blocked, error, done. Matching is exact, not by substring.",
  "value": "not running"
}`)
	para(b, "`pointer` is an RFC 6901 JSON Pointer to the offending member, and `line` and `column` "+
		"resolve it within the document. A hint is actionable: applying it produces a document that "+
		"validates clean, and the conformance suite asserts that for every hint-bearing case.")
	return nil
}

// coverEveryCode reports a code no family claims, so a code cannot be added to
// the registry and reach no page.
func coverEveryCode(codes []diag.CodeInfo) error {
	for _, info := range codes {
		claimed := false
		for _, family := range diagnosticFamilies {
			if strings.HasPrefix(string(info.Code), family.prefix) {
				claimed = true
				break
			}
		}
		if !claimed {
			return fmt.Errorf("%s falls in no documented family", info.Code)
		}
	}
	return nil
}
