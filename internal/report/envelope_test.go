package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/diag"
)

const testVersion = "1.4.0"

func sampleEnvelope() Envelope {
	e := NewEnvelope(KindScan, testVersion, "/w/space", CodeFindings)
	e.Data = map[string]any{
		"zeta":     3,
		"alpha":    "one",
		"middle":   []any{"a", "b"},
		"beta":     true,
		"gamma":    nil,
		"delta":    1.5,
		"epsilon":  map[string]any{"nested": "value", "another": 2},
		"omega":    "last",
		"iota":     "",
		"kappa":    []any{},
		"lambda":   map[string]any{},
		"mu":       "µ",
		"unicode":  "路径/日志",
		"controls": "a\tb\nc",
	}
	e.Diagnostics = []diag.Diagnostic{
		{
			Code:     diag.CodeStale,
			Severity: diag.Warning,
			Path:     "agent-a/state.json",
			Pointer:  "/updated_at",
			Line:     4,
			Column:   17,
			Message:  "The document has not been updated within its declared heartbeat.",
			Hint:     "Rewrite the document or lower heartbeat_seconds.",
			Value:    "2026-08-24T11:00:00Z",
		},
		{
			Code:     diag.CodeLegacyFilename,
			Severity: diag.Info,
			Path:     "agent-b/status.json",
			Message:  "The state document uses a compatibility filename.",
		},
	}
	return e
}

func TestNewEnvelope(t *testing.T) {
	got := NewEnvelope(KindValidate, testVersion, "/w", CodeOK)
	if got.Schema != EnvelopeSchema {
		t.Errorf("Schema = %q, want %q", got.Schema, EnvelopeSchema)
	}
	if got.Kind != KindValidate {
		t.Errorf("Kind = %q, want %q", got.Kind, KindValidate)
	}
	if got.Version != testVersion {
		t.Errorf("Version = %q, want %q", got.Version, testVersion)
	}
	if got.Root != "/w" {
		t.Errorf("Root = %q, want %q", got.Root, "/w")
	}
	if got.Exit != CodeOK {
		t.Errorf("Exit = %s, want %s", got.Exit, CodeOK)
	}
	if got.Data != nil || got.Diagnostics != nil {
		t.Error("NewEnvelope attached a payload")
	}
}

func TestKinds(t *testing.T) {
	got := Kinds()
	want := []string{KindScan, KindValidate, KindDoctor, KindVersion, KindError}
	if !slices.Equal(got, want) {
		t.Errorf("Kinds = %v, want %v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, k := range got {
		if k == "" {
			t.Error("Kinds holds an empty kind")
		}
		if seen[k] {
			t.Errorf("Kinds repeats %q", k)
		}
		seen[k] = true
	}
}

// A consumer that iterates the findings must not have to test the key first,
// so the member is an array on a clean result rather than absent or null.
func TestWriteJSONAlwaysCarriesADiagnosticsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := NewEnvelope(KindScan, testVersion, "/w", CodeOK).WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `"diagnostics":[]`) {
		t.Fatalf("a clean result carries no empty diagnostics array: %s", buf.String())
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ds, ok := got["diagnostics"].([]any)
	if !ok {
		t.Fatalf("diagnostics is %T, want an array", got["diagnostics"])
	}
	if len(ds) != 0 {
		t.Errorf("diagnostics = %v, want it empty", ds)
	}
}

func TestWriteJSONIsDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	if err := sampleEnvelope().WriteJSON(&first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := sampleEnvelope().WriteJSON(&second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("equal envelopes encoded differently:\n%s\n%s", first.String(), second.String())
	}

	// The same value encoded repeatedly must not drift either, which is what a
	// content hash over a captured report depends on.
	e := sampleEnvelope()
	for i := range 8 {
		var again bytes.Buffer
		if err := e.WriteJSON(&again); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if !bytes.Equal(first.Bytes(), again.Bytes()) {
			t.Fatalf("encoding %d differs from the first", i)
		}
	}
}

func TestWriteJSONFraming(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleEnvelope().WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Error("output does not end with a newline")
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("output holds %d newlines, want 1", n)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("output is not valid JSON: %s", out)
	}
}

func TestWriteJSONMembers(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleEnvelope().WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	dec := json.NewDecoder(&buf)
	dec.UseNumber()
	var got map[string]any
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got["schema"] != EnvelopeSchema {
		t.Errorf("schema = %v, want %q", got["schema"], EnvelopeSchema)
	}
	if got["kind"] != KindScan {
		t.Errorf("kind = %v, want %q", got["kind"], KindScan)
	}
	if got["version"] != testVersion {
		t.Errorf("version = %v, want %q", got["version"], testVersion)
	}
	if got["root"] != "/w/space" {
		t.Errorf("root = %v", got["root"])
	}

	exit, ok := got["exit"].(json.Number)
	if !ok {
		t.Fatalf("exit is %T, want a JSON number so a consumer can compare it to the process status", got["exit"])
	}
	if exit.String() != "1" {
		t.Errorf("exit = %s, want 1", exit)
	}
}

