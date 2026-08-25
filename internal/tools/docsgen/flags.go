package main

import (
	"fmt"
	"strings"

	"github.com/stxkxs/agentfs/internal/config"
)

// flagSection groups the limit table by what a setting bounds.
//
// The unit vocabulary is closed, so every row lands in a section. A row whose
// unit has none stops the generator rather than dropping out of the reference.
type flagSection struct {
	// unit is the [config.Limit] unit the section collects.
	unit string
	// title is the heading.
	title string
	// lead states what this kind of setting bounds and how a value is written.
	lead string
}

var flagSections = []flagSection{
	{
		unit:  config.UnitPath,
		title: "Workspace",
		lead: "The directory agentfs reads. It is set by the positional argument every command that " +
			"reads a workspace requires, and there is no flag for it: one setting with two spellings " +
			"would let a command line disagree with itself. Every path in a result is relative to it.",
	},
	{
		unit:  config.UnitEnum,
		title: "Selections",
		lead: "One spelling from a closed set. A spelling outside the set exits `usage` and names the " +
			"permitted values.",
	},
	{
		unit:  config.UnitCount,
		title: "Counts",
		lead: "How many things agentfs reads, holds or registers. Each one turns a quantity the " +
			"workspace chooses — entries in a directory, events in a burst, nodes in a tree — into a " +
			"number agentfs states, which is what makes bounded work something a test can assert. " +
			"Crossing a count truncates the work and raises a diagnostic; see " +
			"[diagnostics.md](diagnostics.md).",
	},
	{
		unit:  config.UnitBytes,
		title: "Byte ceilings",
		lead: "How much agentfs reads from, or retains about, the workspace. A value is written as a " +
			"plain byte count; the defaults below are rendered with a binary suffix.",
	},
	{
		unit:  config.UnitDuration,
		title: "Time windows",
		lead: "How long agentfs waits, and how much clock difference it tolerates. A value is a Go " +
			"duration such as `2s`, `500ms` or `1m30s`.",
	},
	{
		unit:  config.UnitBool,
		title: "Switches",
		lead:  "A boolean. `-strict` sets it and `-strict=false` clears it.",
	},
	{
		unit:  config.UnitList,
		title: "Name lists",
		lead: "A comma-separated list of member names, in the same form on the command line and in the " +
			"environment. An entry is neither blank nor contains a comma, and an empty value clears " +
			"the list.",
	},
}

func renderFlags(b *strings.Builder, _ module) error {
	title(b, "Flags and environment",
		"Every ceiling agentfs runs under is one row of a single table. A setting absent from that "+
			"table cannot be set from the command line and cannot appear here, so this page is the "+
			"whole surface.")

	para(b, "Every flag has an environment variable: "+code(config.EnvPrefix)+
		" followed by the flag name in upper case with dashes as underscores. The environment is "+
		"applied first and flags second, so an explicit flag beats an exported variable — the more "+
		"specific statement of intent is the later one. A variable whose value does not parse is "+
		"ignored rather than fatal: an exported value is ambient, and refusing to start would let an "+
		"unrelated shell setting break the tool.")

	para(b, "`--format` selects the output form and is not part of the table; the values each "+
		"command renders are in [cli.md](cli.md).")

	para(b, "Settings are checked before a command runs, and every finding is reported from one "+
		"pass, so a configuration is corrected whole rather than one start at a time. A value the "+
		"program cannot run under exits `usage` naming the field, the flag, what the value must "+
		"satisfy, and what it was.")

	section(b, "Relations")
	para(b, "Two pairs have to hold together, beyond each setting's own floor:")
	para(b, "- `-max-extra-bytes` must not exceed `-max-document-bytes`: preserved undefined members "+
		"cannot outsize the document holding them.\n"+
		"- `-root-retry-min` must not exceed `-root-retry-max`: a backoff cannot start above its own "+
		"ceiling.")

	return writeFlagSections(b)
}

// writeFlagSections renders one table per section, in [config.Limits] order
// within each.
func writeFlagSections(b *strings.Builder) error {
	limits := config.Limits()
	if err := coverUnits(limits); err != nil {
		return err
	}
	for _, s := range flagSections {
		rows := newTable("Flag", "Environment variable", "Unit", "Default", "Read by", "Summary")
		var enums []config.Limit
		for _, l := range limits {
			if l.Unit != s.unit {
				continue
			}
			rows.row(flagName(l), code(l.Env), code(l.Unit), defaultOf(l), readBy(l), l.Summary)
			if len(l.Enum) > 0 {
				enums = append(enums, l)
			}
		}
		if len(rows.rows) == 0 {
			continue
		}
		section(b, s.title)
		para(b, s.lead)
		if err := rows.write(b); err != nil {
			return fmt.Errorf("%s: %w", s.title, err)
		}
		for _, l := range enums {
			para(b, code("-"+l.Flag)+" accepts "+codeList(l.Enum)+".")
		}
	}
	return nil
}

// coverUnits reports a row the sections do not collect.
func coverUnits(limits []config.Limit) error {
	for _, l := range limits {
		found := false
		for _, s := range flagSections {
			if s.unit == l.Unit {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s carries unit %q, which no section collects", l.Name, l.Unit)
		}
	}
	return nil
}

// flagName renders the flag column. A setting the command line does not offer
// as a flag renders as an em dash rather than as a spelling that does not
// parse.
func flagName(l config.Limit) string {
	if l.Flag == "" || l.Flag == "root" {
		return "—"
	}
	return code("-" + l.Flag)
}

// defaultOf renders the value from [config.Defaults] in the unit's own form.
// An empty rendering is a list that defaults to nothing.
func defaultOf(l config.Limit) string {
	if l.Default == "" {
		return "empty"
	}
	return code(l.Default)
}

// readBy names the commands a setting reaches. A flag every command accepts and
// one command reads is a control that reads as honoured everywhere and works in
// one place; the column is where the reference says which.
func readBy(l config.Limit) string {
	if len(l.Commands) == 0 {
		return "every command"
	}
	out := make([]string, 0, len(l.Commands))
	for _, c := range l.Commands {
		out = append(out, code(c))
	}
	return strings.Join(out, ", ")
}
