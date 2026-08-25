// Package diag is the diagnostic vocabulary every layer of agentfs reports
// through.
//
// A diagnostic is a machine-readable finding about one workspace document: a
// stable code, a severity, an RFC 6901 pointer to the offending member, and a
// hint that names the edit which resolves it. Consumers branch on Code and
// Pointer; Message and Hint are prose for a human and carry no contract.
package diag

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/textx"
)

// Severity ranks a diagnostic. A document carrying an Error diagnostic is not
// usable as state; Warning and Info documents are usable and annotated.
type Severity int

// Severity levels, ordered by increasing seriousness.
const (
	Info Severity = iota
	Warning
	Error
)

// String returns the lowercase wire form of the severity.
func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// MarshalText encodes the severity as its wire form.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Code identifies a diagnostic kind. Codes are permanent: a code is retired
// rather than reused, so a consumer that suppresses one never has it silently
// come to mean something else.
type Code string

// Diagnostic codes, grouped by the layer that raises them.
//
// 1xxx document well-formedness, 2xxx member typing, 3xxx member semantics,
// 4xxx observation, 5xxx resource ceilings.
const (
	CodeNotJSON          Code = "AFS1001"
	CodeNotObject        Code = "AFS1002"
	CodeUnknownMember    Code = "AFS1003"
	CodeSchemaMissing    Code = "AFS1004"
	CodeSchemaUnknown    Code = "AFS1005"
	CodeExtraTooLarge    Code = "AFS1006"
	CodeLegacyFilename   Code = "AFS1007"
	CodeDocumentTooLarge Code = "AFS1008"

	CodeWrongType     Code = "AFS2001"
	CodeEmptyString   Code = "AFS2002"
	CodeStringTooLong Code = "AFS2003"
	CodeStepType      Code = "AFS2004"
	CodeStepNegative  Code = "AFS2005"

	CodeStatusMissing       Code = "AFS3001"
	CodeStatusUnknown       Code = "AFS3002"
	CodeTimeFuture          Code = "AFS3003"
	CodeTimeNotRFC3339      Code = "AFS3004"
	CodeTimeNoOffset        Code = "AFS3005"
	CodeStepBeyondTotal     Code = "AFS3006"
	CodeErrorWithoutProblem Code = "AFS3007"

	CodeUnreadable Code = "AFS4001"
	CodeStale      Code = "AFS4002"
	CodeSettling   Code = "AFS4010"

	CodeDiagnosticsDropped Code = "AFS5009"
	CodeRootLost           Code = "AFS5010"
	CodeRootRecovered      Code = "AFS5011"
	CodeNodeCeiling        Code = "AFS5012"
	CodeEntriesTruncated   Code = "AFS5013"
	CodeDepthTruncated     Code = "AFS5014"
	CodeWatchBudget        Code = "AFS5015"
	CodeBatchTruncated     Code = "AFS5016"
)

// CodeInfo describes one code. The registry is the single source for the
// generated diagnostic reference, so a code without an entry is a build
// failure rather than an undocumented code.
type CodeInfo struct {
	Code Code
	// Severity is the level the code is raised at unless a call site
	// downgrades it.
	Severity Severity
	// Summary is one sentence naming the condition.
	Summary string
	// Semantic marks a code that no JSON Schema validator can raise, so the
	// schema-agreement test knows to exclude it rather than fail on it.
	Semantic bool
}

