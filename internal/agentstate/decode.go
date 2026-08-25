package agentstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/textx"
)

// Options tunes how a document is read.
type Options struct {
	// Now is the observer's clock, used to detect a timestamp from the future.
	Now time.Time
	// SkewTolerance is how far ahead of Now a timestamp may be before it is
	// reported. Workspaces on a shared mount are written by other hosts, whose
	// clocks are not this one.
	SkewTolerance time.Duration
	// MaxExtraBytes caps the undefined members preserved per document. A
	// member is charged its name together with its value, because both are
	// held. Zero applies [DefaultMaxExtraBytes].
	MaxExtraBytes int
}

// DefaultMaxExtraBytes caps preserved undefined members per document.
const DefaultMaxExtraBytes = 64 << 10

// DefaultSkewTolerance is how far ahead of the observer's clock a timestamp may
// be before it is reported as being from the future.
const DefaultSkewTolerance = 5 * time.Second

func (o Options) maxExtra() int {
	if o.MaxExtraBytes <= 0 {
		return DefaultMaxExtraBytes
	}
	return o.MaxExtraBytes
}

func (o Options) skew() time.Duration {
	if o.SkewTolerance <= 0 {
		return DefaultSkewTolerance
	}
	return o.SkewTolerance
}

// Decode reads one state document and returns the state together with every
// diagnostic the document raises.
//
// Decoding is member-by-member rather than a single unmarshal, because
// encoding/json abandons a document at its first type error. A document with
// three bad members yields three diagnostics, which is the difference between a
// validator an integrator can work from and one they must run repeatedly.
//
// A returned State is usable whenever no diagnostic has severity error; members
// that failed to decode hold their zero values.
func Decode(path string, src []byte, opts Options) (State, []diag.Diagnostic) {
	sink := diag.NewSink(path, src)
	st := State{Extra: map[string]json.RawMessage{}}

	if IsLegacyStateFile(baseName(path)) {
		sink.Add(diag.CodeLegacyFilename, "",
			"The state document is named "+quote(baseName(path))+".",
			fmt.Sprintf("Rename it to %q; the compatibility names are read but not canonical.", StateFile),
			display(baseName(path)))
	}

	raw, ok := decodeTopLevel(src, sink)
	if !ok {
		return st, sink.Diagnostics()
	}

	st.Schema, st.Profile = decodeProfile(raw, sink)
	if st.Profile == ProfileV1 && st.Schema != SchemaVersion {
		return st, sink.Diagnostics()
	}

	decodeMembers(&st, raw, sink, opts)
	checkSemantics(&st, raw, sink, opts)

	return st, sink.Diagnostics()
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func decodeTopLevel(src []byte, sink *diag.Sink) (map[string]json.RawMessage, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(src, &raw); err == nil {
		return raw, true
	}
	if !json.Valid(src) {
		sink.Add(diag.CodeNotJSON, "",
			"The document is not well-formed JSON.",
			"Write the document atomically so a reader never observes a partial write.",
			firstLine(src))
		return nil, false
	}
	sink.Add(diag.CodeNotObject, "",
		"The document's top level is not a JSON object.",
		"Wrap the value in an object whose members are the contract's.",
		firstLine(src))
	return nil, false
}

func decodeProfile(raw map[string]json.RawMessage, sink *diag.Sink) (string, Profile) {
	rawSchema, present := raw["schema"]
	if !present {
		sink.Add(diag.CodeSchemaMissing, "",
			"The document declares no schema member, so it is read under the compatibility profile.",
			fmt.Sprintf("Add %q: %q to read it under the versioned contract.", "schema", SchemaVersion),
			"")
		return "", ProfileCompat
	}
	var v string
	if err := json.Unmarshal(rawSchema, &v); err != nil {
		sink.Add(diag.CodeWrongType, "/schema",
			"The schema member is not a string.",
			fmt.Sprintf("Set it to %q.", SchemaVersion),
			string(rawSchema))
		return "", ProfileCompat
	}
	if v != SchemaVersion {
		sink.Add(diag.CodeSchemaUnknown, "/schema",
			"The document declares schema "+quote(v)+", which this build does not implement.",
			fmt.Sprintf("This build implements %q.", SchemaVersion),
			display(v))
		return v, ProfileV1
	}
	return v, ProfileV1
}

func decodeMembers(st *State, raw map[string]json.RawMessage, sink *diag.Sink, opts Options) {
	known := members()
	extraBytes := 0

	for _, name := range slices.Sorted(maps.Keys(raw)) {
		value := raw[name]
		at := "/" + diag.EscapeToken(name)
		rule, defined := known[name]
		if !defined {
			// A preserved member is held under its name, so the name is
			// charged with the value. Charging the value alone leaves a
			// document whose bulk is in its member names retaining that bulk
			// with the ceiling unreached.
			if cost := len(name) + len(value); extraBytes+cost <= opts.maxExtra() {
				st.Extra[name] = value
				extraBytes += cost
			} else {
				sink.Add(diag.CodeExtraTooLarge, at,
					"Preserved undefined members exceed the per-document ceiling.",
					"Move bulk data out of the state document; it is a status declaration.",
					"")
			}
			sink.Add(diag.CodeUnknownMember, at,
				"The contract does not define the member "+quote(name)+".",
				"Undefined members are preserved and ignored; move it under labels to make it contractual.",
				"")
			continue
		}
		if name != rule.Member && st.Profile == ProfileV1 {
			sink.Add(diag.CodeUnknownMember, at,
				fmt.Sprintf("The member %s is a compatibility name not defined by %s.", quote(name), SchemaVersion),
				fmt.Sprintf("Rename it to %q.", rule.Member),
				"")
			continue
		}
		if name != rule.Member {
			sink.Add(diag.CodeUnknownMember, at,
				fmt.Sprintf("The member %s is read as %q under the compatibility profile.", quote(name), rule.Member),
				fmt.Sprintf("Rename it to %q.", rule.Member),
				"")
		}
		assign(st, rule, name, value, sink, opts)
	}
}

//nolint:gocyclo // one branch per contract member; splitting it hides the table.
func assign(st *State, rule Rule, name string, value json.RawMessage, sink *diag.Sink, opts Options) {
	ptr := "/" + name
	switch rule.Member {
	case "schema":
		// Resolved by decodeProfile.
	case "status":
		s, ok := decodeString(value, ptr, rule, sink)
		if !ok {
			return
		}
		status, matched := ParseStatus(s, st.Profile)
		if !matched {
			sink.Add(diag.CodeStatusUnknown, ptr,
				"The status "+quote(s)+" is not in the contract vocabulary.",
				"Use one of: "+strings.Join(Vocabulary(), ", ")+". Matching is exact, not by substring.",
				display(s))
			return
		}
		st.Status = status
	case "agent":
		st.Agent, _ = decodeString(value, ptr, rule, sink)
	case "task":
		st.Task, _ = decodeString(value, ptr, rule, sink)
	case "model":
		st.Model, _ = decodeString(value, ptr, rule, sink)
	case "run_id":
		st.RunID, _ = decodeString(value, ptr, rule, sink)
	case "problem":
		st.Problem, _ = decodeString(value, ptr, rule, sink)
	case "step":
		st.Step = decodeStep(value, ptr, sink)
	case "steps_total":
		st.StepsTotal, _ = decodeInt(value, ptr, sink)
	case "heartbeat_seconds":
		if secs, ok := decodeNumber(value, ptr, sink); ok {
			st.Heartbeat = time.Duration(secs * float64(time.Second))
		}
	case "started_at":
		st.StartedAt = decodeTime(value, ptr, sink)
	case "updated_at":
		st.UpdatedAt = decodeTime(value, ptr, sink)
	case "labels":
		st.Labels = decodeLabels(value, ptr, sink)
	}
	_ = opts
}

// isNull reports whether a member was declared as JSON null. encoding/json
// treats null as a no-op for every scalar target, so without this guard a null
// member decodes to the zero value and reads as a declared value.
func isNull(value json.RawMessage) bool {
	return string(bytes.TrimSpace(value)) == "null"
}

func decodeString(value json.RawMessage, ptr string, rule Rule, sink *diag.Sink) (string, bool) {
	if isNull(value) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		sink.Add(diag.CodeWrongType, ptr,
			fmt.Sprintf("The member %q is not a string.", rule.Member),
			"Quote the value.",
			string(value))
		return "", false
	}
	if s == "" {
		sink.Add(diag.CodeEmptyString, ptr,
			fmt.Sprintf("The member %q is an empty string.", rule.Member),
			"Omit the member rather than declaring it empty.",
			"")
		return "", true
	}
	if rule.MaxRunes > 0 && utf8.RuneCountInString(s) > rule.MaxRunes {
		sink.Add(diag.CodeStringTooLong, ptr,
			fmt.Sprintf("The member %q is longer than %d runes.", rule.Member, rule.MaxRunes),
			"Shorten the value; a state document is a status declaration, not a log.",
			s)
		return diag.Abbreviate(s, rule.MaxRunes), true
	}
	return s, true
}

