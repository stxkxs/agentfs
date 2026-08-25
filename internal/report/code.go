// Package report is every machine-readable byte agentfs emits, and the exit
// codes it emits them alongside.
//
// An unattended caller branches on two things: the process exit code and the
// envelope it reads from the command's output. Both are contracts, so both are
// declared here rather than at the call sites that produce them. [Codes] is
// the single table the published exit-code reference is rendered from, so the
// reference cannot describe a status the process does not return, and a code
// the table does not name fails this package's own tests rather than shipping
// undocumented.
//
// A one-shot command emits one [Envelope]. A watching command emits a [Stream]
// of [Record] lines. Neither form reads the clock or the environment: the
// caller supplies the writer and the instant, which is what makes both
// testable byte for byte.
package report

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/stxkxs/agentfs/internal/diag"
)

// Code is a process exit code.
//
// The values are the contract a shell pipeline branches on: they separate "ran
// and found nothing" from "ran and found something" from "could not run at
// all", so a gate decides what to do without parsing output. A value is
// retired rather than reassigned, because a caller pinned to a code cannot
// tell that its meaning changed underneath it.
type Code int

// Exit codes agentfs terminates with, ascending.
const (
	// CodeOK reports that the command succeeded with nothing to report.
	CodeOK Code = 0
	// CodeFindings reports that the command ran to completion and reported
	// findings. The output is a complete result, not an error.
	CodeFindings Code = 1
	// CodeUsage reports that the invocation was malformed: an unknown flag, a
	// missing argument, or a value a flag does not accept.
	CodeUsage Code = 2
	// CodePath reports that the workspace root could not be read.
	CodePath Code = 3
	// CodeInternal reports an unexpected fault, with the stack included in the
	// report. The value is EX_SOFTWARE from sysexits.h.
	CodeInternal Code = 70
	// CodeInterrupted reports that SIGINT ended the command. The value follows
	// the shell convention of 128 plus the signal number.
	CodeInterrupted Code = 130
)

// CodeInfo describes one exit code for the generated reference.
type CodeInfo struct {
	// Code is the integer the process exits with.
	Code Code
	// Name is the lowercase identifier the reference and [Code.String] use.
	Name string
	// Summary is one sentence naming the condition the code reports.
	Summary string
}

var registry = []CodeInfo{
	{CodeOK, "ok", "The command succeeded and had nothing to report."},
	{CodeFindings, "findings", "The command ran to completion and reported findings."},
	{CodeUsage, "usage", "The invocation was malformed."},
	{CodePath, "path", "The workspace root could not be read."},
	{CodeInternal, "internal", "An unexpected fault ended the command, and the stack is reported."},
	{CodeInterrupted, "interrupted", "SIGINT ended the command."},
}

// Codes returns every registered exit code in ascending order.
//
// The order is imposed here rather than taken from the registry's declaration,
// so adding a row cannot change what the reference prints. The result is a
// copy: a caller that sorts or filters it does not disturb the table.
func Codes() []CodeInfo {
	out := slices.Clone(registry)
	slices.SortFunc(out, func(a, b CodeInfo) int { return cmp.Compare(a.Code, b.Code) })
	return out
}

// Lookup returns the registry entry for c, and reports whether c is a code
// agentfs exits with.
func Lookup(c Code) (CodeInfo, bool) {
	for _, info := range registry {
		if info.Code == c {
			return info, true
		}
	}
	return CodeInfo{}, false
}

// String returns the code's registry name, or the integer rendered as "exit N"
// for a value outside the registry.
func (c Code) String() string {
	if info, ok := Lookup(c); ok {
		return info.Name
	}
	return "exit " + strconv.Itoa(int(c))
}

// ExitFor returns the exit code a command reports for a set of diagnostics.
//
// A diagnostic at [diag.Warning] or above is a finding: it names something an
// operator has to decide about. A [diag.Info] diagnostic annotates a result
// that is otherwise clean — a compatibility filename, a member the contract
// does not define — and leaves the exit code alone, so a gate running agentfs
// does not fail on a document agentfs reads perfectly well.
func ExitFor(ds []diag.Diagnostic) Code {
	for _, d := range ds {
		if d.Severity >= diag.Warning {
			return CodeFindings
		}
	}
	return CodeOK
}
