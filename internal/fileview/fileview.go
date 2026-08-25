// Package fileview reads a bounded window of one workspace file and gives its
// lines syntactic spans.
//
// Two bounds are structural. A window holds at most [Options.MaxBytes]
// regardless of the file's size, so a log larger than memory is viewable and
// opening one costs the window rather than the file. And
// [Window.Follow] re-reads only the range appended since the last read, so
// following a growing file costs the appended bytes rather than the file — the
// same read also proves the file is still the file the window holds, which is
// how a truncation and a rotation are told apart from an append.
//
// Every line is passed through [github.com/stxkxs/agentfs/internal/textx.Sanitize]
// before it is stored, so no byte a renderer receives from this package can
// drive the terminal.
package fileview

import (
	"bytes"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/textx"
)

// DefaultMaxBytes is the window ceiling applied when [Options.MaxBytes] is not
// set.
const DefaultMaxBytes = 1 << 20

// MaxLines is the number of display lines a window holds. A window of empty
// lines costs far more in line structure than in the bytes it came from, so the
// line count is bounded independently of the byte count and any elision it
// causes is reported through [Window.Truncated].
const MaxLines = 1 << 16

// MaxSpans is the number of spans one display line carries. A line of
// punctuation yields a span per byte, so a single minified document line costs
// the renderer orders of magnitude more in span structure than in text. Bytes
// past the ceiling carry no role, which no reader sees: the ceiling is far
// beyond the number of tokens a terminal row displays.
const MaxSpans = 1 << 10

// sniffBytes is the prefix of a file [DetectKind] classifies from.
const sniffBytes = 8 << 10

// anchorBytes is the trailing run of a window that [Window.Follow] re-reads to
// decide whether the file still holds the bytes the window was built from.
const anchorBytes = 64

// ErrNotRegular reports that a path names something other than a regular file,
// so it has no content to window.
var ErrNotRegular = errors.New("fileview: not a regular file")

// Kind is the detected content type of a file.
type Kind int

// The content types the viewer distinguishes.
const (
	// KindPlain is text with no recognized structure.
	KindPlain Kind = iota
	// KindJSON is a single JSON document, possibly spread over many lines.
	KindJSON
	// KindYAML is a YAML document.
	KindYAML
	// KindNDJSON is one JSON document per line.
	KindNDJSON
	// KindLog is a line-oriented log.
	KindLog
	// KindBinary is content that is not text. It is described rather than
	// displayed.
	KindBinary
)

// String returns the lowercase name of the kind.
func (k Kind) String() string {
	switch k {
	case KindPlain:
		return plainName
	case KindJSON:
		return "json"
	case KindYAML:
		return "yaml"
	case KindNDJSON:
		return "ndjson"
	case KindLog:
		return "log"
	case KindBinary:
		return "binary"
	default:
		return plainName
	}
}

// Line is one display line: text that is safe to render, and the spans that
// give its bytes syntactic roles.
type Line struct {
	// Text is the line's content, sanitized by textx.Sanitize.
	Text string
	// Spans are ascending, non-overlapping byte ranges of Text, at most
	// [MaxSpans] of them.
	Spans []Span
}

// Options bounds a read.
type Options struct {
	// MaxBytes is the most the window ever holds. A value at or below zero
	// applies [DefaultMaxBytes].
	MaxBytes int64
	// Tail holds the end of the file rather than the beginning, which is what
	// a reader of a log wants and what [Window.Follow] extends.
	Tail bool
	// RedactKeys names the JSON members whose string values are replaced with
	// [Mask] before the line is lexed. A workspace is rendered onto whatever
	// screen the operator is sharing, and a credential an agent wrote into its
	// own state should not be put there by the tool watching it.
	RedactKeys []string
}

// Window is the portion of a file held in memory.
//
// A Window is not safe for concurrent use: [Window.Follow] mutates it, and the
// slices the accessors return alias what the next Follow rebuilds.
type Window struct {
	max    int64
	tail   bool
	redact []string

	kind   Kind
	buf    []byte
	lines  []Line
	offset int64
	size   int64
	err    error

	// split is the index in buf just past the last byte already turned into a
	// complete line, and partial reports whether the final entry of lines was
	// built from the bytes after it. Together they let an append extend the
	// lines instead of rebuilding them.
	split   int
	partial bool

	headCut bool
	tailCut bool
}

// Lines returns the window's display lines.
func (w *Window) Lines() []Line { return w.lines }

// Kind returns the file's detected content type.
func (w *Window) Kind() Kind { return w.kind }

// Size returns the file's size at the last read.
func (w *Window) Size() int64 { return w.size }

