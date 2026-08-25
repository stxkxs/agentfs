package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/textx"
)

// EnvPrefix is prepended to a flag name to form the environment variable that
// sets the same field. Every [Limit.Env] follows from [Limit.Flag] this way, so
// an operator who knows one knows the other.
const EnvPrefix = "AGENTFS_"

// ListSeparator joins the entries of a [UnitList] value on the command line and
// in the environment, and splits them again. One constant serves both
// directions, so a name [Config.Validate] accepts is a name the encoding reads
// back unchanged.
const ListSeparator = ","

// The unit vocabulary. A flag is registered, a value is parsed and a default is
// rendered from [Limit.Unit], so the set is closed: a row carrying a unit
// outside it would be a setting the command line cannot express.
const (
	// UnitBytes is a byte count, rendered with a binary suffix.
	UnitBytes = "bytes"
	// UnitCount is a plain cardinal.
	UnitCount = "count"
	// UnitDuration is a Go duration, parsed by [time.ParseDuration].
	UnitDuration = "duration"
	// UnitPath is a filesystem path.
	UnitPath = "path"
	// UnitEnum is one spelling from [Limit.Enum].
	UnitEnum = "enum"
	// UnitBool is a boolean.
	UnitBool = "bool"
	// UnitList is a comma-separated list of names.
	UnitList = "list"
)

// Limit describes one setting: what it is called on the command line, in the
// environment and in Go, what shape its value has, what it defaults to, and
// what it is for.
type Limit struct {
	// Name is the [Config] field name.
	Name string
	// Flag is the command-line flag that sets the field, without dashes.
	Flag string
	// Env is the environment variable that sets the field.
	Env string
	// Unit is one of the Unit constants, and selects how a value is parsed and
	// rendered.
	Unit string
	// Default is the value from [Defaults], rendered in Unit's form.
	Default string
	// Summary is one sentence: what the setting bounds, and, where the value
	// is a number, why the default is the number it is.
	Summary string
	// Enum is the permitted set when Unit is [UnitEnum], and is empty
	// otherwise.
	Enum []string
	// Commands names the commands that read the setting, empty when every
	// command does.
	//
	// A flag every command accepts and one command reads is a control that
	// reads as honoured everywhere and works in one place. Naming the commands
	// is what lets the reference say so and a test check it.
	Commands []string
}

// Limits returns the table, in [Config] field-declaration order. The order is
// stable because a test asserts it against the struct, which also makes the
// generated reference's section order follow the struct rather than an
// alphabetical accident.
//
// The returned rows own their memory: mutating one cannot change what a later
// caller reads.
func Limits() []Limit {
	dv := reflect.ValueOf(Defaults())
	out := make([]Limit, 0, len(limitSpecs))
	for i := range limitSpecs {
		s := &limitSpecs[i]
		out = append(out, Limit{
			Name:     s.name,
			Flag:     s.flag,
			Env:      s.env,
			Unit:     s.unit,
			Default:  renderField(dv, s),
			Summary:  s.summary,
			Enum:     slices.Clone(s.enum),
			Commands: slices.Clone(s.commands),
		})
	}
	return out
}

// spec is one row of the table. It carries what [Limit] publishes plus the
// floor [Config.Validate] holds the field to, which keeps the permitted range
// of a setting in the same place as its documentation.
type spec struct {
	name    string
	flag    string
	env     string
	unit    string
	summary string
	enum    []string
	// commands names the commands that read the field, empty when every
	// command does.
	commands []string
	// min is the lowest permitted value for a numeric unit, in the field's own
	// units: bytes, a cardinal, or nanoseconds.
	min int64
}

