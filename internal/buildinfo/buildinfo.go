// Package buildinfo reports the identity of the running binary: the version it
// ships as, the revision it was built from, when it was built, the toolchain
// that compiled it, and the workspace contract version it implements.
//
// A release build stamps the first three at link time. A build made without
// stamps still identifies itself, because the module version, revision and
// build time the Go toolchain embeds are read back from [debug.BuildInfo]: a
// binary from `go install github.com/stxkxs/agentfs@latest` reports the version
// it was installed at, and one built inside a checkout reports the commit it
// was built from and whether that tree was clean.
//
// Every reported fact is sanitized and bounded before it leaves this package,
// so a stamp supplied on a linker command line cannot emit a terminal control
// sequence or a line break into the output that prints it.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/textx"
)

// Name is the binary's name, and the first word of every identity line.
const Name = "agentfs"

// DevVersion is the version reported by a build that carries no released
// version: a working-tree build, or a test binary.
const DevVersion = "dev"

// Unknown is reported for a fact the build carries no evidence of.
const Unknown = "unknown"

const (
	// dirtySuffix marks a revision built from a tree with uncommitted changes.
	dirtySuffix = "-dirty"

	// revisionLen is the number of hexadecimal characters a full revision is
	// shortened to. It matches the revision length the module system encodes
	// in a pseudo-version, so a commit recovered from a version and one
	// recovered from a VCS stamp print as the same string.
	revisionLen = 12

	// maxField bounds one reported fact in terminal cells. A stamp is whatever
	// the build passed to the linker, and the identity is rendered into a
	// terminal, so an unbounded stamp is an unbounded line.
	maxField = 64

	// develVersion is what the toolchain records as the module version of a
	// binary built outside a release.
	develVersion = "(devel)"

	// stampLayout is the timestamp form the module system encodes in a
	// pseudo-version.
	stampLayout = "20060102150405"
)

// version, commit and buildDate carry the link-time stamps:
//
//	go build -ldflags "-X github.com/stxkxs/agentfs/internal/buildinfo.version=1.4.0"
//
// A build that sets none of them leaves them empty, and every fact is
// recovered from the information the toolchain embeds instead.
var (
	version   string
	commit    string
	buildDate string
)

// Info identifies the running binary. Every field is non-empty: a fact the
// build carries no evidence of reads [Unknown] or [DevVersion] rather than
// blank, so a rendered identity has no holes in it.
//
// It travels as the payload of the one-shot JSON envelope, so its members take
// the spelling every other member of that format takes: a consumer reads one
// naming convention across every command rather than one per payload.
type Info struct {
	// Version is the released version without its leading "v", or [DevVersion]
	// for a build made outside a release.
	Version string `json:"version"`
	// Commit is the revision the build was made from, or [Unknown]. A full
	// hexadecimal revision is shortened; a revision in any other form, such
	// as the output of git describe, is reported as it was given. A revision
	// the toolchain recorded from a tree carrying uncommitted changes is
	// suffixed "-dirty"; a revision supplied as a link-time stamp is
	// reported as stamped, because the release process names what it built.
	Commit string `json:"commit"`
	// BuildDate is when the binary was built, in RFC 3339 and UTC, or
	// [Unknown].
	BuildDate string `json:"build_date"`
	// GoVersion is the toolchain that compiled the binary.
	GoVersion string `json:"go_version"`
	// Schema is the [agentstate] contract version this build implements. It is
	// read from the contract itself, so the version agentfs reports and the
	// version it enforces cannot disagree.
	Schema string `json:"schema"`
}

// Get returns the identity of the running binary. The answer cannot change
// over the life of the process, so it is resolved once and shared by every
// caller, from any goroutine.
func Get() Info { return cached() }

var cached = sync.OnceValue(read)

func read() Info {
	// ReadBuildInfo reports nil for a binary that carries no embedded build
	// information, which resolve reads as no evidence.
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, commit, buildDate, bi)
}

// resolve derives the identity from the link-time stamps and the embedded
// build information, either of which may be absent. A stamp wins over embedded
// information, because a stamp is what the release process asserted.
func resolve(stampedVersion, stampedCommit, stampedDate string, bi *debug.BuildInfo) Info {
	vcs := vcsStamps(bi)
	ver := resolveVersion(stampedVersion, bi)
	revision, built := pseudo(ver)

	return Info{
		Version:   ver,
		Commit:    resolveCommit(stampedCommit, vcs, revision),
		BuildDate: resolveDate(stampedDate, vcs, built),
		GoVersion: resolveGo(bi),
		Schema:    agentstate.SchemaVersion,
	}
}

// stamps are the version-control facts the toolchain embeds when it builds
// from a checkout.
type stamps struct {
	revision string
	built    string
	modified bool
}