func decodeStep(value json.RawMessage, ptr string, sink *diag.Sink) Step {
	if isNull(value) {
		return NoStep
	}
	var n int64
	if err := json.Unmarshal(value, &n); err == nil {
		if n < 0 {
			sink.Add(diag.CodeStepNegative, ptr,
				"The step ordinal is negative.",
				"Use a non-negative ordinal, or a string to name a phase.",
				string(value))
			return NoStep
		}
		return OrdinalStep(int(n))
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		if s == "" {
			sink.Add(diag.CodeEmptyString, ptr,
				"The step label is an empty string.",
				"Omit the member rather than declaring it empty.",
				"")
			return NoStep
		}
		return LabelStep(diag.Abbreviate(s, 256))
	}
	sink.Add(diag.CodeStepType, ptr,
		"The step member is neither a non-negative integer nor a string.",
		"Use an ordinal such as 3, or a phase name such as \"retrieval\".",
		string(value))
	return NoStep
}

func decodeInt(value json.RawMessage, ptr string, sink *diag.Sink) (int, bool) {
	if isNull(value) {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(value, &n); err != nil {
		sink.Add(diag.CodeWrongType, ptr,
			"The member is not an integer.",
			"Write a JSON number with no fractional part.",
			string(value))
		return 0, false
	}
	if n < 0 {
		sink.Add(diag.CodeStepNegative, ptr,
			"The member is negative.",
			"Use a non-negative integer.",
			string(value))
		return 0, false
	}
	return int(n), true
}

func decodeNumber(value json.RawMessage, ptr string, sink *diag.Sink) (float64, bool) {
	if isNull(value) {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(value, &f); err != nil {
		sink.Add(diag.CodeWrongType, ptr,
			"The member is not a number.",
			"Write a JSON number.",
			string(value))
		return 0, false
	}
	if f < 0 {
		sink.Add(diag.CodeStepNegative, ptr,
			"The member is negative.",
			"Use a non-negative number.",
			string(value))
		return 0, false
	}
	return f, true
}

func decodeTime(value json.RawMessage, ptr string, sink *diag.Sink) time.Time {
	if isNull(value) {
		return time.Time{}
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		sink.Add(diag.CodeWrongType, ptr,
			"The timestamp member is not a string.",
			"Write an RFC 3339 date-time such as 2026-04-08T13:00:00Z.",
			string(value))
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05"} {
		if _, err := time.Parse(layout, s); err == nil {
			sink.Add(diag.CodeTimeNoOffset, ptr,
				"The timestamp declares no UTC offset, so it names no instant.",
				"Append an offset: Z for UTC, or +01:00.",
				s)
			return time.Time{}
		}
	}
	sink.Add(diag.CodeTimeNotRFC3339, ptr,
		"The timestamp is not an RFC 3339 date-time.",
		"Write it as 2026-04-08T13:00:00Z.",
		s)
	return time.Time{}
}

// MaxLabels bounds the members the labels object carries, and MaxLabelRunes
// bounds each name and each value.
//
// Labels are integrator-defined, which means their size is chosen by the
// document rather than by agentfs. Without a ceiling a state document can hand
// a reader an unbounded map to hold and an unbounded string to render, which is
// the one member of the contract with no shape of its own to constrain it.
const (
	MaxLabels     = 64
	MaxLabelRunes = 256
)

func decodeLabels(value json.RawMessage, ptr string, sink *diag.Sink) map[string]string {
	if isNull(value) {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(value, &m); err != nil {
		sink.Add(diag.CodeWrongType, ptr,
			"The labels member is not an object of strings.",
			"Write an object whose values are all strings.",
			string(value))
		return nil
	}

	names := slices.Sorted(maps.Keys(m))
	if len(names) > MaxLabels {
		sink.Add(diag.CodeStringTooLong, ptr,
			fmt.Sprintf("The labels object carries %d members, past the ceiling of %d.", len(names), MaxLabels),
			"Labels annotate a state declaration; move a collection into an artifact.",
			"")
		names = names[:MaxLabels]
	}

	// The result is built rather than edited, and in name order, so decoding
	// one document twice yields one set of labels. Shortening an over-long
	// name produces a name the object may already carry, which writing back
	// into the object being read would resolve by whichever order the map
	// happened to hand out.
	out := make(map[string]string, len(names))
	var oversize []string
	for _, name := range names {
		v := m[name]
		if utf8.RuneCountInString(name) <= MaxLabelRunes && utf8.RuneCountInString(v) <= MaxLabelRunes {
			out[name] = v
			continue
		}
		oversize = append(oversize, name)
	}

	// A label within the ceiling is read under the name the document gave it,
	// so an over-long name is shortened against the labels that are already
	// read. The label that loses the collision is dropped and named, because a
	// label that disappears without a finding is one the integrator has no way
	// to see.
	for _, name := range oversize {
		v := m[name]
		at := ptr + "/" + diag.EscapeToken(name)
		if _, taken := out[diag.Abbreviate(name, MaxLabelRunes)]; taken {
			sink.Add(diag.CodeStringTooLong, at,
				fmt.Sprintf("A label is longer than %d runes, and its name shortened to that ceiling is one another label already carries, so it is dropped.", MaxLabelRunes),
				fmt.Sprintf("Give the labels names that differ within %d runes.", MaxLabelRunes),
				v)
			continue
		}
		sink.Add(diag.CodeStringTooLong, at,
			fmt.Sprintf("A label is longer than %d runes.", MaxLabelRunes),
			"Shorten the name or the value; a label annotates rather than carries.",
			v)
		out[diag.Abbreviate(name, MaxLabelRunes)] = diag.Abbreviate(v, MaxLabelRunes)
	}
	return out
}

func checkSemantics(st *State, raw map[string]json.RawMessage, sink *diag.Sink, opts Options) {
	// A null member declares nothing, which is what the decoder reads it as
	// everywhere else. Counting a nulled status as declared would leave the
	// required member unreported and the state rendering as unknown.
	if value, present := raw["status"]; !present || isNull(value) {
		sink.Add(diag.CodeStatusMissing, "",
			"The document declares no status.",
			"Add a status member: "+strings.Join(Vocabulary(), ", ")+".",
			"")
	}

	if ord, ok := st.Step.Ordinal(); ok && st.StepsTotal > 0 && ord > st.StepsTotal {
		sink.Add(diag.CodeStepBeyondTotal, "/step",
			fmt.Sprintf("The step ordinal %d exceeds steps_total %d.", ord, st.StepsTotal),
			"Raise steps_total, or lower step.",
			st.Step.String())
	}

	if st.Status == StatusError && st.Problem == "" {
		sink.Add(diag.CodeErrorWithoutProblem, "/status",
			"The status is error but the document describes no problem.",
			"Add a problem member naming what failed.",
			"")
	}

	if !opts.Now.IsZero() {
		for ptr, ts := range map[string]time.Time{"/started_at": st.StartedAt, "/updated_at": st.UpdatedAt} {
			if ts.IsZero() {
				continue
			}
			if ahead := ts.Sub(opts.Now); ahead > opts.skew() {
				sink.Add(diag.CodeTimeFuture, ptr,
					fmt.Sprintf("The timestamp is %s ahead of this host's clock.", ahead.Round(time.Second)),
					"Write timestamps in UTC, and check clock synchronization between the writing and reading hosts.",
					ts.Format(time.RFC3339))
			}
		}
	}
}

func firstLine(src []byte) string {
	s := string(src)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return diag.Abbreviate(s, 120)
}

// quote wraps untrusted workspace text in quotation marks for a diagnostic
// message. The text is neutralized before it is spliced, so the message holds
// one quoted run and agentfs's own words are outside it.
func quote(s string) string { return `"` + display(s) + `"` }

// display prepares untrusted workspace text for a diagnostic. A call site whose
// message quotes a value passes this result as the finding's value too, so the
// two halves of the finding are neutralized the same way.
//
// Go's %q verb escapes what a terminal acts on, which makes a message safe on
// its own but makes it disagree with the Value member beside it: the two halves
// then render the same text two ways, and a reader resolving the finding trusts
// whichever they read first. [diag.About] bounds the two at ceilings of their
// own, so a value longer than its ceiling agrees with its message by its head.
//
// [textx.Sanitize] consumes an escape sequence forward from its introducer to
// the terminator that ends it. A value ending in an unterminated sequence
// consumes what follows it, so sanitizing the finished message would let the
// workspace eat the sentence its value was spliced into. Sanitizing the
// fragment bounds that consumption to the fragment.
//
// The quotation mark is what delimits the fragment within the message, so a
// value carrying one would close agentfs's quotation and continue in agentfs's
// voice. It renders as an apostrophe, in the message and in the value alike.
func display(s string) string {
	return strings.ReplaceAll(textx.Sanitize(s), `"`, "'")
}