// Offset returns the byte offset in the file where the held window begins.
func (w *Window) Offset() int64 { return w.offset }

// Truncated reports whether content was elided at each end of the window.
func (w *Window) Truncated() (head, tail bool) { return w.headCut, w.tailCut }

// Err returns the error that left the window empty, or nil.
func (w *Window) Err() error { return w.err }

// Load reads a bounded window of name through root.
//
// It always returns a window: one whose [Window.Err] is set holds no lines and
// is the argument to a later [Window.Follow], which retries the read.
func Load(root *fsx.Root, name string, opts Options) *Window {
	w := &Window{max: opts.MaxBytes, tail: opts.Tail, redact: opts.RedactKeys}
	if w.max <= 0 {
		w.max = DefaultMaxBytes
	}
	w.load(root, name)
	return w
}

// Follow extends w with the bytes appended to name since the last read.
//
// The read that fetches the appended range also re-reads the window's trailing
// bytes. A file that shrank, or whose trailing bytes no longer match what the
// window holds, was truncated or rotated rather than appended to, and w is
// reloaded from the file's content instead of being extended with bytes that
// belong to a different file.
func (w *Window) Follow(root *fsx.Root, name string) error {
	fsys, err := rootFS(root)
	if err != nil {
		w.err = err
		return err
	}
	if w.err != nil || len(w.buf) == 0 {
		w.load(root, name)
		return w.err
	}
	info, err := fsys.Stat(name)
	if err != nil {
		w.err = err
		return err
	}
	if !info.Mode().IsRegular() {
		w.err = ErrNotRegular
		return w.err
	}
	grew := info.Size() - (w.offset + int64(len(w.buf)))
	if grew < 0 || (w.tail && grew > w.max) {
		w.load(root, name)
		return w.err
	}
	return w.extend(root, name, grew)
}

// extend reads the window's anchor and the grew bytes beyond it in one range
// read, and applies the appended bytes when the anchor still matches.
func (w *Window) extend(root *fsx.Root, name string, grew int64) error {
	room := grew
	if !w.tail {
		if free := w.max - int64(len(w.buf)); room > free {
			room = free
		}
	}
	anchor := int64(len(w.buf))
	if anchor > anchorBytes {
		anchor = anchorBytes
	}
	buf := make([]byte, anchor+room)
	n, size, err := root.ReadRange(name, w.offset+int64(len(w.buf))-anchor, buf)
	if err != nil {
		w.err = err
		return err
	}
	if int64(n) < anchor || !bytes.Equal(buf[:anchor], w.buf[int64(len(w.buf))-anchor:]) {
		w.load(root, name)
		return w.err
	}
	w.size = size
	held := len(w.buf)
	// A changed kind gives every held line different spans, so it invalidates
	// the display lines exactly as an eviction does.
	stale := w.absorb(buf[anchor:n])
	if w.reclassify(name, held) {
		stale = true
	}
	if stale {
		w.rebuild()
	} else {
		w.extendLines()
	}
	return nil
}

// absorb appends add to the window, evicting from the front once the window is
// full so that a followed file keeps its most recent bytes. It reports whether
// it evicted, because eviction moves every byte the display lines were built
// from and so invalidates them.
func (w *Window) absorb(add []byte) (evicted bool) {
	if len(add) == 0 {
		return false
	}
	total := int64(len(w.buf)) + int64(len(add))
	if total <= w.max {
		w.buf = append(w.buf, add...)
		return false
	}
	drop := total - w.max
	if drop >= int64(len(w.buf)) {
		w.buf = append(w.buf[:0], add[drop-int64(len(w.buf)):]...)
	} else {
		kept := copy(w.buf, w.buf[drop:])
		w.buf = append(w.buf[:kept], add...)
	}
	w.offset += drop
	return true
}

// load fills the window from the file's content, reusing the buffer the window
// already owns so a reload costs no allocation proportional to the window.
func (w *Window) load(root *fsx.Root, name string) {
	w.reset()
	fsys, err := rootFS(root)
	if err != nil {
		w.err = err
		return
	}
	info, err := fsys.Stat(name)
	if err != nil {
		w.err = err
		return
	}
	if !info.Mode().IsRegular() {
		w.err = ErrNotRegular
		return
	}
	off := int64(0)
	if w.tail && info.Size() > w.max {
		off = info.Size() - w.max
	}
	span := info.Size() - off
	if span > w.max {
		span = w.max
	}
	buf := w.buf[:0]
	if int64(cap(buf)) < span {
		buf = make([]byte, span)
	}
	n, size, err := root.ReadRange(name, off, buf[:span])
	if err != nil {
		w.err = err
		return
	}
	w.buf, w.offset, w.size = buf[:n], off, size
	w.kind = w.detect(root, name)
	w.rebuild()
}