var registry = map[Code]CodeInfo{
	CodeNotJSON:          {CodeNotJSON, Error, "The document is not well-formed JSON.", false},
	CodeNotObject:        {CodeNotObject, Error, "The document's top level is not a JSON object.", false},
	CodeUnknownMember:    {CodeUnknownMember, Info, "The document carries a member the contract does not define.", false},
	CodeSchemaMissing:    {CodeSchemaMissing, Warning, "The document omits the schema member, so it is read under the compatibility profile.", true},
	CodeSchemaUnknown:    {CodeSchemaUnknown, Error, "The document declares a schema version agentfs does not implement.", false},
	CodeExtraTooLarge:    {CodeExtraTooLarge, Warning, "Preserved unknown members exceed the per-document ceiling and were dropped.", true},
	CodeLegacyFilename:   {CodeLegacyFilename, Info, "The state document uses a compatibility filename rather than state.json.", true},
	CodeDocumentTooLarge: {CodeDocumentTooLarge, Error, "The document exceeds the per-document byte ceiling and was not read.", true},

	CodeWrongType:     {CodeWrongType, Error, "A member holds a JSON type the contract does not allow.", false},
	CodeEmptyString:   {CodeEmptyString, Warning, "A member holds an empty string where a value is expected.", false},
	CodeStringTooLong: {CodeStringTooLong, Warning, "A string member exceeds its length ceiling and was truncated for display.", false},
	CodeStepType:      {CodeStepType, Error, "The step member is neither a non-negative integer nor a string.", false},
	CodeStepNegative:  {CodeStepNegative, Error, "The step member holds a negative ordinal.", false},

	CodeStatusMissing:       {CodeStatusMissing, Error, "The document declares no status.", false},
	CodeStatusUnknown:       {CodeStatusUnknown, Error, "The status member holds a value outside the contract vocabulary.", false},
	CodeTimeFuture:          {CodeTimeFuture, Warning, "A timestamp lies beyond the observer's clock by more than the skew tolerance.", true},
	CodeTimeNotRFC3339:      {CodeTimeNotRFC3339, Error, "A timestamp member is not an RFC 3339 date-time.", false},
	CodeTimeNoOffset:        {CodeTimeNoOffset, Error, "A timestamp member omits its UTC offset, so it names no instant.", false},
	CodeStepBeyondTotal:     {CodeStepBeyondTotal, Warning, "The step ordinal exceeds steps_total.", true},
	CodeErrorWithoutProblem: {CodeErrorWithoutProblem, Warning, "The status is error but no problem is described.", true},

	CodeUnreadable: {CodeUnreadable, Error, "The document could not be read.", true},
	CodeStale:      {CodeStale, Warning, "The document has not been updated within its declared heartbeat.", true},
	CodeSettling:   {CodeSettling, Info, "The document changed between reads, so a well-formedness error is withheld pending a stable observation.", true},

	CodeDiagnosticsDropped: {CodeDiagnosticsDropped, Warning, "Retained findings reached the ceiling, so the remainder is counted rather than listed.", true},
	CodeRootLost:           {CodeRootLost, Error, "The workspace root became unreadable.", true},
	CodeRootRecovered:      {CodeRootRecovered, Info, "The workspace root became readable again.", true},
	CodeNodeCeiling:        {CodeNodeCeiling, Warning, "The retained tree reached the node ceiling, so what is held is a prefix of the workspace.", true},
	CodeEntriesTruncated:   {CodeEntriesTruncated, Warning, "A directory holds more entries than the per-directory ceiling.", true},
	CodeDepthTruncated:     {CodeDepthTruncated, Warning, "A subtree is deeper than the depth ceiling.", true},
	CodeWatchBudget:        {CodeWatchBudget, Warning, "The kernel watch budget is exhausted, so part of the tree is swept rather than watched.", true},
	CodeBatchTruncated:     {CodeBatchTruncated, Warning, "A change batch exceeded its ceiling, so a resynchronization is required.", true},
}

// Lookup returns the registry entry for a code.
func Lookup(c Code) (CodeInfo, bool) {
	info, ok := registry[c]
	return info, ok
}

// Codes returns every registered code in ascending order.
func Codes() []CodeInfo {
	out := make([]CodeInfo, 0, len(registry))
	for _, info := range registry {
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b CodeInfo) int { return cmp.Compare(a.Code, b.Code) })
	return out
}

// Diagnostic is one finding about one document.
type Diagnostic struct {
	Code     Code     `json:"code"`
	Severity Severity `json:"severity"`
	// Path is the workspace-relative path of the document, or empty for a
	// finding about the workspace itself.
	Path string `json:"path,omitempty"`
	// Pointer is an RFC 6901 JSON Pointer to the offending member, empty when
	// the finding is about the document as a whole.
	Pointer string `json:"pointer,omitempty"`
	// Line and Column are 1-based positions of Pointer within the document,
	// zero when the position is not resolvable.
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
	// Message names the condition.
	Message string `json:"message"`
	// Hint names the edit that resolves the finding. A hint is actionable:
	// applying it to the document makes the finding go away.
	Hint string `json:"hint,omitempty"`
	// Value is the offending value rendered for display, truncated.
	Value string `json:"value,omitempty"`
}

// String renders the diagnostic in the one-line form the text output uses.
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(string(d.Code))
	b.WriteByte(' ')
	b.WriteString(d.Severity.String())
	if d.Path != "" {
		b.WriteByte(' ')
		b.WriteString(d.Path)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", d.Line, d.Column)
		}
	}
	if d.Pointer != "" {
		b.WriteString(" at ")
		b.WriteString(d.Pointer)
	}
	b.WriteString(": ")
	b.WriteString(d.Message)
	if d.Hint != "" {
		b.WriteString(" — ")
		b.WriteString(d.Hint)
	}
	return b.String()
}

// Sink accumulates diagnostics for one document. The zero Sink is ready to use.
type Sink struct {
	path string
	src  []byte
	list []Diagnostic
}