func vcsStamps(bi *debug.BuildInfo) stamps {
	var s stamps
	if bi == nil {
		return s
	}
	for _, setting := range bi.Settings {
		switch setting.Key {
		case "vcs.revision":
			s.revision = setting.Value
		case "vcs.time":
			s.built = setting.Value
		case "vcs.modified":
			s.modified = setting.Value == "true"
		}
	}
	return s
}

func resolveVersion(stamped string, bi *debug.BuildInfo) string {
	if v := clean(stamped); v != "" {
		return trimV(v)
	}
	if bi != nil {
		if v := clean(bi.Main.Version); v != "" && v != develVersion {
			return trimV(v)
		}
	}
	return DevVersion
}

func resolveCommit(stamped string, vcs stamps, pseudoRevision string) string {
	if c := clean(stamped); c != "" {
		return shortenRevision(c)
	}
	if r := clean(vcs.revision); r != "" {
		r = shortenRevision(r)
		if vcs.modified {
			r = textx.Truncate(r, maxField-len(dirtySuffix)) + dirtySuffix
		}
		return r
	}
	if pseudoRevision != "" {
		return pseudoRevision
	}
	return Unknown
}

func resolveDate(stamped string, vcs stamps, pseudoDate string) string {
	if d, ok := normalizeTime(clean(stamped)); ok {
		return d
	}
	if d, ok := normalizeTime(clean(vcs.built)); ok {
		return d
	}
	if pseudoDate != "" {
		return pseudoDate
	}
	return Unknown
}

func resolveGo(bi *debug.BuildInfo) string {
	if bi != nil {
		if g := clean(bi.GoVersion); g != "" {
			return g
		}
	}
	return clean(runtime.Version())
}

// pseudo returns the revision and build time encoded in a Go pseudo-version,
// the version the module system assigns a commit that carries no tag. A binary
// installed at such a version carries no VCS stamps, so its version string is
// the only evidence of what it was built from. Both results are empty for a
// version in any other form.
func pseudo(v string) (revision, built string) {
	parts := strings.Split(v, "-")
	if len(parts) < 3 {
		return "", ""
	}
	rev := parts[len(parts)-1]
	if len(rev) != revisionLen || !isHex(rev) {
		return "", ""
	}
	stamp := parts[len(parts)-2]
	if dot := strings.LastIndexByte(stamp, '.'); dot >= 0 {
		stamp = stamp[dot+1:]
	}
	at, err := time.Parse(stampLayout, stamp)
	if err != nil {
		return "", ""
	}
	return rev, rfc3339(at)
}

// timeLayouts are the forms a build stamp is read in, tried in order. Each
// normalizes to one comparable form, so two builds stamped by different
// pipelines report their dates the same way. A bare date or a zoneless
// timestamp is read as UTC.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	stampLayout,
	time.DateOnly,
}

// normalizeTime returns s as RFC 3339 in UTC. A value matching no layout is
// read as seconds since the Unix epoch. It reports false for a value it cannot
// read at all, and for an epoch outside the four-digit year range RFC 3339 can
// represent.
func normalizeTime(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	for _, layout := range timeLayouts {
		if at, err := time.Parse(layout, s); err == nil {
			return rfc3339(at), true
		}
	}
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		at := time.Unix(secs, 0)
		if y := at.UTC().Year(); y < 0 || y > 9999 {
			return "", false
		}
		return rfc3339(at), true
	}
	return "", false
}

// rfc3339 renders an instant in the one form every reported date takes. Every
// layout in timeLayouts carries a four-digit year, so a time read through one
// of them is representable.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// shortenRevision returns a full hexadecimal revision shortened to revisionLen
// characters. A revision in any other form is returned as given, so a stamp
// that already carries a describe suffix survives intact.
func shortenRevision(s string) string {
	if len(s) <= revisionLen || !isHex(s) {
		return s
	}
	return s[:revisionLen]
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsFunc(s, notHexDigit)
}

func notHexDigit(r rune) bool {
	return (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F')
}

// trimV removes the leading "v" the module system puts on a version, so a
// version stamped as 1.4.0 and one read back as v1.4.0 report identically. The
// prefix is removed only when a digit follows it, which is the only form the
// module system produces; stripping it unconditionally would rewrite a version
// that merely begins with a letter, and would rewrite it again on every pass.
func trimV(v string) string {
	rest, found := strings.CutPrefix(v, "v")
	if !found || rest == "" || rest[0] < '0' || rest[0] > '9' {
		return v
	}
	return rest
}

// clean returns s with terminal controls neutralized, surrounding space
// removed, and its width bounded to maxField cells.
func clean(s string) string {
	return textx.Truncate(strings.TrimSpace(textx.Sanitize(s)), maxField)
}