// reset returns the window to the state Load starts from, keeping the buffer's
// capacity.
func (w *Window) reset() {
	w.buf = w.buf[:0]
	w.lines = w.lines[:0]
	w.kind = KindPlain
	w.split, w.partial = 0, false
	w.offset, w.size = 0, 0
	w.headCut, w.tailCut = false, false
	w.err = nil
}

// detect classifies the file from its first bytes.
//
// A kind is a claim about the whole file, and the bytes a window holds need not
// carry the NUL that settles it: a window of the file's end holds none of the
// prefix, and a window whose ceiling falls below [sniffBytes] holds only part
// of it. Both read the prefix on its own. A read that fails leaves whichever
// part of it the window holds, so the kind falls back to the name rather than
// the load failing.
func (w *Window) detect(root *fsx.Root, name string) Kind {
	head := w.buf
	if w.offset > 0 {
		head = nil
	}
	if len(head) < sniffBytes && int64(len(head)) < w.size {
		if full := readHead(root, name); len(full) > len(head) {
			head = full
		}
	}
	return DetectKind(name, head)
}

// reclassify re-reads the kind once an append has extended the prefix a kind is
// claimed from, and reports whether the kind changed. A file classified from
// the few bytes that existed when it was opened is claimed by the bytes that
// follow them, so an artifact whose NUL arrives after the first read is
// described rather than rendered. A window that no longer begins at the file's
// start, or that already held the whole prefix, keeps the kind it has.
func (w *Window) reclassify(name string, held int) bool {
	if w.offset > 0 || held >= sniffBytes || len(w.buf) == held {
		return false
	}
	kind := DetectKind(name, w.buf)
	if kind == w.kind {
		return false
	}
	w.kind = kind
	return true
}

// readHead returns the file's classification prefix, and nothing when the read
// fails. A kind taken from the name alone still displays the file, so a failure
// here does not fail the load.
func readHead(root *fsx.Root, name string) []byte {
	buf := make([]byte, sniffBytes)
	n, _, err := root.ReadRange(name, 0, buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

// rebuild recomputes every display line from the held bytes.
func (w *Window) rebuild() {
	w.mark()
	if w.kind == KindBinary {
		w.lines = []Line{{Text: binaryText(w.size)}}
		w.split, w.partial = len(w.buf), false
		return
	}
	w.split, w.partial = 0, false
	if w.headCut {
		// A window that does not begin at the file's start begins inside a
		// line, and half a line displayed as a whole one is a lie about the
		// file.
		if i := bytes.IndexByte(w.buf, '\n'); i >= 0 {
			w.split = i + 1
		}
	}
	w.lines = w.lines[:0]
	w.appendLines()
	w.capLines()
}

// extendLines brings the display lines up to date with bytes appended to the
// window, leaving the lines built before them untouched.
func (w *Window) extendLines() {
	w.mark()
	if w.kind == KindBinary {
		w.lines = []Line{{Text: binaryText(w.size)}}
		w.split = len(w.buf)
		return
	}
	w.appendLines()
	w.capLines()
}

// mark records which ends of the file the window elides.
func (w *Window) mark() {
	w.headCut = w.offset > 0
	w.tailCut = w.offset+int64(len(w.buf)) < w.size
}

// appendLines turns the bytes after the last complete line into display lines.
// The run without a trailing newline is a line too: a log is read while it is
// being written, and withholding its last line hides the entry a reader is
// waiting for. That line is rebuilt on the next call, when the rest of it has
// arrived.
func (w *Window) appendLines() {
	if w.partial {
		w.lines = w.lines[:len(w.lines)-1]
		w.partial = false
	}
	for w.split < len(w.buf) {
		rest := w.buf[w.split:]
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			w.lines = append(w.lines, w.line(rest))
			w.partial = true
			return
		}
		w.lines = append(w.lines, w.line(rest[:i]))
		w.split += i + 1
	}
}

// capLines holds the window to [MaxLines], keeping the end a follower reads and
// the start a reader of a document reads.
func (w *Window) capLines() {
	if len(w.lines) <= MaxLines {
		return
	}
	if w.tail {
		w.lines = append(w.lines[:0], w.lines[len(w.lines)-MaxLines:]...)
		w.headCut = true
		return
	}
	w.lines = w.lines[:MaxLines]
	// The bytes past the ceiling are accounted for as read, so a later append
	// does not build lines this window has no room to show.
	w.split, w.partial = len(w.buf), false
	w.tailCut = true
}

// line builds one display line from its raw bytes.
func (w *Window) line(raw []byte) Line {
	text := textx.Sanitize(strings.TrimSuffix(string(raw), "\r"))
	if len(w.redact) > 0 {
		text = Redact(text, w.redact)
	}
	return Line{Text: text, Spans: Highlight(w.kind, text)}
}

func rootFS(root *fsx.Root) (fsx.FS, error) {
	if root == nil {
		return nil, fsx.ErrRootLost
	}
	fsys := root.FS()
	if fsys == nil {
		return nil, fsx.ErrRootLost
	}
	return fsys, nil
}

// binaryText is the one line a binary file is displayed as. Rendering the bytes
// themselves produces mojibake that says less than the file's size does.
func binaryText(size int64) string {
	return "binary content, " + humanBytes(size) + ", not displayed"
}

var byteUnits = [...]string{"KiB", "MiB", "GiB", "TiB"}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < len(byteUnits)-1 {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + byteUnits[exp]
}

// DetectKind reports the content type of name from its extension and the first
// bytes of its content.
//
// A NUL within the first 8 KiB makes the file binary whatever its name says,
// because content is a stronger claim about content than a name is. Beyond
// that, an extension the convention defines settles the kind, and a file
// without one is classified from the shape of its first lines.
func DetectKind(name string, head []byte) Kind {
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return KindBinary
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".ndjson", ".jsonl":
		return KindNDJSON
	case ".json":
		if looksNDJSON(head) {
			return KindNDJSON
		}
		return KindJSON
	case ".yaml", ".yml":
		return KindYAML
	case ".log":
		return KindLog
	case ".txt", ".md":
		return KindPlain
	default:
		return sniff(head)
	}
}

