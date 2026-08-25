package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

var testInstant = time.Date(2026, time.August, 24, 13, 45, 5, 0, time.UTC)

// records decodes every line the stream wrote, failing on a line that is not
// one complete JSON object.
func records(t *testing.T, out string) []Record {
	t.Helper()
	if out == "" {
		return nil
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("stream does not end with a newline: %q", out)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	got := make([]Record, 0, len(lines))
	for i, line := range lines {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not a record: %v: %q", i+1, err, line)
		}
		got = append(got, rec)
	}
	return got
}

func TestStreamKinds(t *testing.T) {
	got := StreamKinds()
	want := []string{RecordChange, RecordStatus, RecordError}
	if !slices.Equal(got, want) {
		t.Errorf("StreamKinds = %v, want %v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, k := range got {
		if k == "" {
			t.Error("StreamKinds holds an empty kind")
		}
		if seen[k] {
			t.Errorf("StreamKinds repeats %q", k)
		}
		seen[k] = true
	}
}

func TestStreamWriteRecord(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)

	if got := s.Seq(); got != 0 {
		t.Errorf("Seq before the first write = %d, want 0", got)
	}

	payload := map[string]any{"path": "agent-a/state.json", "op": "write"}
	if err := s.Write(RecordChange, "agent-a/state.json@7", testInstant, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := s.Seq(); got != 1 {
		t.Errorf("Seq after one write = %d, want 1", got)
	}

	got := records(t, buf.String())
	if len(got) != 1 {
		t.Fatalf("wrote %d records, want 1", len(got))
	}
	rec := got[0]
	if rec.Schema != StreamSchema {
		t.Errorf("schema = %q, want %q", rec.Schema, StreamSchema)
	}
	if rec.Kind != RecordChange {
		t.Errorf("kind = %q, want %q", rec.Kind, RecordChange)
	}
	if rec.Seq != 1 {
		t.Errorf("seq = %d, want 1", rec.Seq)
	}
	if rec.DedupKey != "agent-a/state.json@7" {
		t.Errorf("dedup_key = %q", rec.DedupKey)
	}
	if rec.Time != "2026-08-24T13:45:05Z" {
		t.Errorf("time = %q, want the RFC 3339 rendering of the instant passed in", rec.Time)
	}
	data, ok := rec.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want an object", rec.Data)
	}
	if data["path"] != "agent-a/state.json" || data["op"] != "write" {
		t.Errorf("data = %v", data)
	}
}

func TestStreamSequenceIsDense(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)

	for i := range 25 {
		if err := s.Write(RecordStatus, "", testInstant.Add(time.Duration(i)*time.Second), i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if got := s.Seq(); got != 25 {
		t.Errorf("Seq = %d, want 25", got)
	}

	got := records(t, buf.String())
	if len(got) != 25 {
		t.Fatalf("wrote %d records, want 25", len(got))
	}
	for i, rec := range got {
		if rec.Seq != uint64(i)+1 {
			t.Errorf("record %d carries seq %d, want %d", i, rec.Seq, i+1)
		}
	}
}

func TestStreamOmitsUnsetMembers(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)
	if err := s.Write(RecordStatus, "", testInstant, nil); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSuffix(buf.Bytes(), []byte("\n")), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, member := range []string{"dedup_key", "data"} {
		if _, present := got[member]; present {
			t.Errorf("unset member %q was encoded", member)
		}
	}
	for _, member := range []string{"schema", "kind", "seq", "time"} {
		if _, present := got[member]; !present {
			t.Errorf("member %q is missing, so a consumer cannot frame the record", member)
		}
	}
}

func TestStreamTimeCarriesOffset(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"utc", time.Date(2026, time.August, 24, 13, 45, 5, 0, time.UTC), "2026-08-24T13:45:05Z"},
		{
			"west of utc",
			time.Date(2026, time.August, 24, 6, 45, 5, 0, time.FixedZone("PDT", -7*60*60)),
			"2026-08-24T06:45:05-07:00",
		},
		{
			"east of utc",
			time.Date(2026, time.August, 24, 22, 15, 5, 0, time.FixedZone("IST", 5*60*60+30*60)),
			"2026-08-24T22:15:05+05:30",
		},
		{
			"sub-second",
			time.Date(2026, time.August, 24, 13, 45, 5, 123456789, time.UTC),
			"2026-08-24T13:45:05.123456789Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := NewStream(&buf).Write(RecordChange, "", tt.at, nil); err != nil {
				t.Fatalf("write: %v", err)
			}
			got := records(t, buf.String())
			if got[0].Time != tt.want {
				t.Errorf("time = %q, want %q", got[0].Time, tt.want)
			}
			if _, err := time.Parse(time.RFC3339, got[0].Time); err != nil {
				t.Errorf("time is not RFC 3339: %v", err)
			}
		})
	}
}

