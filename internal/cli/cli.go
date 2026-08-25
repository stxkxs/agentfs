// Package cli is the command surface: the subcommand table, the flag registry
// resolved from [config.Limits], and the exit-code contract.
//
// [Run] takes its environment as a value and returns a code rather than calling
// os.Exit, so every command — including the terminal one — is exercised by a
// test that supplies its own arguments and captures its own output.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/buildinfo"
	"github.com/stxkxs/agentfs/internal/config"
	"github.com/stxkxs/agentfs/internal/report"
)

// Env is the process environment a command runs in.
type Env struct {
	// Args are the command-line arguments, program name included.
	Args []string
	// Stdout receives results.
	Stdout io.Writer
	// Stderr receives diagnostics and usage.
	Stderr io.Writer
	// Getenv reads the environment. It may be nil, in which case no
	// environment variable is consulted.
	Getenv func(string) string
	// Now is the clock.
	Now func() time.Time
	// Interactive reports whether Stdout is a terminal, which decides whether
	// a command that has a terminal form uses it.
	Interactive bool
	// DarkBackground reports the terminal's background, which selects a
	// palette.
	DarkBackground bool
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

func (e Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

// The output formats. A command declares the ones it has a form for, so a
// value it cannot render is refused at the command line rather than accepted
// and ignored.
const (
	// FormatText is the form a person reads: a terminal frame, or lines.
	FormatText = "text"
	// FormatJSON is one [report.Envelope] carrying a whole result.
	FormatJSON = "json"
	// FormatNDJSON is the [report.Record] stream, one record per line. Only a
	// command that observes over time has one.
	FormatNDJSON = "ndjson"
)

// Formats returns every format some command accepts, in the order the help
// text lists them.
func Formats() []string {
	return []string{FormatText, FormatJSON, FormatNDJSON}
}

// Command is one subcommand.
type Command struct {
	// Name is how the command is invoked.
	Name string
	// Summary is one line for the command list.
	Summary string
	// Usage is the argument form, without the program name.
	Usage string
	// Formats are the values of --format the command renders, the first being
	// its default. A format outside the list exits usage naming the list.
	Formats []string
	// NeedsRoot marks a command that takes a workspace directory.
	NeedsRoot bool
	// Run executes the command.
	Run func(context.Context, Env, Options) report.Code
}

// oneShot is the format set of a command that produces one result: a person
// reads the text, a program reads the envelope.
var oneShot = []string{FormatText, FormatJSON}

// Commands returns the command table in the order the help text lists them.
func Commands() []Command {
	return []Command{
		{Name: "watch", Summary: "Watch a workspace, drawing it or streaming its changes.",
			Usage: "watch <directory>", Formats: []string{FormatText, FormatNDJSON},
			NeedsRoot: true, Run: runWatch},
		{Name: "scan", Summary: "Report the agents a workspace declares, once.",
			Usage: "scan <directory>", Formats: oneShot, NeedsRoot: true, Run: runScan},
		{Name: "validate", Summary: "Check every state document against the contract.",
			Usage: "validate <directory>", Formats: oneShot, NeedsRoot: true, Run: runValidate},
		{Name: "doctor", Summary: "Report how a workspace is observed and what it costs.",
			Usage: "doctor <directory>", Formats: oneShot, NeedsRoot: true, Run: runDoctor},
		{Name: "schema", Summary: "Print the JSON Schema for the state contract.",
			Usage: "schema", Formats: oneShot, Run: runSchema},
		{Name: "version", Summary: "Print the build's identity.",
			Usage: "version", Formats: oneShot, Run: runVersion},
	}
}

// lookup returns the command with the given name.
func lookup(name string) (Command, bool) {
	for _, c := range Commands() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// Run executes one invocation and returns the process exit code.
//
// A bare directory argument runs the terminal command, so `agentfs ./workspace`
// keeps working alongside `agentfs watch ./workspace`.
func Run(ctx context.Context, env Env) report.Code {
	args := env.Args
	if len(args) > 0 {
		args = args[1:]
	}
	if len(args) == 0 {
		writeUsage(newPrinter(env.Stderr), "")
		return report.CodeUsage
	}

	switch args[0] {
	case "-h", "--help", "help":
		writeUsage(newPrinter(env.Stdout), topicOf(args))
		return report.CodeOK
	case "-v", "--version":
		return runVersion(ctx, env, Options{Format: FormatText, Command: "version"})
	}

	name, rest := args[0], args[1:]
	cmd, known := lookup(name)
	if !known {
		// An argument that names no command is the shorthand form: a workspace
		// directory, or a flag preceding one, watched.
		cmd, rest = mustLookup("watch"), args
	}

	opts, err := resolve(env, cmd, rest)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// Asking for help is not a malformed invocation, and help is a result
		// rather than a complaint.
		help := newPrinter(env.Stdout)
		writeUsage(help, cmd.Name)
		return help.finish(env, report.CodeOK)
	case err != nil:
		usage := newPrinter(env.Stderr)
		usage.printf("agentfs: %v\n\n", err)
		writeUsage(usage, cmd.Name)
		return report.CodeUsage
	}
	return cmd.Run(ctx, env, opts)
}

func mustLookup(name string) Command {
	c, ok := lookup(name)
	if !ok {
		panic("cli: command table has no " + name)
	}
	return c
}

func topicOf(args []string) string {
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

// Options is a resolved invocation: the configuration, which carries the
// workspace, and how the result is rendered.
type Options struct {
	// Config carries the ceilings and the workspace directory. One value is
	// the workspace: the one the reference documents, the one
	// [config.Config.Validate] holds to a shape, and the one a command opens.
	Config config.Config
	// Format is one of [Formats], and is one the command declares.
	Format string
	// Command is the name that was invoked.
	Command string
}

// Root returns the workspace directory a command opens.
func (o Options) Root() string { return o.Config.Root }

// JSON reports whether the result is rendered as one envelope.
func (o Options) JSON() bool { return o.Format == FormatJSON }

// NDJSON reports whether the result is rendered as a record stream.
func (o Options) NDJSON() bool { return o.Format == FormatNDJSON }

// resolve parses flags and the positional workspace argument.
//
// The environment is applied before the flags, so an explicit flag wins over an
// exported variable: the more specific statement of intent is the later one.
func resolve(env Env, cmd Command, args []string) (Options, error) {
	cfg := config.Defaults()
	applyEnv(env, &cfg)

	set := flag.NewFlagSet("agentfs "+cmd.Name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	format := set.String("format", FormatText, "output format: "+formatList(cmd.Formats))
	bind(set, &cfg)

	if err := set.Parse(args); err != nil {
		return Options{}, err
	}

	opts := Options{Config: cfg, Format: *format, Command: cmd.Name}
	if !slices.Contains(cmd.Formats, opts.Format) {
		return opts, fmt.Errorf("%s does not render %q: use %s",
			cmd.Name, opts.Format, formatList(cmd.Formats))
	}

	if cmd.NeedsRoot {
		rest := set.Args()
		switch {
		case len(rest) == 1:
			opts.Config.Root = rest[0]
		case len(rest) > 1:
			return opts, fmt.Errorf("%s takes one workspace directory, given %d", cmd.Name, len(rest))
		case opts.Config.Root == config.Defaults().Root:
			// The environment carries a workspace only when it names one other
			// than the default, so a shell that exports nothing still needs the
			// argument.
			return opts, fmt.Errorf("%s needs a workspace directory, as an argument or in %sROOT",
				cmd.Name, config.EnvPrefix)
		}
	}
	if err := opts.Config.Validate(); err != nil {
		return opts, err
	}
	return opts, nil
}

// formatList renders a command's formats as the phrase a refusal ends with.
func formatList(formats []string) string {
	switch len(formats) {
	case 0:
		return "no format"
	case 1:
		return formats[0]
	default:
		return strings.Join(formats[:len(formats)-1], ", ") + " or " + formats[len(formats)-1]
	}
}

// writeUsage prints the command list, or one command's own usage.
func writeUsage(p *printer, topic string) {
	if topic != "" {
		if cmd, ok := lookup(topic); ok {
			p.printf("agentfs %s — %s\n\nusage: agentfs %s [flags]\n\nflags:\n",
				cmd.Name, cmd.Summary, cmd.Usage)
			writeFlags(p, cmd.Formats)
			return
		}
	}

	p.printf("agentfs — watch AI agent workspaces\n\nusage: agentfs <command> [flags] [directory]\n       agentfs <directory>\n\ncommands:\n")
	for _, c := range Commands() {
		p.printf("  %-9s %s\n", c.Name, c.Summary)
	}
	p.printf("\nflags:\n")
	writeFlags(p, Formats())
	p.printf("\nexit codes:\n")
	for _, c := range report.Codes() {
		p.printf("  %-3d %s\n", int(c.Code), c.Summary)
	}
	p.printf("\nEvery flag has an environment variable: %sFLAG_NAME with dashes as underscores.\n", config.EnvPrefix)
	p.printf("The state contract is published by `agentfs schema`.\n")
}

// flagColumn is where a flag's description begins, and helpWidth is where it
// wraps. Both are fixed so the list reads as a column rather than as prose that
// happens to be indented.
const (
	flagColumn = 30
	helpWidth  = 96
)

// writeFlags renders the flag list, opening with the formats the reader's
// command accepts rather than the whole vocabulary: a format one command
// cannot render is a refusal that reader would rather read here.
func writeFlags(p *printer, formats []string) {
	writeFlag(p, "format", "string", FormatText, "Select the output format: "+formatList(formats)+".")
	for _, l := range config.Limits() {
		if l.Flag == "" || l.Flag == "root" {
			// The workspace is a positional argument. Offering it as a flag as
			// well would give one setting two spellings.
			continue
		}
		writeFlag(p, l.Flag, l.Unit, l.Default, l.Summary)
	}
}

// writeFlag renders one flag with its description wrapped into the column.
func writeFlag(p *printer, name, unit, def, summary string) {
	head := fmt.Sprintf("  -%s %s", name, unit)
	pad := flagColumn - len(head)
	if pad < 1 {
		p.println(head)
		head, pad = "", flagColumn
	}

	text := describe(summary, name)
	if def != "" {
		text += " (default " + def + ")"
	}
	for i, line := range wrap(text, helpWidth-flagColumn) {
		if i == 0 {
			p.printf("%s%s%s\n", head, strings.Repeat(" ", pad), line)
			continue
		}
		p.printf("%s%s\n", strings.Repeat(" ", flagColumn), line)
	}
}

// describe turns a table summary into flag help. The table names the Go field
// first because it documents a struct; the flag list does not, because the
// reader is looking at the flag and a Go identifier names nothing they can
// type. The copula goes with the name, so a summary reading "X is the period"
// renders as "The period" rather than as "Is the period".
func describe(summary, flagName string) string {
	rest, cut := cutFieldName(summary, flagName)
	if !cut {
		return summary
	}
	if tail, found := strings.CutPrefix(rest, "is "); found {
		rest = tail
	}
	return upperFirst(rest)
}

// cutFieldName removes a summary's opening word when that word is the Go field
// name the flag sets, and reports whether it did.
//
// A field name differs from its flag only by case and by the dashes the flag
// spells a case change with, so the comparison drops both. One rule then covers
// a name spelled as an acronym, ASCII behind -ascii and DedupTTL behind
// -dedup-ttl, and one spelled in camel case.
func cutFieldName(summary, flagName string) (string, bool) {
	word, rest, split := strings.Cut(summary, " ")
	if !split || !strings.EqualFold(undash(word), undash(flagName)) {
		return summary, false
	}
	return rest, true
}

// undash drops the dashes a flag name carries where a Go field name carries a
// case change.
func undash(s string) string { return strings.ReplaceAll(s, "-", "") }

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// wrap breaks text into lines of at most width columns, on word boundaries.
func wrap(text string, width int) []string {
	if width < 8 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(word) <= width {
			lines[last] += " " + word
			continue
		}
		lines = append(lines, word)
	}
	return lines
}

func firstSentence(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i+1]
	}
	return s
}

func runVersion(_ context.Context, env Env, opts Options) report.Code {
	info := buildinfo.Get()
	p := newPrinter(env.Stdout)

	if opts.JSON() {
		e := report.NewEnvelope(report.KindVersion, info.Version, "", report.CodeOK)
		e.Data = info
		if err := e.WriteJSON(env.Stdout); err != nil {
			return writeErr(env, err, report.CodeOK)
		}
		return report.CodeOK
	}
	p.println(info.Long())
	return p.finish(env, report.CodeOK)
}

// writeErr resolves a failure the command could not proceed past against the
// code the command had reached, which is the rule [printer.finish] applies to
// the text form.
//
// A closed pipe is the reader deciding it has enough, which is not a failure of
// this process, so the command's own verdict stands: a machine reading
// `agentfs validate --format json | head` learns the workspace has findings.
// Any other failure is one the operator needs told about.
func writeErr(env Env, err error, code report.Code) report.Code {
	if report.IsBrokenPipe(err) {
		return code
	}
	out := newPrinter(env.Stderr)
	out.printf("agentfs: %v\n", err)
	return report.CodeInternal
}
