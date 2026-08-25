package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stxkxs/agentfs/internal/cli"
	"github.com/stxkxs/agentfs/internal/report"
)

// sweepMode is the detection mechanism a stream test forces, so a change is
// observed within a test's patience whatever the filesystem underneath reports
// through the kernel.
const sweepMode = "sweep"

// settle is how long a stream test waits for a record it expects. It is long
// enough to absorb a loaded machine and short enough that a stream that never
// produces the record fails the run rather than holding it.
const settle = 15 * time.Second

// syncBuffer collects what a running command writes. The command writes from
// its own goroutine while the test reads, so the buffer is guarded.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// running is one watch command in flight, and the records it has written.
type running struct {
	out  *syncBuffer
	errs *syncBuffer
	// code is what the command exited with, readable once done is closed.
	code report.Code
	done chan struct{}
	// cancel ends the command the way SIGINT ends it.
	cancel context.CancelFunc
}

// watchStream starts `agentfs watch` over a workspace with the sweep forced and
// shortened, so a change is observed within a test's patience whatever the
// filesystem underneath reports through the kernel.
func watchStream(t *testing.T, root string, extra ...string) *running {
	t.Helper()

	args := append([]string{"agentfs", "watch", "-watch", sweepMode, "-sweep-interval", "100ms"}, extra...)
	args = append(args, root)

	ctx, cancel := context.WithCancel(t.Context())
	r := &running{
		out:    &syncBuffer{},
		errs:   &syncBuffer{},
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go func() {
		defer close(r.done)
		r.code = cli.Run(ctx, cli.Env{
			Args:   args,
			Stdout: r.out,
			Stderr: r.errs,
			Getenv: func(string) string { return "" },
		})
	}()
	t.Cleanup(r.stop)
	return r
}

// records decodes the complete lines the stream has written so far. A partial
// final line is a record still being written, not a malformed one.
func (r *running) records(t *testing.T) []report.Record {
	t.Helper()
	out := r.out.String()
	if !strings.HasSuffix(out, "\n") {
		if i := strings.LastIndexByte(out, '\n'); i >= 0 {
			out = out[:i+1]
		} else {
			return nil
		}
	}
	var got []report.Record
	for line := range strings.Lines(out) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		var rec report.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a stream line is not a record: %v: %q", err, line)
		}
		got = append(got, rec)
	}
	return got
}

// await waits for the stream to satisfy want and returns the records it had
// when it did.
func (r *running) await(t *testing.T, want func([]report.Record) bool) []report.Record {
	t.Helper()
	return r.awaitWith(t, func(int) {}, want)
}

// awaitWith applies poke before each reading until the stream satisfies want.
//
// A sweep's first reading of a directory establishes what is there rather than
// reporting it, so a change made before that reading is not a change to
// report. A test therefore makes its change until it is observed rather than
// once, and poke takes the attempt number so each one differs from the last.
func (r *running) awaitWith(t *testing.T, poke func(int), want func([]report.Record) bool) []report.Record {
	t.Helper()
	deadline := time.Now().Add(settle)
	for attempt := 0; ; attempt++ {
		got := r.records(t)
		if want(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream did not produce what the test waited for in %s:\n%s%s",
				settle, r.out.String(), r.errs.String())
		}
		poke(attempt)
		time.Sleep(20 * time.Millisecond)
	}
}

// stop ends the command and waits for it. It is safe to call more than once,
// so a test can end the stream where it means to and still be cleaned up.
func (r *running) stop() {
	r.cancel()
	<-r.done
}

// exit returns the code the command ended with, waiting for it to end.
func (r *running) exit() report.Code {
	r.stop()
	return r.code
}

// changed returns the change records naming a path.
func changed(recs []report.Record, path string) []report.Record {
	var out []report.Record
	for _, rec := range recs {
		if rec.Kind != report.RecordChange {
			continue
		}
		data, ok := rec.Data.(map[string]any)
		if ok && data["path"] == path {
			out = append(out, rec)
		}
	}
	return out
}

// kindsIn returns the record kinds present, in first-seen order.
func kindsIn(recs []report.Record) []string {
	var out []string
	for _, rec := range recs {
		if !slices.Contains(out, rec.Kind) {
			out = append(out, rec.Kind)
		}
	}
	return out
}