func TestStreamConcurrentSequenceHasNoGapsOrDuplicates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			producers = 8
			perWriter = 32
		)

		var buf bytes.Buffer
		s := NewStream(&buf)

		var wg sync.WaitGroup
		errs := make([]error, producers)
		for p := range producers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var high uint64
				for i := range perWriter {
					err := s.Write(RecordChange, "", testInstant, map[string]any{"producer": p, "i": i})
					if err != nil {
						errs[p] = err
						return
					}
					// Reading Seq while the other producers are writing is
					// what exercises its own lock; reading it only after they
					// finish would leave the accessor unsynchronized.
					n := s.Seq()
					if n < 1 || n > producers*perWriter {
						t.Errorf("producer %d read seq %d, outside 1..%d", p, n, producers*perWriter)
					}
					if n < high {
						t.Errorf("producer %d read seq %d after %d, so the counter went backwards", p, n, high)
					}
					high = n
				}
			}()
		}
		wg.Wait()

		for p, err := range errs {
			if err != nil {
				t.Fatalf("producer %d: %v", p, err)
			}
		}

		got := records(t, buf.String())
		if len(got) != producers*perWriter {
			t.Fatalf("wrote %d records, want %d", len(got), producers*perWriter)
		}
		if final := s.Seq(); final != uint64(producers*perWriter) {
			t.Errorf("Seq = %d, want %d", final, producers*perWriter)
		}

		seen := make(map[uint64]bool, len(got))
		for i, rec := range got {
			if seen[rec.Seq] {
				t.Fatalf("record %d reuses seq %d", i, rec.Seq)
			}
			seen[rec.Seq] = true
			// Holding the lock across the write is what makes the byte order
			// on the wire the sequence order; a consumer relies on it to spot
			// a gap without buffering.
			if rec.Seq != uint64(i)+1 {
				t.Fatalf("record %d carries seq %d, so the lines are out of order", i, rec.Seq)
			}
		}
		for n := uint64(1); n <= uint64(producers*perWriter); n++ {
			if !seen[n] {
				t.Errorf("seq %d is missing", n)
			}
		}
	})
}

func TestStreamFailedWriteDoesNotReuseSequence(t *testing.T) {
	w := &flakyWriter{failOn: 2, err: errors.New("no space left on device")}
	s := NewStream(w)

	if err := s.Write(RecordChange, "a", testInstant, nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := s.Write(RecordChange, "b", testInstant, nil); err == nil {
		t.Fatal("second write succeeded against a failing writer")
	}
	if got := s.Seq(); got != 2 {
		t.Errorf("Seq after a failed write = %d, want 2: a consumed number is not returned to the pool", got)
	}
	if err := s.Write(RecordChange, "c", testInstant, nil); err != nil {
		t.Fatalf("third write: %v", err)
	}

	got := records(t, w.String())
	if len(got) != 2 {
		t.Fatalf("wrote %d records, want 2", len(got))
	}
	if got[0].Seq != 1 || got[1].Seq != 3 {
		t.Errorf("sequences %d and %d, want 1 and 3 so the lost record shows as a gap", got[0].Seq, got[1].Seq)
	}
	if got[1].DedupKey != "c" {
		t.Errorf("the record after the failure is %q, want %q", got[1].DedupKey, "c")
	}
}

func TestStreamUnmarshalablePayloadEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)

	err := s.Write(RecordStatus, "k", testInstant, make(chan int))
	if err == nil {
		t.Fatal("encoding a channel payload succeeded")
	}
	if !strings.Contains(err.Error(), RecordStatus) {
		t.Errorf("error does not name the kind: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a failed encode wrote %d bytes: %q", buf.Len(), buf.String())
	}

	// The stream stays usable: the failed record consumed its number and the
	// next one continues from there.
	if err := s.Write(RecordStatus, "k", testInstant, "recovered"); err != nil {
		t.Fatalf("write after a failed encode: %v", err)
	}
	got := records(t, buf.String())
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("got %+v, want one record at seq 2", got)
	}
}

func TestStreamReportsWriterError(t *testing.T) {
	sentinel := errors.New("device is on fire")
	err := NewStream(errWriter{err: sentinel}).Write(RecordChange, "", testInstant, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Write = %v, want an error wrapping the writer's", err)
	}
	if IsBrokenPipe(err) {
		t.Error("an unrelated write error was reported as a broken pipe")
	}
}

func TestStreamReportsBrokenPipe(t *testing.T) {
	err := NewStream(errWriter{err: syscall.EPIPE}).Write(RecordChange, "", testInstant, nil)
	if err == nil {
		t.Fatal("writing to a closed pipe succeeded")
	}
	if !IsBrokenPipe(err) {
		t.Errorf("IsBrokenPipe(%v) = false", err)
	}
}

// A path or a dedup key holding an ampersand or an angle bracket travels as
// itself. A consumer that matches record fields against a workspace path
// cannot match one rewritten into \u0026.
func TestStreamDoesNotEscapeHTML(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"path": "agent-a/<b>&<c>.json"}
	if err := NewStream(&buf).Write(RecordChange, "agent-a/b&c<d>@1", testInstant, payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	out := buf.String()
	for _, want := range []string{`"agent-a/b&c<d>@1"`, `"agent-a/<b>&<c>.json"`} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is not in the record verbatim: %s", want, out)
		}
	}
	for _, escape := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(out, escape) {
			t.Errorf("the record carries the HTML escape %s: %s", escape, out)
		}
	}
}

