// Command schemagen writes the published contracts into schema/.
//
// Three documents are published, one per contract a program on the other side
// of agentfs has to agree with: agent-state.v1.json, the document an agent
// writes; report.v1.json, the one-shot result agentfs emits; and
// stream.v1.json, one line of the change stream. Each is rendered from the Go
// table the running program consults — [agentstate.SchemaJSON] from the rules
// the decoder types members against, [report.EnvelopeSchemaJSON] and
// [report.StreamSchemaJSON] from the member tables the reference page is
// rendered from — so a published contract and the code on either side of it
// cannot disagree. Publishing them as files rather than only as commands lets
// an integrator vendor a contract, or point a validator at it, without
// installing agentfs.
//
// Each document's SHA-256 is recorded beside it under schema/.frozen/.
//
// Usage:
//
//	schemagen
//
// It takes no arguments. The output paths are fixed relative to the module
// root, which it finds by walking up from the working directory to the go.mod
// declaring this module, so the run is independent of where it is invoked
// from and cannot write into an unrelated tree.
//
// The checksums are what make the published contracts frozen artifacts. The
// repository collapses schema/*.json in a review as generated; the checksum
// files are outside that glob, so an edit to a published contract reaches a
// reviewer as a changed digest line rather than as a diff nobody expands.
// Their format is the one shasum(1) and sha256sum(1) read, so the freeze is
// checkable from the module root:
//
//	shasum -a 256 -c schema/.frozen/*.sha256
//
// Exit status is 0 when every file is written and reported, 1 when a document
// cannot be rendered, a file cannot be written, or the result cannot be
// reported, and 2 when arguments are supplied or the module root cannot be
// located.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/report"
)

// Process exit codes.
const (
	exitOK    = 0
	exitWrite = 1
	exitUsage = 2
)

// modulePath is the module schemagen writes into. The root walk requires it,
// so a run from inside a different module stops rather than creating a schema
// directory there.
const modulePath = "github.com/stxkxs/agentfs"

// Path elements of the published artifacts, relative to the module root.
const (
	schemaDir = "schema"
	frozenDir = ".frozen"
	schemaExt = ".json"
	frozenExt = ".sha256"
)

// Modes the artifacts are created with. Both are committed alongside the
// source that generates them, so both are world-readable.
const (
	filePerm = 0o644
	dirPerm  = 0o755
)

// artifact is one published contract.
//
// The table is what a run writes: a contract added here is published, frozen
// and served from the path its own $id names, and one that is not here is a
// contract an integrator can only get by running the binary.
type artifact struct {
	// name is the document's file name under schema/.
	name string
	// render returns the document's bytes.
	render func() ([]byte, error)
}

// artifacts returns the published contracts in the order they are written.
func artifacts() []artifact {
	return []artifact{
		{"agent-state.v1" + schemaExt, agentstate.SchemaJSON},
		{"report.v1" + schemaExt, report.EnvelopeSchemaJSON},
		{"stream.v1" + schemaExt, report.StreamSchemaJSON},
	}
}

// out writes formatted output and holds the first error. A generator that
// could not name what it wrote has not reported its result, so the failure
// reaches the exit code rather than being dropped.
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run generates the artifacts under the module root containing the working
// directory and reports the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	problem := &out{w: stderr}
	if len(args) > 0 {
		names := make([]string, 0, len(artifacts()))
		for _, a := range artifacts() {
			names = append(names, path.Join(schemaDir, a.name))
		}
		problem.printf("schemagen takes no arguments; it writes %s and their checksums under the module root\n",
			strings.Join(names, ", "))
		return exitUsage
	}

	wd, err := os.Getwd()
	if err != nil {
		problem.printf("schemagen: %v\n", err)
		return exitUsage
	}
	root, err := moduleRoot(wd)
	if err != nil {
		problem.printf("schemagen: %v\n", err)
		return exitUsage
	}

	written, err := generate(root)
	if err != nil {
		problem.printf("schemagen: %v\n", err)
		return exitWrite
	}

	result := &out{w: stdout}
	for _, name := range written {
		result.printf("wrote %s\n", name)
	}
	if result.err != nil {
		problem.printf("schemagen: %v\n", result.err)
		return exitWrite
	}
	return exitOK
}

// generate writes every published contract and its checksum under root,
// returning the paths it wrote in the order it wrote them.
func generate(root string) ([]string, error) {
	if err := os.MkdirAll(filepath.Join(root, schemaDir, frozenDir), dirPerm); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	written := make([]string, 0, 2*len(artifacts()))
	for _, a := range artifacts() {
		doc, err := a.render()
		if err != nil {
			return nil, err
		}
		schema, frozen := schemaPath(root, a.name), frozenPath(root, a.name)
		if err := os.WriteFile(schema, doc, filePerm); err != nil {
			return nil, fmt.Errorf("write %s: %w", a.name, err)
		}
		if err := os.WriteFile(frozen, []byte(checksumLine(a.name, doc)), filePerm); err != nil {
			return nil, fmt.Errorf("write the checksum of %s: %w", a.name, err)
		}
		written = append(written, schema, frozen)
	}
	return written, nil
}

// schemaPath returns a published document's path under a module root.
func schemaPath(root, name string) string {
	return filepath.Join(root, schemaDir, name)
}

// frozenPath returns a published document's checksum path under a module root.
func frozenPath(root, name string) string {
	return filepath.Join(root, schemaDir, frozenDir, frozenName(name))
}

// frozenName returns the checksum file name covering a document.
func frozenName(name string) string {
	return strings.TrimSuffix(name, schemaExt) + frozenExt
}

// checksumLine renders doc's digest in the format shasum(1) and sha256sum(1)
// read: the hex digest, two spaces, and the path the digest covers written
// with forward slashes and relative to the module root, so a check run from
// the module root resolves it on every platform.
func checksumLine(name string, doc []byte) string {
	sum := sha256.Sum256(doc)
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), path.Join(schemaDir, name))
}

// moduleRoot returns the directory of the go.mod declaring [modulePath],
// walking up from dir. The first go.mod found is the enclosing module by Go's
// own resolution rule; one declaring a different module is an error rather
// than a directory to write into.
func moduleRoot(dir string) (string, error) {
	start := dir
	for {
		name := filepath.Join(dir, "go.mod")
		src, err := os.ReadFile(name)
		switch {
		case err == nil:
			declared := declaredModule(src)
			if declared != modulePath {
				return "", fmt.Errorf("%s declares module %q, not %q", name, declared, modulePath)
			}
			return dir, nil
		case !os.IsNotExist(err):
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", start)
		}
		dir = parent
	}
}

// declaredModule returns the path a go.mod's module directive declares, empty
// when it declares none.
func declaredModule(src []byte) string {
	for line := range strings.Lines(string(src)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}