// The stream is the non-terminal form of the watching command: one record per
// observed change, framed one to a line, each carrying the schema, a dense
// sequence number and the identity a consumer deduplicates on.
func TestWatchStreamsChangesAsNDJSON(t *testing.T) {
	t.Parallel()

	ws := conformant(t)
	run := watchStream(t, ws, "-format", "ndjson")

	opened := run.await(t, func(recs []report.Record) bool { return len(recs) > 0 })
	if opened[0].Kind != report.RecordStatus {
		t.Fatalf("the stream opens with a %q record, want %q", opened[0].Kind, report.RecordStatus)
	}
	status, ok := opened[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("the opening record carries %T, want an object", opened[0].Data)
	}
	if status["event"] != "opened" {
		t.Errorf("the opening record reports %v, want the opened event", status["event"])
	}
	if status["watching"] == "" || status["mode"] != sweepMode {
		t.Errorf("the opening record does not say what is being watched: %v", status)
	}

	const doc = "agent-writer/state.json"
	recs := run.awaitWith(t,
		func(attempt int) { declare(t, ws, doc, "running", attempt) },
		func(recs []report.Record) bool { return len(changed(recs, doc)) > 0 })
	change := changed(recs, doc)[0]
	if change.DedupKey == "" {
		t.Error("a change record carries no dedup key, so a consumer cannot discard a repeat")
	}
	if change.Time == "" {
		t.Error("a change record carries no time")
	}
	data, ok := change.Data.(map[string]any)
	if !ok {
		t.Fatalf("a change record carries %T, want an object", change.Data)
	}
	if data["op"] != "modify" {
		t.Errorf("op = %v, want the word modify", data["op"])
	}

	for i, rec := range recs {
		if rec.Schema != report.StreamSchema {
			t.Errorf("record %d: schema = %q, want %q", i, rec.Schema, report.StreamSchema)
		}
		if rec.Seq != uint64(i)+1 {
			t.Errorf("record %d: seq = %d, want %d — a gap is a loss a consumer will report",
				i, rec.Seq, i+1)
		}
		if !slices.Contains(report.StreamKinds(), rec.Kind) {
			t.Errorf("record %d: kind %q is outside the vocabulary", i, rec.Kind)
		}
	}

	// SIGINT reaches the stream as a cancelled context, and the shell
	// convention for it is 128 plus the signal number.
	if code := run.exit(); code != report.CodeInterrupted {
		t.Errorf("a cancelled stream exited %d, want %d", code, report.CodeInterrupted)
	}
}