// NewSink returns a sink that resolves pointers against src and stamps every
// diagnostic with path.
func NewSink(path string, src []byte) *Sink {
	return &Sink{path: path, src: src}
}

// Add records a diagnostic, filling in severity from the registry and
// resolving the pointer's position within the document.
func (s *Sink) Add(code Code, pointer, message, hint, value string) {
	info, ok := Lookup(code)
	sev := Error
	if ok {
		sev = info.Severity
	}
	s.AddSeverity(code, sev, pointer, message, hint, value)
}

// AddSeverity records a diagnostic at an explicit severity, for the call sites
// where a flag downgrades or promotes a code.
//
// Message and Value are sanitized here rather than at the point they are
// rendered. A diagnostic quotes the workspace back — an unrecognized status, a
// malformed timestamp, the first line of a document that is not JSON — so its
// text carries whatever an agent wrote, and every surface that prints it would
// otherwise have to remember to neutralize it. A terminal escape reaching a
// diagnostic reaches every one of them.
func (s *Sink) AddSeverity(code Code, sev Severity, pointer, message, hint, value string) {
	d := About(code, s.path, pointer, message, hint, value)
	d.Severity = sev
	if pointer != "" && len(s.src) > 0 {
		d.Line, d.Column = Locate(s.src, pointer)
	}
	s.list = append(s.list, d)
}

// Diagnostics returns the accumulated diagnostics in the order they were added.
func (s *Sink) Diagnostics() []Diagnostic {
	if s == nil {
		return nil
	}
	return s.list
}

// Worst returns the highest severity recorded, and false when the sink is empty.
func (s *Sink) Worst() (Severity, bool) {
	if s == nil || len(s.list) == 0 {
		return Info, false
	}
	worst := s.list[0].Severity
	for _, d := range s.list[1:] {
		if d.Severity > worst {
			worst = d.Severity
		}
	}
	return worst, true
}

// HasError reports whether any recorded diagnostic is an Error.
func (s *Sink) HasError() bool {
	worst, ok := s.Worst()
	return ok && worst == Error
}

// Abbreviate shortens v to at most maxRunes runes, marking the elision. It cuts
// on a rune boundary, so the result is always valid UTF-8.
func Abbreviate(v string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(v) <= maxRunes {
		return v
	}
	var n, w int
	for i := range v {
		if n == maxRunes-1 {
			w = i
			break
		}
		n++
		w = i
	}
	return v[:w] + "…"
}

// MessageRunes bounds a diagnostic's message.
//
// A message quotes the workspace back — an unrecognized status, the member name
// the contract does not define — so its length is chosen by the document rather
// than by agentfs. A member name of two hundred thousand runes would otherwise
// produce a two-hundred-kilobyte diagnostic, on one line, once per reading.
const MessageRunes = 512

// About builds a diagnostic about one document.
//
// Every member a workspace can influence — the path, the pointer, the message
// that quotes a value back, and the value itself — is neutralized here. A
// diagnostic is built in several places and printed in more, so an escape that
// reaches one field reaches every surface; holding the invariant at
// construction is what makes "no workspace byte a terminal acts on survives"
// true of the type rather than of each caller's discipline.
//
// A pointer is a member name the workspace chose, so it is bounded as well as
// neutralized: a document naming a member a megabyte long would otherwise put a
// megabyte on one line.
func About(code Code, path, pointer, message, hint, value string) Diagnostic {
	info, _ := Lookup(code)
	return Diagnostic{
		Code:     code,
		Severity: info.Severity,
		Path:     textx.Sanitize(path),
		Pointer:  Abbreviate(textx.Sanitize(pointer), 200),
		Message:  Abbreviate(textx.Sanitize(message), MessageRunes),
		Hint:     textx.Sanitize(hint),
		Value:    Abbreviate(textx.Sanitize(value), 120),
	}
}

// Observed builds a diagnostic about the observer rather than about one
// document, taking its severity from the registry so a code is raised at the
// level the generated reference publishes for it.
//
// A condition agentfs met — a ceiling, a refused watch, a rebuilt tree — is a
// finding a caller needs in the same shape as a finding about a document, or a
// consumer reading the machine output has to learn a second vocabulary for the
// half of the picture that is about the reader rather than the workspace.
func Observed(code Code, message, hint, value string) Diagnostic {
	return About(code, "", "", message, hint, value)
}

// Tally renders a count against the noun it agrees with, so a diagnostic's
// value reads as a phrase rather than as a number the reader has to attach a
// unit to.
func Tally[N int | uint64](n N, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(uint64(n)) + " " + many
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