// A payload that is already JSON is compacted on the way out, so the newlines
// a raw message carries between its members cannot become record boundaries.
func TestStreamRawPayloadCannotBreakFraming(t *testing.T) {
	var buf bytes.Buffer
	raw := json.RawMessage("{\n  \"path\": \"agent-a/state.json\",\n  \"note\": \"one\\ntwo\"\n}")
	if err := NewStream(&buf).Write(RecordStatus, "k", testInstant, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("output holds %d newlines, want the framing one: %q", n, buf.String())
	}

	got := records(t, buf.String())
	data, ok := got[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want an object", got[0].Data)
	}
	if data["note"] != "one\ntwo" {
		t.Errorf("note = %q, want the newline preserved inside the string", data["note"])
	}
}

// A raw payload that is not JSON fails its record, and the encoder the stream
// reuses across records does not carry the failure into the next one.
func TestStreamInvalidRawPayloadEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf)

	if err := s.Write(RecordStatus, "k", testInstant, json.RawMessage("{\"note\": \"lit\neral\"}")); err == nil {
		t.Fatal("an unparseable raw payload encoded without error")
	}
	if buf.Len() != 0 {
		t.Fatalf("a failed encode wrote %d bytes: %q", buf.Len(), buf.String())
	}

	if err := s.Write(RecordStatus, "k2", testInstant, "recovered"); err != nil {
		t.Fatalf("the stream did not survive an unparseable payload: %v", err)
	}
	got := records(t, buf.String())
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("got %+v, want one record at seq 2", got)
	}
}

func FuzzStreamWrite(f *testing.F) {
	f.Add(RecordChange, "agent-a/state.json@1", "payload")
	f.Add("", "", "")
	f.Add("kind\nwith\nnewlines", "key\r\nwith\r\nbreaks", "data\nspanning\nlines")
	f.Add("\x00\x1b[2J\x07", "\\\"}", "{\"seq\":999}")
	f.Add("status", "\u202e", "\xff\xfe\x00")

	f.Fuzz(func(t *testing.T, kind, dedupKey, payload string) {
		var buf bytes.Buffer
		s := NewStream(&buf)

		for range 3 {
			if err := s.Write(kind, dedupKey, testInstant, payload); err != nil {
				t.Fatalf("write: %v", err)
			}
		}

		out := buf.Bytes()
		// Three records means three framing newlines and nothing else: a
		// consumer splits this stream on newlines, so a payload that
		// introduced one would silently become extra records.
		if n := bytes.Count(out, []byte("\n")); n != 3 {
			t.Fatalf("output holds %d newlines, want 3: %q", n, out)
		}
		if out[len(out)-1] != '\n' {
			t.Fatalf("output does not end with the framing newline: %q", out)
		}

		got := records(t, buf.String())
		if len(got) != 3 {
			t.Fatalf("decoded %d records, want 3", len(got))
		}
		for i, rec := range got {
			if rec.Schema != StreamSchema {
				t.Errorf("record %d: schema = %q, want %q", i, rec.Schema, StreamSchema)
			}
			if rec.Seq != uint64(i)+1 {
				t.Errorf("record %d: seq = %d, want %d", i, rec.Seq, i+1)
			}
			if utf8.ValidString(kind) && rec.Kind != kind {
				t.Errorf("record %d: kind = %q, want %q", i, rec.Kind, kind)
			}
			if utf8.ValidString(dedupKey) && rec.DedupKey != dedupKey {
				t.Errorf("record %d: dedup_key = %q, want %q", i, rec.DedupKey, dedupKey)
			}
		}
	})
}

// A record travels the same boundary an envelope does: a program reads it and
// may print a member of it into a terminal. A right-to-left override left raw
// reverses everything the consumer prints after it.
func TestStreamEscapesTheRunesATerminalReordersAround(t *testing.T) {
	// U+202E, the right-to-left override.
	const spoofed = "agent-\u202egpj.exe"

	var buf bytes.Buffer
	if err := NewStream(&buf).Write(RecordChange, spoofed+"\x00modify", testInstant,
		map[string]string{"path": spoofed}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if strings.ContainsRune(buf.String(), '\u202e') {
		t.Errorf("a right-to-left override reached the stream raw: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `\u202e`) {
		t.Errorf("the override was not escaped: %s", buf.String())
	}

	// Escaping keeps the value recoverable: a consumer that decodes the line
	// gets the rune the workspace wrote.
	got := records(t, buf.String())
	data, ok := got[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want an object", got[0].Data)
	}
	if data["path"] != spoofed {
		t.Errorf("path = %q, want the rune back after decoding", data["path"])
	}
}
