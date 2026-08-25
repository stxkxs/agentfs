// Package config is the table of every ceiling and setting agentfs runs under.
//
// A ceiling that is not in the table cannot be set and cannot be documented.
// Flag registration, the generated reference and the doctor subcommand are all
// rendered from [Limits], and a test asserts by reflection that the table names
// every field of [Config], so a field added without a row fails the build
// rather than shipping as an undocumented knob.
//
// A second test walks the module for a read of each field the table names, so a
// setting no component consumes fails the same way: what the reference publishes
// is what the binary applies, in both directions.
//
// Every ceiling exists because the quantity it bounds is chosen by the
// workspace rather than by agentfs: the number of entries in a directory, the
// size of a file, the rate of change events. A ceiling turns each of those into
// a number this package states, which is what makes a bounded-work claim
// something a test can assert instead of something the prose promises.
package config

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
)

// Config is the resolved runtime configuration. Every field is settable, and
// each one is described by the [Limit] row of the same Name.
type Config struct {
	// Root is the workspace directory agentfs observes.
	Root string
	// Watch is the requested change-detection strategy, resolved against the
	// filesystem under Root by [Config.FilesystemMode].
	Watch Mode

	// MaxDepth bounds descent below Root.
	MaxDepth int
	// MaxEntriesPerDir bounds the entries read from one directory.
	MaxEntriesPerDir int
	// MaxNodes bounds the tree the model holds.
	MaxNodes int
	// MaxWatches bounds directory watch registrations.
	MaxWatches int
	// MaxBatch bounds the events folded into one model update.
	MaxBatch int
	// MaxQueue bounds events waiting to be applied.
	MaxQueue int

	// MaxPreviewBytes bounds the bytes read and held from any one file.
	MaxPreviewBytes int64
	// MaxDocumentBytes bounds a state document.
	MaxDocumentBytes int64
	// MaxExtraBytes bounds the undefined members preserved per document.
	MaxExtraBytes int64

	// MaxFeedEntries bounds the activity feed ring.
	MaxFeedEntries int
	// MaxDiagnostics bounds retained diagnostics.
	MaxDiagnostics int
	// SweepBudget bounds the directories one sweep pass reads.
	SweepBudget int

	// SweepInterval is the period between sweep passes.
	SweepInterval time.Duration
	// DedupTTL is the window within which repeated events for one path
	// collapse to a single update.
	DedupTTL time.Duration
	// SkewTolerance is how far ahead of this host's clock a workspace
	// timestamp may sit before it is reported as being from the future.
	SkewTolerance time.Duration
	// StaleAfter is the silence after which a workspace is marked stale.
	StaleAfter time.Duration
	// RootRetryMin is the first delay before reopening a lost root.
	RootRetryMin time.Duration
	// RootRetryMax is the ceiling the reopen backoff climbs to.
	RootRetryMax time.Duration

	// Strict promotes every warning diagnostic to an error.
	Strict bool
	// RedactKeys names the document members whose values are masked before
	// rendering.
	RedactKeys []string
	// Color selects styling: [ColorAuto], [ColorAlways] or [ColorNever].
	Color string
	// ASCII restricts the frame to ASCII glyphs.
	ASCII bool
}

// Color selections. Auto is the only value that inspects the terminal.
const (
	// ColorAuto styles only when the output is an interactive terminal.
	ColorAuto = "auto"
	// ColorAlways styles regardless of where the output goes, which is what a
	// pipe into a pager wants.
	ColorAlways = "always"
	// ColorNever emits no escape sequence at all.
	ColorNever = "never"
)

// colorValues are the accepted spellings of [Config.Color], in the order the
// reference lists them.
var colorValues = []string{ColorAuto, ColorAlways, ColorNever}

// defaultRedactKeys are the member names whose values are masked unless the
// operator replaces the list. They are the spellings a credential is written
// under by the SDKs agents are built on.
var defaultRedactKeys = []string{
	"api_key",
	"apikey",
	"authorization",
	"cookie",
	"credential",
	"password",
	"secret",
	"token",
}

// Defaults returns the configuration agentfs runs under when nothing is set.
// It satisfies [Config.Validate], and the reference document's default column
// is rendered from it, so a default that cannot be run with is a test failure.
//
// MaxExtraBytes and SkewTolerance take the contract's own values, so a document
// read through agentfs and one read through [agentstate] are read under the
// same limits.
func Defaults() Config {
	return Config{
		Root:  ".",
		Watch: ModeAuto,

		MaxDepth:         32,
		MaxEntriesPerDir: 5000,
		MaxNodes:         200000,
		MaxWatches:       8192,
		MaxBatch:         4096,
		MaxQueue:         8192,

		MaxPreviewBytes:  1 << 20,
		MaxDocumentBytes: 1 << 20,
		MaxExtraBytes:    agentstate.DefaultMaxExtraBytes,

		MaxFeedEntries: 2000,
		MaxDiagnostics: 500,
		SweepBudget:    512,

		SweepInterval: 2 * time.Second,
		DedupTTL:      2 * time.Second,
		SkewTolerance: agentstate.DefaultSkewTolerance,
		StaleAfter:    90 * time.Second,
		RootRetryMin:  time.Second,
		RootRetryMax:  30 * time.Second,

		Strict:     false,
		RedactKeys: append([]string(nil), defaultRedactKeys...),
		Color:      ColorAuto,
		ASCII:      false,
	}
}

// String renders the settings that differ from [Defaults] as flag=value pairs
// in table order, and reports "defaults" when none differ. The doctor
// subcommand prints this: what it shows is the whole set of choices that
// separates this run from a documented one.
func (c Config) String() string {
	cv := reflect.ValueOf(c)
	dv := reflect.ValueOf(Defaults())
	var b strings.Builder
	for i := range limitSpecs {
		s := &limitSpecs[i]
		cf := cv.FieldByName(s.name)
		df := dv.FieldByName(s.name)
		if !cf.IsValid() || !df.IsValid() || reflect.DeepEqual(cf.Interface(), df.Interface()) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s.flag)
		b.WriteByte('=')
		b.WriteString(quote(renderField(cv, s)))
	}
	if b.Len() == 0 {
		return "defaults"
	}
	return b.String()
}

// quote wraps a rendered value in Go quotes when leaving it bare would make the
// pair list ambiguous to read or to split.
func quote(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\"'") {
		return strconv.Quote(v)
	}
	return v
}