// limitSpecs is the table. Its order is [Config]'s field order.
var limitSpecs = []spec{
	{
		name: "Root", flag: "root", env: EnvPrefix + "ROOT", unit: UnitPath,
		summary: "Root is the directory agentfs observes; every path it reports is relative to it, and the root is opened confined so that no traversal, including one through a symlink, leaves the tree.",
	},
	{
		name: "Watch", flag: "watch", env: EnvPrefix + "WATCH", unit: UnitEnum,
		enum:    modeNames[:],
		summary: "Watch selects change detection, with auto resolving to kernel notification on a local filesystem and to notification plus sweep on every other kind, because a write by another client of a network export raises no event on this host.",
	},
	{
		name: "MaxDepth", flag: "max-depth", env: EnvPrefix + "MAX_DEPTH", unit: UnitCount, min: 1,
		summary: "MaxDepth bounds descent below the root; the default is deeper than a real workspace layout and shallow enough that a pathological tree truncates with a diagnostic instead of walking without end.",
	},
	{
		name: "MaxEntriesPerDir", flag: "max-entries-per-dir", env: EnvPrefix + "MAX_ENTRIES_PER_DIR", unit: UnitCount, min: 1,
		summary: "MaxEntriesPerDir bounds the entries read from one directory; the default is larger than any directory a person lays out by hand, so a run directory an agent fills without bound costs one bounded listing and a truncation diagnostic rather than stalling the walk.",
	},
	{
		name: "MaxNodes", flag: "max-nodes", env: EnvPrefix + "MAX_NODES", unit: UnitCount, min: 1,
		summary: "MaxNodes bounds the tree the model holds; the default spans a large checkout while keeping the node table proportional to a number agentfs chose rather than to whatever the workspace contains.",
	},
	{
		name: "MaxWatches", flag: "max-watches", env: EnvPrefix + "MAX_WATCHES", unit: UnitCount, min: 1,
		commands: []string{"watch"},
		summary:  "MaxWatches bounds directory watch registrations, keeping agentfs inside a conservative per-user kernel watch budget and handing the remainder of a large tree to the sweep.",
	},
	{
		name: "MaxBatch", flag: "max-batch", env: EnvPrefix + "MAX_BATCH", unit: UnitCount, min: 1,
		commands: []string{"watch"},
		summary:  "MaxBatch bounds the events folded into one model update; the default is half of MaxQueue, so a queue that has filled drains in two frames and a burst larger than that is reconciled by the sweep rather than applied in one long frame.",
	},
	{
		name: "MaxQueue", flag: "max-queue", env: EnvPrefix + "MAX_QUEUE", unit: UnitCount, min: 1,
		commands: []string{"watch"},
		summary:  "MaxQueue bounds the events waiting to be applied; the default is two full batches, which is the backlog a burst may build without a frame missing its deadline, and a queue that fills degrades to a sweep reconciliation instead of growing until the process is killed.",
	},
	{
		name: "MaxPreviewBytes", flag: "max-preview-bytes", env: EnvPrefix + "MAX_PREVIEW_BYTES", unit: UnitBytes, min: 1,
		commands: []string{"watch"},
		summary:  "MaxPreviewBytes bounds the window a preview holds of any one file; a preview is a window onto a file rather than a copy of it, so the default is a fraction of the transcript an agent writes and a file whose size the workspace chose costs the window instead of the file.",
	},
	{
		name: "MaxDocumentBytes", flag: "max-document-bytes", env: EnvPrefix + "MAX_DOCUMENT_BYTES", unit: UnitBytes, min: 1,
		summary: "MaxDocumentBytes bounds a state document; a status declaration larger than the default is a log, and decoding it as state would spend the whole memory budget on one file.",
	},
	{
		name: "MaxExtraBytes", flag: "max-extra-bytes", env: EnvPrefix + "MAX_EXTRA_BYTES", unit: UnitBytes, min: 1,
		summary: "MaxExtraBytes bounds the undefined members preserved per document; the default is the ceiling the state contract itself applies, so a document read by agentfs and one read by any conforming reader keep the same bytes, and reading a document written to a schema this build does not know cannot be turned into unbounded retention.",
	},
	{
		name: "MaxFeedEntries", flag: "max-feed-entries", env: EnvPrefix + "MAX_FEED_ENTRIES", unit: UnitCount, min: 1,
		commands: []string{"watch"},
		summary:  "MaxFeedEntries bounds the activity feed, which is a ring: the default holds enough history to explain what an agent has been doing at a cost that does not grow with how long agentfs has been running.",
	},
	{
		name: "MaxDiagnostics", flag: "max-diagnostics", env: EnvPrefix + "MAX_DIAGNOSTICS", unit: UnitCount, min: 1,
		commands: []string{"watch"},
		summary:  "MaxDiagnostics bounds retained findings; the default holds more distinct findings than a workspace has distinct ways of being wrong, and the overflow is counted and reported once, because a workspace that is wrong in one way is wrong in that way thousands of times.",
	},
	{
		name: "SweepBudget", flag: "sweep-budget", env: EnvPrefix + "SWEEP_BUDGET", unit: UnitCount, min: 1,
		summary: "SweepBudget bounds the directories one sweep pass reads; the default is the number a pass can read well inside SweepInterval, which is what keeps a sweep off the critical path of a frame however large the tree is.",
	},
	{
		name: "SweepInterval", flag: "sweep-interval", env: EnvPrefix + "SWEEP_INTERVAL", unit: UnitDuration, min: int64(MinSweepInterval),
		summary: "SweepInterval is the period between sweep passes; the default is shorter than an operator's patience for stale output and long enough that a budgeted pass costs a fraction of one core.",
	},
	{
		name: "DedupTTL", flag: "dedup-ttl", env: EnvPrefix + "DEDUP_TTL", unit: UnitDuration,
		commands: []string{"watch"},
		summary:  "DedupTTL is the window within which repeated events for one path collapse to a single update, sized so that an editor's write-then-rename sequence lands inside it, and disabled at zero.",
	},
	{
		name: "SkewTolerance", flag: "skew-tolerance", env: EnvPrefix + "SKEW_TOLERANCE", unit: UnitDuration,
		summary: "SkewTolerance is how far ahead of this host's clock a workspace timestamp may sit before it is reported as being from the future; the default is the tolerance the state contract names, wide enough for the drift between hosts sharing a mount and narrow enough that a genuinely wrong clock is still reported.",
	},
	{
		name: "StaleAfter", flag: "stale-after", env: EnvPrefix + "STALE_AFTER", unit: UnitDuration, min: 1,
		summary: "StaleAfter is the silence after which a workspace is marked stale, longer than the pause between an agent's steps and shorter than the time an operator would spend trusting a dead pane.",
	},
	{
		name: "RootRetryMin", flag: "root-retry-min", env: EnvPrefix + "ROOT_RETRY_MIN", unit: UnitDuration, min: 1,
		commands: []string{"watch"},
		summary:  "RootRetryMin is the first delay before reopening a lost root, short enough that a brief unmount recovers before an operator reaches for the keyboard.",
	},
	{
		name: "RootRetryMax", flag: "root-retry-max", env: EnvPrefix + "ROOT_RETRY_MAX", unit: UnitDuration, min: 1,
		commands: []string{"watch"},
		summary:  "RootRetryMax is the ceiling the reopen backoff climbs to, bounding what agentfs spends on a root that does not come back while keeping the retry frequent enough to notice one that does.",
	},
	{
		name: "Strict", flag: "strict", env: EnvPrefix + "STRICT", unit: UnitBool,
		summary: "Strict promotes every warning diagnostic to an error, so a workspace that reads with warnings fails a conformance run instead of passing it quietly, and reports a well-formedness error on the first reading rather than withholding it until a second reading agrees.",
	},
	{
		name: "RedactKeys", flag: "redact-keys", env: EnvPrefix + "REDACT_KEYS", unit: UnitList,
		commands: []string{"watch"},
		summary:  "RedactKeys names the document members whose values are masked before rendering, so a credential an agent writes into its own state is not put on a shared screen by the tool watching it.",
	},
	{
		name: "Color", flag: "color", env: EnvPrefix + "COLOR", unit: UnitEnum,
		commands: []string{"watch"},
		enum:     colorValues,
		summary:  "Color selects styling: auto styles only when the output is an interactive terminal, always styles whatever the output is, and never emits no escape sequence at all.",
	},
	{
		name: "ASCII", flag: "ascii", env: EnvPrefix + "ASCII", unit: UnitBool,
		commands: []string{"watch"},
		summary:  "ASCII restricts the frame to ASCII glyphs, for terminals and fonts that render box drawing and braille as replacement boxes.",
	},
}