func TestWriteJSONDiagnosticWireForm(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleEnvelope().WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got struct {
		Diagnostics []map[string]any `json:"diagnostics"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Diagnostics) != 2 {
		t.Fatalf("decoded %d diagnostics, want 2", len(got.Diagnostics))
	}

	first := got.Diagnostics[0]
	if first["code"] != string(diag.CodeStale) {
		t.Errorf("code = %v, want %q", first["code"], diag.CodeStale)
	}
	if first["severity"] != "warning" {
		t.Errorf("severity = %v, want the wire form %q", first["severity"], "warning")
	}
	if first["pointer"] != "/updated_at" {
		t.Errorf("pointer = %v", first["pointer"])
	}

	// A diagnostic that resolved no position omits its coordinates rather than
	// claiming line 0, which is not a position any editor can open.
	second := got.Diagnostics[1]
	for _, member := range []string{"line", "column", "pointer", "hint", "value"} {
		if _, present := second[member]; present {
			t.Errorf("unset member %q was encoded", member)
		}
	}
}

func TestWriteJSONOmitsUnsetMembers(t *testing.T) {
	var buf bytes.Buffer
	if err := NewEnvelope(KindDoctor, testVersion, "", CodeOK).WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, member := range []string{"root", "data"} {
		if _, present := got[member]; present {
			t.Errorf("unset member %q was encoded", member)
		}
	}
	// A zero exit is a verdict, not an absence, so it is always encoded.
	if _, present := got["exit"]; !present {
		t.Error("a zero exit was omitted, so a consumer cannot distinguish success from a missing member")
	}
}

func TestWriteJSONFillsSchema(t *testing.T) {
	e := Envelope{Kind: KindError, Version: testVersion, Exit: CodeUsage}

	var buf bytes.Buffer
	if err := e.WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `"schema":"`+EnvelopeSchema+`"`) {
		t.Errorf("an envelope built as a literal encoded without a schema: %s", buf.String())
	}
	if e.Schema != "" {
		t.Error("WriteJSON mutated the caller's envelope")
	}
}

func TestWriteJSONKeepsSchemaOverride(t *testing.T) {
	e := NewEnvelope(KindScan, testVersion, "", CodeOK)
	e.Schema = "agentfs/report/v99"

	var buf bytes.Buffer
	if err := e.WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `"schema":"agentfs/report/v99"`) {
		t.Errorf("an explicit schema was overwritten: %s", buf.String())
	}
}

func TestWriteJSONDoesNotEscapeHTML(t *testing.T) {
	e := NewEnvelope(KindScan, testVersion, "/w/a&b/<c>", CodeOK)

	var buf bytes.Buffer
	if err := e.WriteJSON(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), `"/w/a&b/<c>"`) {
		t.Errorf("the root path was escaped: %s", buf.String())
	}
}

func TestWriteJSONUnmarshalablePayloadEmitsNothing(t *testing.T) {
	e := NewEnvelope(KindScan, testVersion, "/w", CodeOK)
	e.Data = make(chan int)

	var buf bytes.Buffer
	err := e.WriteJSON(&buf)
	if err == nil {
		t.Fatal("encoding a channel payload succeeded")
	}
	if !strings.Contains(err.Error(), KindScan) {
		t.Errorf("error does not name the kind: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a failed encode wrote %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestWriteJSONReportsWriterError(t *testing.T) {
	sentinel := errors.New("device is on fire")
	err := NewEnvelope(KindScan, testVersion, "/w", CodeOK).WriteJSON(errWriter{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WriteJSON = %v, want an error wrapping the writer's", err)
	}
	if IsBrokenPipe(err) {
		t.Error("an unrelated write error was reported as a broken pipe")
	}
}

func TestWriteJSONReportsBrokenPipe(t *testing.T) {
	err := NewEnvelope(KindScan, testVersion, "/w", CodeOK).WriteJSON(errWriter{err: syscall.EPIPE})
	if err == nil {
		t.Fatal("writing to a closed pipe succeeded")
	}
	if !IsBrokenPipe(err) {
		t.Errorf("IsBrokenPipe(%v) = false", err)
	}
}

func FuzzEnvelopeWriteJSON(f *testing.F) {
	f.Add(KindScan, "1.0.0", "/w", 0, "payload")
	f.Add(KindError, "", "", 130, "")
	f.Add("kind\nwith\nnewlines", "v\t", "/w/\"quoted\"", 70, "{\"not\":\"nested\"}")
	f.Add("\x00\x1b[2J", "\u202e", "/w/\ufeff", -1, "\xff\xfe")
	f.Add("scan", "1.0.0", "a&b<c>", 1, "\\")

	f.Fuzz(func(t *testing.T, kind, version, root string, exit int, payload string) {
		e := NewEnvelope(kind, version, root, Code(exit))
		e.Data = map[string]any{"payload": payload}

		var buf bytes.Buffer
		if err := e.WriteJSON(&buf); err != nil {
			t.Fatalf("write: %v", err)
		}

		out := buf.Bytes()
		if n := bytes.Count(out, []byte("\n")); n != 1 {
			t.Fatalf("output holds %d newlines, want exactly the framing one: %q", n, out)
		}
		if out[len(out)-1] != '\n' {
			t.Fatalf("output does not end with the framing newline: %q", out)
		}
		if !json.Valid(out) {
			t.Fatalf("output is not valid JSON: %q", out)
		}

		var got Envelope
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Schema != EnvelopeSchema {
			t.Errorf("schema = %q, want %q", got.Schema, EnvelopeSchema)
		}
		if got.Exit != Code(exit) {
			t.Errorf("exit = %d, want %d", int(got.Exit), exit)
		}

		// Strings that are not valid UTF-8 are encoded with replacement
		// characters, so only well-formed input round-trips unchanged.
		if utf8.ValidString(kind) && got.Kind != kind {
			t.Errorf("kind = %q, want %q", got.Kind, kind)
		}
		if utf8.ValidString(version) && got.Version != version {
			t.Errorf("version = %q, want %q", got.Version, version)
		}
		if utf8.ValidString(root) && got.Root != root {
			t.Errorf("root = %q, want %q", got.Root, root)
		}
	})
}

func TestEnvelopeWriteJSONToDiscard(t *testing.T) {
	// A caller that only wants the error path must not be forced to allocate a
	// buffer to reach it.
	if err := sampleEnvelope().WriteJSON(io.Discard); err != nil {
		t.Fatalf("write: %v", err)
	}
}
