package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// renderStateSchema writes the workspace contract.
func renderStateSchema(b *strings.Builder, _ module) error {
	title(b, "The state contract",
		"An agent declares what it is doing by writing a JSON document into its workspace directory. "+
			"This page is the contract, rendered from the table the decoder types members against, so "+
			"a member described here and a member the decoder accepts are the same member.")

	para(b, "`agentfs schema` prints the contract as a JSON Schema 2020-12 document. Its `$id` is "+
		code(agentstate.SchemaID)+", which the release workflow serves from `schema/` on a release "+
		"tag — until one is pushed, the identifier names the document without resolving to it, and "+
		"`agentfs schema` is where to fetch it. `agentfs validate <directory>` checks a workspace "+
		"against the same rules.")

	section(b, "Where the document lives")
	para(b, "Each immediate subdirectory of the workspace is one agent. The agent's state document is "+
		"the first of these names it holds:")

	t := newTable("Name", "Status")
	for i, name := range agentstate.StateFiles() {
		status := "canonical"
		if i > 0 {
			status = "accepted, with an info diagnostic naming " + code(agentstate.StateFile)
		}
		t.row(code(name), status)
	}
	if err := t.write(b); err != nil {
		return err
	}

	para(b, "A directory holding "+codeList(agentstate.SignalDirs)+" is recognized as an agent workspace "+
		"even when it declares no state document, which is how an agent that writes only logs is seen.")

	section(b, "Members")
	para(b, "A member the contract does not define is preserved and reported with an info diagnostic, so "+
		"an integrator's own fields survive a round trip through agentfs without becoming contractual.")

	rules := agentstate.Rules()
	t = newTable("Member", "Type", "Required", "Meaning")
	for _, r := range rules {
		t.row(code(r.Member), memberType(r), yesNo(r.Required), r.Summary)
	}
	if err := t.write(b); err != nil {
		return err
	}

	if err := describeConstraints(b, rules); err != nil {
		return err
	}

	section(b, "The status vocabulary")
	para(b, "`status` is matched exactly against a closed set, after trimming and case folding. A value "+
		"outside it raises a diagnostic naming the accepted set. Matching is never by substring, so "+
		"`\"not running\"` is not running and `\"unfailed\"` is not a failure.")

	t = newTable("Value", "Meaning")
	t.row(code("running"), "The agent holds work and is progressing.")
	t.row(code("idle"), "The agent holds no work and is available.")
	t.row(code("blocked"), "The agent holds work it cannot progress without an external input.")
	t.row(code("error"), "The agent stopped because it could not continue.")
	t.row(code("done"), "The agent completed its work.")
	if err := t.write(b); err != nil {
		return err
	}
	if err := coverVocabulary(t); err != nil {
		return err
	}

	section(b, "Status and problem are independent")
	para(b, "`problem` describes a fault. It does not set `status`, and `status` does not imply it. "+
		"A reader that derived one from the other could not express either of these:")
	fence(b, "json", `{"schema": "agentfs/v1", "status": "done",    "problem": "The first pass timed out and was retried."}
{"schema": "agentfs/v1", "status": "running", "problem": "A transient 503; retrying."}`)
	para(b, "A `status` of `error` with no `problem` leaves a reader nothing to act on, and raises a warning.")

	section(b, "The compatibility profile")
	para(b, "A document declaring no `schema` member is read under the compatibility profile: the "+
		"spellings below resolve, each with an info diagnostic naming the canonical form. Under "+
		code(agentstate.SchemaVersion)+" only the vocabulary above is accepted.")

	t = newTable("Spelling", "Resolves to")
	for _, alias := range agentstate.Aliases() {
		t.row(code(alias.Spelling), code(alias.Status.String()))
	}
	if err := t.write(b); err != nil {
		return err
	}

	section(b, "A complete document")
	fence(b, "json", `{
  "schema": "agentfs/v1",
  "status": "running",
  "agent": "researcher",
  "task": "Retrieve and rank sources",
  "step": 3,
  "steps_total": 8,
  "model": "claude-opus-5",
  "run_id": "eval-2026-04-08-a",
  "heartbeat_seconds": 30,
  "started_at": "2026-04-08T12:00:00Z",
  "updated_at": "2026-04-08T13:00:00Z",
  "labels": {
    "queue": "batch"
  }
}`)

	section(b, "Writing it")
	para(b, "Write the document atomically: to a temporary file in the same directory, flushed, then "+
		"renamed over the target. A reader opens the file at moments the writer does not choose, and a "+
		"writer that truncates the target and streams into it hands that reader bytes that are not "+
		"JSON. agentfs holds the last complete reading rather than flickering, and reports the torn "+
		"read — but the fix belongs to the writer.")
	para(b, "Reference writers to vendor are in [contrib](../../contrib): one file each for Python and "+
		"TypeScript, with no dependencies.")
	return nil
}

// memberType renders a rule's JSON type for the table.
func memberType(r agentstate.Rule) string {
	switch r.Type {
	case "integer|string":
		return code("integer") + " or " + code("string")
	case "string":
		if r.Format != "" {
			return code("string") + " (" + r.Format + ")"
		}
		return code("string")
	default:
		return code(r.Type)
	}
}

// describeConstraints writes the constraints the member table cannot carry in a
// cell: closed vocabularies, length caps, and compatibility names.
func describeConstraints(b *strings.Builder, rules []agentstate.Rule) error {
	var enums, caps, compat [][2]string
	for _, r := range rules {
		if len(r.Enum) > 0 {
			enums = append(enums, [2]string{r.Member, codeList(r.Enum)})
		}
		if r.MaxRunes > 0 {
			caps = append(caps, [2]string{r.Member, strconv.Itoa(r.MaxRunes) + " runes"})
		}
		if len(r.Compat) > 0 {
			compat = append(compat, [2]string{r.Member, codeList(r.Compat)})
		}
	}

	if len(caps) > 0 {
		subsection(b, "Length ceilings")
		para(b, "A state document is a status declaration rather than a log. A member longer than its "+
			"ceiling is truncated for display and reported.")
		t := newTable("Member", "Ceiling")
		for _, c := range caps {
			t.row(code(c[0]), c[1])
		}
		if err := t.write(b); err != nil {
			return err
		}
	}

	if len(compat) > 0 {
		subsection(b, "Compatibility member names")
		para(b, "Read under the compatibility profile only, each with an info diagnostic naming the "+
			"canonical member.")
		t := newTable("Member", "Also accepted as")
		for _, c := range compat {
			t.row(code(c[0]), c[1])
		}
		if err := t.write(b); err != nil {
			return err
		}
	}

	if len(enums) == 0 {
		return fmt.Errorf("no member declares a closed vocabulary, so the contract has no schema member")
	}
	return nil
}

// coverVocabulary reports a status the page describes nowhere, so a value
// cannot be added to the vocabulary and reach no page.
func coverVocabulary(t *table) error {
	described := make(map[string]bool, len(t.rows))
	for _, row := range t.rows {
		described[strings.Trim(row[0], "`")] = true
	}
	for _, v := range agentstate.Vocabulary() {
		if !described[v] {
			return fmt.Errorf("the status %q is in the vocabulary and this page describes it nowhere", v)
		}
	}
	return nil
}