// modeType is compared against a field's type so that a [Mode] renders as its
// spelling rather than as the integer it is.
var modeType = reflect.TypeOf(ModeAuto)

// renderField returns the display form of the field s names within the struct
// value v.
func renderField(v reflect.Value, s *spec) string {
	f := v.FieldByName(s.name)
	if !f.IsValid() {
		return ""
	}
	if f.Type() == modeType {
		return Mode(f.Int()).String()
	}
	switch f.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(f.Bool())
	case reflect.String:
		return textx.Sanitize(f.String())
	case reflect.Int, reflect.Int64:
		return renderNumber(s.unit, f.Int())
	case reflect.Slice:
		return renderList(f)
	default:
		return ""
	}
}

// renderNumber returns n in the form its unit is read in.
func renderNumber(unit string, n int64) string {
	switch unit {
	case UnitBytes:
		return formatBytes(n)
	case UnitDuration:
		return time.Duration(n).String()
	default:
		return strconv.FormatInt(n, 10)
	}
}

// renderList joins a string slice into the comma-separated form the flag
// accepts.
func renderList(f reflect.Value) string {
	xs, ok := f.Interface().([]string)
	if !ok {
		return ""
	}
	return textx.Sanitize(strings.Join(xs, ListSeparator))
}

// byteSuffixes are the binary magnitudes [formatBytes] renders in.
var byteSuffixes = [...]string{"B", "KiB", "MiB", "GiB", "TiB"}

// formatBytes renders n with the largest binary suffix that divides it
// exactly, so a ceiling written as a power of two reads back as one.
func formatBytes(n int64) string {
	const unit = 1024
	v, i := n, 0
	for v >= unit && v%unit == 0 && i < len(byteSuffixes)-1 {
		v /= unit
		i++
	}
	return strconv.FormatInt(v, 10) + byteSuffixes[i]
}
