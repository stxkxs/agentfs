// Command covercheck enforces statement-coverage floors on a Go coverage
// profile.
//
// It reads a profile written by "go test -coverprofile" together with a
// floors file, computes covered statements over total statements for every
// package, prints the table, and fails when a package sits below the floor
// declared for it.
//
// Usage:
//
//	covercheck -profile cover.out -config coverage.yaml [-ratchet]
//
// A ratchet run additionally fails when a package fell below the coverage
// recorded for it — including a package that left the profile altogether —
// and rewrites the recorded values that improved. The floor is the contract a
// reviewer agreed to; the recorded value is the high-water mark, which is why
// one is edited by hand and the other by this command.
//
// Exit status is 0 when every floor is met, 1 when a floor is unmet or a
// package regressed, and 2 for a usage or IO error. The last is separate so a
// caller can tell a rejected tree from a gate that could not run.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
)

// Process exit codes.
const (
	exitOK    = 0
	exitBelow = 1
	exitUsage = 2
)

// configPerm is the mode a ratchet creates the floors file with. The file is
// committed alongside the source it gates, so it is world-readable.
const configPerm = 0o644

// filer is the file access covercheck performs. Reads and writes go through
// it so the gate can be exercised against an in-memory tree.
type filer interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte) error
}

// osFiler resolves names against the process working directory.
type osFiler struct{}

// ReadFile implements filer.
func (osFiler) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// WriteFile implements filer.
func (osFiler) WriteFile(name string, data []byte) error {
	return os.WriteFile(name, data, configPerm)
}

// out writes formatted output and holds the first error. A gate that cannot
// print its table has not reported its verdict, so the failure is carried to
// the exit code rather than dropped.
type out struct {
	w   io.Writer
	err error
}

// printf writes one formatted line unless an earlier write failed.
func (o *out) printf(format string, args ...any) {
	if o.err != nil {
		return
	}
	_, o.err = fmt.Fprintf(o.w, format, args...)
}

func main() {
	os.Exit(run(os.Args[1:], osFiler{}, os.Stdout, os.Stderr))
}

// run is covercheck: it parses args, applies the gate, and returns the
// process exit status.
func run(args []string, files filer, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("covercheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "cover.out", "coverage profile to read")
	configPath := flags.String("config", "coverage.yaml", "floors file to enforce")
	ratchet := flags.Bool("ratchet", false, "fail on a drop below a recorded value, and record an improvement")
	flags.Usage = func() {
		o := &out{w: stderr}
		o.printf("usage: covercheck -profile cover.out -config coverage.yaml [-ratchet]\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		return fail(stderr, fmt.Errorf("unexpected argument %q", abbrev(flags.Arg(0))))
	}

	f, p, err := load(files, *configPath, *profilePath)
	if err != nil {
		return fail(stderr, err)
	}
	rep := evaluate(f, p, *ratchet)
	if err := rep.write(stdout); err != nil {
		return fail(stderr, err)
	}
	if err := rep.explain(stderr); err != nil {
		return fail(stderr, err)
	}
	if err := commit(files, *configPath, f, &rep, stderr); err != nil {
		return fail(stderr, err)
	}
	return rep.exit()
}

// load reads the floors file and the coverage profile, naming the file at
// fault in any error.
func load(files filer, configPath, profilePath string) (*floors, *profile, error) {
	configData, err := files.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	f, err := parseFloors(configData)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", configPath, err)
	}
	profileData, err := files.ReadFile(profilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", profilePath, err)
	}
	p, err := parseProfile(bytes.NewReader(profileData))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", profilePath, err)
	}
	return f, p, nil
}

// commit writes the raised recorded values back to the floors file. A run
// that raised nothing leaves the file untouched, so a tree whose coverage has
// not moved produces no diff. A run that raised something writes it whether
// or not the gate passed, because a package that improved is a high-water
// mark even when a different package failed.
func commit(files filer, configPath string, f *floors, rep *report, stderr io.Writer) error {
	if !rep.ratchet || len(rep.updates) == 0 {
		return nil
	}
	if err := files.WriteFile(configPath, f.render(rep.updates)); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	o := &out{w: stderr}
	o.printf("covercheck: recorded %d raised value(s) in %s\n", len(rep.updates), configPath)
	return o.err
}

// fail reports err and returns the usage exit status.
func fail(stderr io.Writer, err error) int {
	o := &out{w: stderr}
	o.printf("covercheck: %v\n", err)
	return exitUsage
}