// declare rewrites an agent's state document, with a step that differs on
// every call so successive writes are successive changes.
func declare(t *testing.T, ws, doc, status string, step int) {
	t.Helper()
	body := fmt.Sprintf(`{"schema":"agentfs/v1","status":%q,"step":%d}`, status, step+1)
	if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(doc)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A reader closing the pipe is a decision that it has enough, so the stream
// stops without reporting a fault and keeps the code it had reached.
func TestAClosedPipeEndsTheStreamWithoutAFault(t *testing.T) {
	t.Parallel()
	got := invokeTo(t, failingWriter{err: syscall.EPIPE}, "watch", "-format", "ndjson", conformant(t))

	if got.code != report.CodeOK {
		t.Errorf("exit = %d against a closed pipe, want %d", got.code, report.CodeOK)
	}
	if got.stderr != "" {
		t.Errorf("a closed pipe was reported as a fault:\n%s", got.stderr)
	}
}

// The reciprocal of report.StreamKinds: every kind in the vocabulary is one the
// watching command writes, and every kind it writes is in the vocabulary. A
// kind no producer writes is one a consumer waits for forever.
func TestEveryRecordKindIsEmitted(t *testing.T) {
	t.Parallel()

	ws := conformant(t)
	run := watchStream(t, ws, "-format", "ndjson")
	defer run.stop()

	run.await(t, func(recs []report.Record) bool { return len(recs) > 0 })

	// A tracked directory that goes away is a fault the producer meets on its
	// next pass and cannot resolve.
	if err := os.RemoveAll(filepath.Join(ws, "agent-researcher")); err != nil {
		t.Fatal(err)
	}

	recs := run.awaitWith(t,
		func(attempt int) { declare(t, ws, "agent-writer/state.json", "blocked", attempt) },
		func(recs []report.Record) bool {
			got := kindsIn(recs)
			for _, want := range report.StreamKinds() {
				if !slices.Contains(got, want) {
					return false
				}
			}
			return true
		})

	for _, rec := range recs {
		if !slices.Contains(report.StreamKinds(), rec.Kind) {
			t.Errorf("the stream wrote the kind %q, which is outside report.StreamKinds()", rec.Kind)
		}
	}
}

// A stream belongs to the command that observes over time, and to no other.
// A command that produces one result has nothing to stream, so it refuses the
// format naming what it does render rather than accepting a value it ignores.
func TestOnlyTheWatchingCommandRendersNDJSON(t *testing.T) {
	t.Parallel()
	ws := conformant(t)

	streams := map[string]bool{"watch": true}
	oneShot := []string{"scan", "validate", "doctor", "schema", "version"}
	for _, name := range oneShot {
		streams[name] = false
	}

	for _, cmd := range cli.Commands() {
		wants, named := streams[cmd.Name]
		if !named {
			t.Errorf("the command %q is in neither list, so this test says nothing about it", cmd.Name)
			continue
		}
		if got := slices.Contains(cmd.Formats, cli.FormatNDJSON); got != wants {
			t.Errorf("%s declares ndjson = %v, want %v", cmd.Name, got, wants)
		}
	}

	for _, name := range oneShot {
		args := []string{name, "-format", "ndjson"}
		if cmd, ok := commandNamed(t, name); ok && cmd.NeedsRoot {
			args = append(args, ws)
		}
		got := invoke(t, nil, args...)

		if got.code != report.CodeUsage {
			t.Errorf("%s --format ndjson exited %d, want %d", name, got.code, report.CodeUsage)
		}
		if !strings.Contains(got.stderr, "text or json") {
			t.Errorf("%s does not name the formats it renders:\n%s", name, got.stderr)
		}
	}
}

// commandNamed returns the entry of the command table with the given name.
func commandNamed(t *testing.T, name string) (cli.Command, bool) {
	t.Helper()
	for _, c := range cli.Commands() {
		if c.Name == name {
			return c, true
		}
	}
	t.Errorf("the command table has no %q", name)
	return cli.Command{}, false
}

// A format the command list declares must be one the flag accepts, or the help
// text advertises a value the command line refuses.
func TestEveryCommandDeclaresFormatsItAccepts(t *testing.T) {
	t.Parallel()

	for _, cmd := range cli.Commands() {
		if len(cmd.Formats) == 0 {
			t.Errorf("%s declares no format", cmd.Name)
			continue
		}
		if cmd.Formats[0] != cli.FormatText {
			t.Errorf("%s defaults to %q, and the flag defaults to text", cmd.Name, cmd.Formats[0])
		}
		for _, format := range cmd.Formats {
			if !slices.Contains(cli.Formats(), format) {
				t.Errorf("%s declares the format %q, which is not one agentfs has", cmd.Name, format)
			}
		}
		help := invoke(t, nil, "help", cmd.Name)
		if !strings.Contains(help.stdout, strings.Join(cmd.Formats, ", ")) &&
			!strings.Contains(help.stdout, strings.Join(cmd.Formats, " or ")) {
			t.Errorf("the usage for %s does not name the formats it renders:\n%s", cmd.Name, help.stdout)
		}
	}
}

// The record stream is the second path from a byte an agent wrote to a
// program that may print it: a filename or a declared value reaches a consumer
// which cats it into a terminal.
func TestTheStreamWritesNoEscapeFromWorkspaceContent(t *testing.T) {
	t.Parallel()

	ws := workspace(t, map[string]string{
		"agent-" + strings.ReplaceAll(hostile, "/", "") + "/state.json": `{"schema":"agentfs/v1","status":"running"}`,
	})
	run := watchStream(t, ws, "-format", "ndjson")
	defer run.stop()

	run.awaitWith(t,
		func(attempt int) {
			declare(t, ws, "agent-"+strings.ReplaceAll(hostile, "/", "")+"/state.json", "running", attempt)
		},
		func(recs []report.Record) bool { return len(recs) > 1 })

	assertNoTerminalControl(t, "the stream", run.out.String())
	assertNoTerminalControl(t, "stderr", run.errs.String())
}
