package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// StreamSchema identifies the change-stream record format. It versions
// independently of [EnvelopeSchema] because the two are consumed by different
// programs: a stream reader is long-lived and a one-shot reader is not.
const StreamSchema = "agentfs/stream/v1"

// Record kinds. The kind selects the shape of a record's payload.
//
// A record reports that a path changed, not what the path now holds: the
// consumer re-reads. That is what keeps the vocabulary this short and the
// producer's cost independent of what the workspace writes.
const (
	// RecordChange reports that a workspace path changed.
	RecordChange = "change"
	// RecordStatus reports the stream's own condition: what it is watching and
	// what it has fallen behind on.
	RecordStatus = "status"
	// RecordError reports a fault the producer could not resolve.
	RecordError = "error"
)

// StreamKinds returns the record kind vocabulary in the order the reference
// lists it.
//
// The table is the vocabulary: a producer writes one of these constants rather
// than a string of its own, and a kind no producer writes is one a consumer
// waits for forever. TestEveryRecordKindIsEmitted holds both directions.
func StreamKinds() []string {
	return []string{RecordChange, RecordStatus, RecordError}
}

// Record is one line of the NDJSON change stream.
//
// The stream is at-least-once: a consumer that reconnects, or that reads a
// stream whose producer restarted, sees records it has already processed. The
// two identifiers answer different questions. Seq orders the records one
// producer emitted and exposes a loss as a gap. DedupKey names the underlying
// event, so a consumer discards a repeat by key — a producer that restarts
// begins its sequence again but emits the same keys.
type Record struct {
	// Schema is [StreamSchema].
	Schema string `json:"schema"`
	// Kind selects the shape of Data. See [StreamKinds].
	Kind string `json:"kind"`
	// Seq is the producer's record ordinal. The first record is 1.
	Seq uint64 `json:"seq"`
	// Time is the instant the record reports, RFC 3339 with its offset.
	Time string `json:"time"`
	// DedupKey identifies the event the record reports, empty for a record
	// that reports no repeatable event.
	DedupKey string `json:"dedup_key,omitempty"`
	// Data is the payload the kind selects.
	Data any `json:"data,omitempty"`
}

// Stream writes NDJSON records to a writer, stamping each with the next
// sequence number.
//
// Use [NewStream]: the zero Stream carries neither a writer nor an encoder.
//
// A Stream is safe for concurrent use by several producers. Taking the
// sequence number and writing the line happen under one lock, so the numbers a
// consumer reads arrive in order and two producers never interleave halves of
// a line into the same stream.
type Stream struct {
	mu  sync.Mutex
	w   io.Writer
	buf bytes.Buffer
	enc *json.Encoder
	seq uint64
}

// NewStream returns a stream that writes records to w.
func NewStream(w io.Writer) *Stream {
	s := &Stream{w: w}
	s.enc = json.NewEncoder(&s.buf)
	s.enc.SetEscapeHTML(false)
	return s
}

// Write encodes one record of the given kind and writes it as a single line.
//
// The sequence number is taken before the line is written and is never reused,
// so a write that fails leaves a gap rather than a duplicate: a consumer can
// detect a lost record from a gap, and cannot detect anything from a number it
// has already accepted.
//
// at is the instant the record reports. Write never reads the clock, so the
// timestamps a stream carries are the caller's to control and to test.
//
// Encoding happens in memory, so a payload that cannot be marshalled fails
// without emitting a partial line, and nothing a caller passes can break the
// newline framing — every byte of kind, dedupKey and data is JSON-escaped on
// the way out, and the runes a terminal reorders around are escaped past what
// the JSON encoder does.
//
// A write to a pipe whose reader is gone is returned rather than swallowed;
// test for it with [IsBrokenPipe].
func (s *Stream) Write(kind, dedupKey string, at time.Time, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq++
	rec := Record{
		Schema:   StreamSchema,
		Kind:     kind,
		Seq:      s.seq,
		Time:     at.Format(time.RFC3339Nano),
		DedupKey: dedupKey,
		Data:     data,
	}

	s.buf.Reset()
	if err := s.enc.Encode(rec); err != nil {
		return fmt.Errorf("encode %s record %d: %w", kind, rec.Seq, err)
	}
	if _, err := s.w.Write(escapeInvisible(s.buf.Bytes())); err != nil {
		return fmt.Errorf("write %s record %d: %w", kind, rec.Seq, err)
	}
	return nil
}

// Seq returns the highest sequence number issued, and zero before the first
// write. A write that failed consumed its number, so the value can name a
// record no consumer ever received.
func (s *Stream) Seq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}
