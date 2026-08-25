package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/stxkxs/agentfs/internal/diag"
)

// EnvelopeSchema identifies the one-shot result format. A consumer that reads
// a value it does not implement stops rather than guessing at the members
// underneath it.
const EnvelopeSchema = "agentfs/report/v1"

// Envelope kinds. The kind names the command that produced the result, which
// is how a consumer selects a payload shape without first parsing the payload.
const (
	// KindScan is the result of a workspace scan.
	KindScan = "scan"
	// KindValidate is the result of validating state documents against the
	// contract.
	KindValidate = "validate"
	// KindDoctor is the result of an environment check.
	KindDoctor = "doctor"
	// KindVersion is the build's identity.
	KindVersion = "version"
	// KindError is a command that could not produce its result. The exit code
	// names the reason and the diagnostics describe it.
	KindError = "error"
)

// Kinds returns the envelope kind vocabulary in the order the reference lists
// it.
//
// The table is the vocabulary: a command emits one of these constants rather
// than a string of its own, and a kind no command emits is one a consumer
// waits for forever. TestEveryKindIsEmitted holds both directions.
func Kinds() []string {
	return []string{KindScan, KindValidate, KindDoctor, KindVersion, KindError}
}

// Envelope wraps every one-shot JSON result, so a consumer branches on kind
// and version before it commits to a payload shape.
//
// Schema, Kind, Version and Exit are the stable frame: they carry the same
// meaning in every schema version, so a consumer that reads an envelope whose
// Schema it does not implement can still report the Kind and Exit it found. A
// version mismatch surfaces as a clear refusal rather than as a parse failure
// deep in a payload.
type Envelope struct {
	// Schema is [EnvelopeSchema].
	Schema string `json:"schema"`
	// Kind selects the shape of Data. See [Kinds].
	Kind string `json:"kind"`
	// Version is the agentfs version that produced the result.
	Version string `json:"version"`
	// Root is the workspace the result describes, omitted when the result
	// describes no workspace.
	Root string `json:"root,omitempty"`
	// Exit is the code the process terminates with, repeated in the payload so
	// a consumer reading a captured file reaches the same verdict as one that
	// watched the process.
	Exit Code `json:"exit"`
	// Data is the payload the kind selects.
	Data any `json:"data,omitempty"`
	// Diagnostics are the findings the command accumulated. The member is
	// always an array, empty included: a consumer iterating it on a clean
	// workspace reads an empty list rather than failing on an absent key.
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}

// NewEnvelope returns an envelope stamped with the schema identifier, for the
// caller to attach Data and Diagnostics to.
//
// An empty root is omitted from the output, which is how a result that
// describes no workspace — a usage error, an environment check — is encoded.
func NewEnvelope(kind, version, root string, exit Code) Envelope {
	return Envelope{
		Schema:  EnvelopeSchema,
		Kind:    kind,
		Version: version,
		Root:    root,
		Exit:    exit,
	}
}

// WriteJSON encodes the envelope as one JSON object followed by a newline.
//
// The output is deterministic: equal values produce equal bytes, so a golden
// file and a content hash are both usable against it. Data has to marshal
// deterministically for that to hold end to end, which a struct and a map both
// do — [encoding/json] emits struct members in declaration order and map
// members in sorted key order.
//
// The object is built in memory before any byte reaches w, so a payload that
// cannot be marshalled fails without leaving a truncated object on the wire
// for a consumer to choke on. HTML escaping is off: a root path holding an
// ampersand travels as itself rather than as an escape.
//
// A write to a pipe whose reader is gone is returned rather than swallowed;
// test for it with [IsBrokenPipe].
func (e Envelope) WriteJSON(w io.Writer) error {
	if e.Schema == "" {
		e.Schema = EnvelopeSchema
	}
	if e.Diagnostics == nil {
		// A nil slice encodes as null, and the member is contracted as an
		// array: a consumer that iterates it must not have to test the key
		// first.
		e.Diagnostics = []diag.Diagnostic{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return fmt.Errorf("encode %s envelope: %w", e.Kind, err)
	}
	if _, err := w.Write(escapeInvisible(buf.Bytes())); err != nil {
		return fmt.Errorf("write %s envelope: %w", e.Kind, err)
	}
	return nil
}