// sniff classifies content whose name carries no extension the convention
// defines.
func sniff(head []byte) Kind {
	lines := sniffLines(head, 8)
	if len(lines) == 0 {
		return KindPlain
	}
	if looksNDJSON(head) {
		return KindNDJSON
	}
	switch lines[0][0] {
	case '{', '[':
		return KindJSON
	case '-':
		if lines[0] == yamlStartMarker {
			return KindYAML
		}
	}
	switch {
	case countMatch(lines, isYAMLLine)*2 >= len(lines):
		return KindYAML
	case countMatch(lines, isLogLine)*2 >= len(lines):
		return KindLog
	default:
		return KindPlain
	}
}

// sniffLines returns up to want non-blank lines of head, trimmed. It walks the
// prefix a line at a time and stops at want, so classifying an 8 KiB prefix
// costs the lines it reads rather than every line the prefix holds.
func sniffLines(head []byte, want int) []string {
	out := make([]string, 0, want)
	for len(out) < want && len(head) > 0 {
		raw := head
		if i := bytes.IndexByte(head, '\n'); i >= 0 {
			raw, head = head[:i], head[i+1:]
		} else {
			head = nil
		}
		if s := strings.TrimSpace(string(raw)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// looksNDJSON reports whether the head holds one JSON object per line. The last
// sniffed line may be cut by the sniff ceiling, so it is allowed to be
// incomplete while every line before it must close.
func looksNDJSON(head []byte) bool {
	lines := sniffLines(head, 5)
	if len(lines) < 2 {
		return false
	}
	closed := 0
	for i, line := range lines {
		if line[0] != '{' {
			return false
		}
		switch {
		case line[len(line)-1] == '}':
			closed++
		case i != len(lines)-1:
			return false
		}
	}
	return closed >= 2
}

func countMatch(lines []string, pred func(string) bool) int {
	n := 0
	for _, line := range lines {
		if pred(line) {
			n++
		}
	}
	return n
}

// isYAMLLine reports whether a trimmed line has the shape of block YAML.
func isYAMLLine(line string) bool {
	switch {
	case line == yamlStartMarker || line == yamlEndMarker:
		return true
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "#"):
		return true
	case !isLetter(line[0]) && line[0] != '_':
		return false
	}
	i := 0
	for i < len(line) && isKeyByte(line[i]) {
		i++
	}
	return i < len(line) && line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ')
}

func isKeyByte(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '_' || c == '-' || c == '.'
}

// isLogLine reports whether a trimmed line opens with a timestamp or carries a
// severity word among its leading fields.
func isLogLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	if looksTimestamp(fields[0]) {
		return true
	}
	for i, f := range fields {
		if i == 4 {
			break
		}
		if _, ok := logLevels[strings.ToLower(strings.Trim(f, "[]():"))]; ok {
			return true
		}
	}
	return false
}

// looksTimestamp reports whether a field has the shape of a date or a clock
// time: digit-led and separated.
func looksTimestamp(field string) bool {
	if len(field) < 8 || !isDigit(field[0]) {
		return false
	}
	return strings.ContainsAny(field, "-:")
}
