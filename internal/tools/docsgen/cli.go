package main

import (
	"fmt"
	"strings"

	"github.com/stxkxs/agentfs/internal/cli"
	"github.com/stxkxs/agentfs/internal/report"
)

// commandNote describes one command beyond the single line its table row
// carries, and names the statuses it terminates with.
//
// An exit status is a property of a command's implementation rather than of
// the command table, so it is declared here. A command in [cli.Commands] with
// no row stops the generator, which is what keeps a command from reaching the
// reference with its statuses unstated.
type commandNote struct {
	// command is the name in [cli.Commands].
	command string
	// codes are the statuses the command terminates with, in ascending order.
	codes []report.Code
	// detail is what the command does, in the depth a one-line summary cannot
	// carry.
	detail string
}

var commandNotes = []commandNote{
	{
		command: "watch",
		codes: []report.Code{
			report.CodeOK, report.CodeUsage, report.CodePath,
			report.CodeInternal, report.CodeInterrupted,
		},
		detail: "Draws the terminal interface: a workspace tree, a file preview and an activity feed, " +
			"with the run history in place of the tree. The change-detection mode and its counters " +
			"are on screen, so how the workspace is being read is visible rather than inferred. " +
			"Key bindings are in [keys.md](keys.md).\n\n" +
			"Without a terminal on standard output there is nothing to draw, so the command writes " +
			"the change stream instead — the same output `--format ndjson` asks for on a terminal. " +
			"It runs until it is interrupted, exiting `interrupted`, or until a write fails. A reader " +
			"that closes the pipe is recognized on the next record written, so a workspace that goes " +
			"quiet holds the command open until it is signalled. The record shapes are in " +
			"[report-envelope.md](report-envelope.md).",
	},
	{
		command: "scan",
		codes: []report.Code{
			report.CodeOK, report.CodeFindings, report.CodeUsage,
			report.CodePath, report.CodeInternal,
		},
		detail: "Reads every agent workspace under the root once and prints a line for each: the agent's " +
			"name, how its state is known, the status it declares, and the task, step, model and " +
			"problem it declares. A diagnostic about an agent follows that agent's line; a diagnostic " +
			"about the workspace itself is written to standard error.\n\n" +
			"Exits `findings` when a document raises an error-severity diagnostic.",
	},
	{
		command: "validate",
		codes: []report.Code{
			report.CodeOK, report.CodeFindings, report.CodeUsage,
			report.CodePath, report.CodeInternal,
		},
		detail: "Prints every diagnostic the workspace raises, ordered by document path and then by line, " +
			"and closes with the number of documents read, the errors, the warnings and the contract " +
			"version.\n\n" +
			"Exits `findings` when any diagnostic is an error, so a pipeline gates on the status rather " +
			"than on the output.",
	},
	{
		command: "doctor",
		codes: []report.Code{
			report.CodeOK, report.CodeUsage, report.CodePath, report.CodeInternal,
		},
		detail: "Reports how the workspace is observed: the filesystem under the root and the kind it " +
			"classifies as, the change-detection mode resolved for that kind, what confinement the " +
			"platform gives, the agents found, the tracked directories with the sweep budget per " +
			"cycle, the kernel watch budget, and the contract version.\n\n" +
			"When the resolved mode sweeps, it also reports the ceiling on filesystem operations per " +
			"hour this process spends sweeping. Several instances pointed at one shared export cost " +
			"that figure each.",
	},
	{
		command: "schema",
		codes: []report.Code{
			report.CodeOK, report.CodeUsage, report.CodeInternal,
		},
		detail: "Writes the JSON Schema for the state contract to standard output. It reads no workspace, " +
			"so an integrator fetches the contract without having one. The members it declares are in " +
			"[state-schema.md](state-schema.md).",
	},
	{
		command: "version",
		codes: []report.Code{
			report.CodeOK, report.CodeUsage, report.CodeInternal,
		},
		detail: "Prints the version, the commit, the build date, the Go version and the contract version.",
	},
}

// noteFor returns the note declared for a command.
func noteFor(name string) (commandNote, bool) {
	for _, n := range commandNotes {
		if n.command == name {
			return n, true
		}
	}
	return commandNote{}, false
}

func renderCommands(b *strings.Builder, _ module) error {
	title(b, "Commands",
		"agentfs takes a command, and the commands that read a workspace take one directory. "+
			"A first argument that names no command is read as that directory, so the two forms below "+
			"run the same thing.")

	fence(b, "", "agentfs <command> [flags] <directory>\nagentfs <directory>")

	para(b, "`agentfs -h`, `agentfs --help` and `agentfs help` print the command list with every flag "+
		"and every exit code. `agentfs help <command>` prints one command's usage. `agentfs -v` and "+
		"`agentfs --version` run the version command.")

	para(b, "Flags are registered for every command, so `--format` and every setting in "+
		"[flags.md](flags.md) parse the same way whichever command is invoked. `--format text` is the "+
		"default. `--format json` writes one "+code(report.EnvelopeSchema)+" envelope carrying the "+
		"schema, the kind, the agentfs version, the workspace root, the exit code and the payload; "+
		"`--format ndjson` writes the "+code(report.StreamSchema)+" record stream, one record to a "+
		"line. A command renders the formats named in its own section below and exits `usage` on any "+
		"other value. The statuses each command exits with are named below too, and described in "+
		"[exit-codes.md](exit-codes.md).")

	summary := newTable("Command", "Summary", "Usage")
	for _, c := range cli.Commands() {
		summary.row(code("agentfs "+c.Name), c.Summary, code("agentfs "+c.Usage))
	}
	if err := summary.write(b); err != nil {
		return err
	}

	for _, c := range cli.Commands() {
		note, ok := noteFor(c.Name)
		if !ok {
			return fmt.Errorf("command %q has no note, so the page would state neither what it does "+
				"beyond its summary nor what it exits with", c.Name)
		}
		section(b, "agentfs "+c.Name)
		para(b, c.Summary)
		fence(b, "", "agentfs "+c.Usage+" [flags]")
		para(b, note.detail)
		para(b, "Formats: "+formatLine(c.Formats)+".")
		exits, err := exitLine(note.codes)
		if err != nil {
			return fmt.Errorf("command %q: %w", c.Name, err)
		}
		para(b, "Exits: "+exits+".")
	}
	return nil
}

// formatLine renders the values of --format a command accepts.
func formatLine(formats []string) string {
	out := make([]string, 0, len(formats))
	for _, f := range formats {
		out = append(out, code(f))
	}
	return strings.Join(out, ", ")
}

// exitLine renders a command's statuses as the number and the registry name of
// each, so the line reads the same way as the process's own report.
func exitLine(codes []report.Code) (string, error) {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		info, ok := report.Lookup(c)
		if !ok {
			return "", fmt.Errorf("exit status %d is outside the registry", int(c))
		}
		out = append(out, fmt.Sprintf("%s %s", code(fmt.Sprint(int(c))), info.Name))
	}
	return strings.Join(out, ", "), nil
}
