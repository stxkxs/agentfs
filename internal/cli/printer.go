package cli

import (
	"fmt"
	"io"

	"github.com/stxkxs/agentfs/internal/report"
)

// printer writes command output and remembers the first write that failed.
//
// A command writes many lines and can act on only one outcome, so checking each
// write at its call site adds noise without adding a decision. The first
// failure is kept and read once, at the end, where the command decides whether
// a closed pipe is a reader that had enough or a fault worth reporting.
type printer struct {
	w   io.Writer
	err error
}

func newPrinter(w io.Writer) *printer { return &printer{w: w} }

// printf writes a formatted line.
func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, err := fmt.Fprintf(p.w, format, args...)
	p.err = err
}

// println writes a line.
func (p *printer) println(args ...any) {
	if p.err != nil {
		return
	}
	_, err := fmt.Fprintln(p.w, args...)
	p.err = err
}

// write writes raw bytes.
func (p *printer) write(b []byte) {
	if p.err != nil {
		return
	}
	_, err := p.w.Write(b)
	p.err = err
}

// Err returns the first write that failed.
func (p *printer) Err() error { return p.err }

// finish resolves a command's exit code against the output it managed to write.
//
// A reader closing the pipe — `agentfs scan ... | head` — is a decision that it
// has enough, not a failure of this process, so it keeps the code the command
// reached. Any other write failure is one the operator needs told about.
func (p *printer) finish(env Env, code report.Code) report.Code {
	if p.err == nil || report.IsBrokenPipe(p.err) {
		return code
	}
	out := newPrinter(env.Stderr)
	out.printf("agentfs: %v\n", p.err)
	return report.CodeInternal
}
