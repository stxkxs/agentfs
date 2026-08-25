// Package workspace turns an observed directory into the agents and runs it
// declares.
//
// Discovery reads the contract in [agentstate] rather than matching filenames
// of its own, so the convention has one definition. What an agent declares is
// authoritative: a run's identity comes from the run_id its state document
// carries, and only a directory that declares nothing is identified by the
// shape of its name.
package workspace

import (
	"cmp"
	"errors"
	"io/fs"
	"maps"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stxkxs/agentfs/internal/agentstate"
	"github.com/stxkxs/agentfs/internal/diag"
	"github.com/stxkxs/agentfs/internal/fsx"
)

// Presence is how an agent's declared state is known at the moment of a scan.
// It separates "the agent says it is idle" from "the agent said nothing", which
// a reader that renders both as unknown cannot.
type Presence int

// The presence states, which are pairwise distinguishable in the rendered bar.
const (
	// PresenceAbsent: the workspace directory declares no state document.
	PresenceAbsent Presence = iota
	// PresenceDeclared: a state document was read and decoded.
	PresenceDeclared
	// PresenceStale: a document was read, and it is older than the heartbeat
	// it declared, so what it says may no longer be true.
	PresenceStale
	// PresenceSettling: the document changed between reads, so a
	// well-formedness error is withheld and the last good reading stands.
	PresenceSettling
	// PresenceInvalid: a document that stopped changing still does not decode.
	PresenceInvalid
	// PresenceUnreadable: the document exists and could not be read.
	PresenceUnreadable
)

// String returns the lowercase name of the presence.
func (p Presence) String() string {
	switch p {
	case PresenceAbsent:
		return "absent"
	case PresenceDeclared:
		return "declared"
	case PresenceStale:
		return "stale"
	case PresenceSettling:
		return "settling"
	case PresenceInvalid:
		return "invalid"
	case PresenceUnreadable:
		return "unreadable"
	default:
		return "unknown"
	}
}

// MarshalText encodes the presence as its name. A consumer of the JSON output
// branches on a word rather than on an ordinal, so adding a presence cannot
// silently change what an existing number means.
func (p Presence) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// Trustworthy reports whether the state carries a reading an operator can act
// on.
func (p Presence) Trustworthy() bool { return p == PresenceDeclared }

// Agent is one detected agent workspace.
type Agent struct {
	// Name is the workspace directory name, or the agent member of the state
	// document when it declares one.
	Name string `json:"name"`
	// Dir is the workspace-relative directory.
	Dir string `json:"dir"`
	// Presence is how the state below is known.
	Presence Presence `json:"presence"`
	// State is the decoded declaration. Under [PresenceSettling] it is the
	// last reading that decoded.
	State agentstate.State `json:"state"`
	// Diagnostics are the findings the document raised.
	Diagnostics []diag.Diagnostic `json:"diagnostics,omitempty"`
	// Document is the workspace-relative path of the state document.
	Document string `json:"document,omitempty"`
	// Age is how long since the document declared it was written, falling back
	// to the file's modification time when it declares nothing.
	Age time.Duration `json:"age"`
	// Roles are the conventional subdirectories the workspace carries.
	Roles []string `json:"roles,omitempty"`
}

// HasRole reports whether the agent carries a conventional subdirectory.
func (a Agent) HasRole(name string) bool { return slices.Contains(a.Roles, name) }

// Status returns the declared status. It is unknown for a presence that carries
// no reading, so a caller cannot mistake "the document could not be read" for
// "the agent declared nothing to do".
func (a Agent) Status() agentstate.Status {
	switch a.Presence {
	case PresenceDeclared, PresenceStale, PresenceSettling:
		return a.State.Status
	case PresenceAbsent, PresenceInvalid, PresenceUnreadable:
		return agentstate.StatusUnknown
	default:
		return agentstate.StatusUnknown
	}
}

// Run is one recorded execution.
type Run struct {
	// ID identifies the run: the run_id its state document declares, or its
	// directory name when it declares none.
	ID string `json:"id"`
	// Agent is the workspace the run belongs to.
	Agent string `json:"agent"`
	// Dir is the workspace-relative run directory.
	Dir string `json:"dir"`
	// Declared reports whether ID came from a state document. An inferred
	// identity is a guess from the shape of a directory name, and is rendered
	// differently so an operator is not misled about which it is.
	Declared bool `json:"declared"`
	// Status is what the run's own state document declared.
	Status agentstate.Status `json:"status"`
	// StartedAt is the run's declared start, falling back to the directory's
	// modification time.
	StartedAt time.Time `json:"started_at"`
	// Files is the number of members the run directory holds.
	Files int `json:"files"`
}

// Result is one scan of the workspace.
type Result struct {
	// Agents are the detected workspaces, ordered by name.
	Agents []Agent `json:"agents"`
	// Diagnostics are findings about the workspace itself rather than about
	// one document.
	Diagnostics []diag.Diagnostic `json:"diagnostics,omitempty"`
	// ObservedAt is when the scan ran.
	ObservedAt time.Time `json:"observed_at"`
}

// Worst returns the most serious severity across every agent's diagnostics.
func (r Result) Worst() (diag.Severity, bool) {
	worst, raised := diag.Info, false
	for _, a := range r.Agents {
		for _, d := range a.Diagnostics {
			if !raised || d.Severity > worst {
				worst, raised = d.Severity, true
			}
		}
	}
	for _, d := range r.Diagnostics {
		if !raised || d.Severity > worst {
			worst, raised = d.Severity, true
		}
	}
	return worst, raised
}

// Options tunes a scan.
type Options struct {
	// Now is the observer's clock.
	Now func() time.Time
	// StaleAfter is how long after its declared write a document is reported
	// stale when it declares no heartbeat of its own.
	StaleAfter time.Duration
	// SkewTolerance is how far ahead of Now a timestamp may be. A workspace on
	// a shared mount is written by hosts whose clocks are not this one, so a
	// small lead is clamped to zero rather than rendered as a negative age.
	SkewTolerance time.Duration
	// MaxDocumentBytes caps a state document. A larger one is reported rather
	// than read.
	MaxDocumentBytes int64
	// MaxExtraBytes caps the undefined members preserved per document. Zero
	// applies [agentstate.DefaultMaxExtraBytes], so a document read here and
	// one read through [agentstate] keep the same bytes.
	MaxExtraBytes int64
	// SettleReads is how many consecutive readings of an unchanged document
	// are required before a well-formedness error is reported. A document
	// being rewritten in place is observed torn, and reporting that as invalid
	// makes the status bar flicker between the agent's real state and a parse
	// error on every cycle.
	SettleReads int
}

// DefaultOptions returns the options a scanner uses when a caller supplies none.
func DefaultOptions() Options {
	return Options{
		Now:              time.Now,
		StaleAfter:       90 * time.Second,
		SkewTolerance:    agentstate.DefaultSkewTolerance,
		MaxDocumentBytes: 1 << 20,
		MaxExtraBytes:    agentstate.DefaultMaxExtraBytes,
		SettleReads:      2,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Now == nil {
		o.Now = d.Now
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = d.StaleAfter
	}
	if o.SkewTolerance <= 0 {
		o.SkewTolerance = d.SkewTolerance
	}
	if o.MaxDocumentBytes <= 0 {
		o.MaxDocumentBytes = d.MaxDocumentBytes
	}
	if o.MaxExtraBytes <= 0 {
		o.MaxExtraBytes = d.MaxExtraBytes
	}
	if o.SettleReads <= 0 {
		o.SettleReads = d.SettleReads
	}
	return o
}

// reading is what the scanner remembers about one document between scans, so
// the settle rule can tell a torn write from a broken one.
type reading struct {
	size      int64
	mod       time.Time
	unstable  int
	lastGood  agentstate.State
	haveGood  bool
	goodDiags []diag.Diagnostic
}

// Scanner discovers agents and runs, and remembers enough between scans to
// apply the settle rule. The memory is bounded by the workspace: each scan
// drops what it did not find.
//
// A caller can have two scans in flight at once — a periodic rescan and one a
// change triggered arrive on separate goroutines — so the remembered readings
// are guarded here rather than left to the caller to serialize. The caller that
// forgets is the one whose staleness reporting is wrong rather than absent.
type Scanner struct {
	root *fsx.Root
	opts Options

	mu   sync.Mutex
	seen map[string]*reading
}

// New returns a scanner over root.
func New(root *fsx.Root, opts Options) *Scanner {
	return &Scanner{root: root, opts: opts.withDefaults(), seen: make(map[string]*reading)}
}

// decodeOptions is how the scanner reads a document: its clock, the skew it
// tolerates, and the retention ceiling on undefined members. One constructor
// keeps an agent's document, a run's document and a run's identity read under
// the same limits.
func (s *Scanner) decodeOptions(now time.Time) agentstate.Options {
	// The contract states its ceiling as a Go int, which is narrower than a
	// byte count on the smallest platform agentfs builds for. A setting above
	// that holds at it rather than wrapping to a small one.
	extra := s.opts.MaxExtraBytes
	if extra > math.MaxInt {
		extra = math.MaxInt
	}
	return agentstate.Options{
		Now:           now,
		SkewTolerance: s.opts.SkewTolerance,
		MaxExtraBytes: int(extra),
	}
}

// Scan reads the workspace and returns the agents it declares.
func (s *Scanner) Scan() Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.opts.Now()
	res := Result{ObservedAt: now}

	fsys := s.root.FS()
	if fsys == nil {
		res.Diagnostics = append(res.Diagnostics, rootLostDiagnostic())
		return res
	}
	entries, err := fsys.ReadDir(".")
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, rootLostDiagnostic())
		return res
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if a, ok := s.inspect(fsys, e.Name(), now); ok {
			res.Agents = append(res.Agents, a)
		}
	}
	slices.SortFunc(res.Agents, func(x, y Agent) int { return cmp.Compare(x.Name, y.Name) })
	s.forget(res.Agents)
	return res
}

// forget drops the remembered readings for documents this scan did not find,
// so the settle memory is the size of the workspace rather than the size of
// everything the session has ever seen. A workspace whose agents come and go
// would otherwise grow it without bound.
//
// It runs only after a scan that read the root, because a scan that could not
// is no evidence that a document is gone.
func (s *Scanner) forget(agents []Agent) {
	live := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		if a.Document != "" {
			live[a.Document] = struct{}{}
		}
	}
	maps.DeleteFunc(s.seen, func(doc string, _ *reading) bool {
		_, held := live[doc]
		return !held
	})
}

// inspect reads one candidate directory, reporting whether it is an agent
// workspace. A directory is one if it declares a state document or carries a
// conventional subdirectory.
func (s *Scanner) inspect(fsys fsx.FS, dir string, now time.Time) (Agent, bool) {
	a := Agent{Name: dir, Dir: dir, Presence: PresenceAbsent}

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return a, false
	}

	var doc string
	for _, name := range agentstate.StateFiles() {
		if slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return !e.IsDir() && e.Name() == name }) {
			doc = path.Join(dir, name)
			break
		}
	}
	for _, e := range entries {
		if e.IsDir() && agentstate.IsSignalDir(e.Name()) {
			a.Roles = append(a.Roles, e.Name())
		}
	}
	slices.Sort(a.Roles)

	if doc == "" && len(a.Roles) == 0 {
		return a, false
	}
	if doc == "" {
		return a, true
	}

	a.Document = doc
	s.read(fsys, &a, now)
	if a.State.Agent != "" {
		a.Name = a.State.Agent
	}
	return a, true
}

// read decodes an agent's state document, applying the settle rule.
func (s *Scanner) read(fsys fsx.FS, a *Agent, now time.Time) {
	prev, remembered := s.seen[a.Document]
	if !remembered {
		prev = &reading{}
		s.seen[a.Document] = prev
	}

	info, err := fsys.Stat(a.Document)
	if err != nil {
		a.Presence = PresenceUnreadable
		a.Diagnostics = []diag.Diagnostic{unreadableDiagnostic(a.Document, err)}
		return
	}
	if info.Size() > s.opts.MaxDocumentBytes {
		a.Presence = PresenceUnreadable
		a.Diagnostics = []diag.Diagnostic{tooLargeDiagnostic(a.Document, info.Size(), s.opts.MaxDocumentBytes)}
		return
	}

	stable := prev.size == info.Size() && prev.mod.Equal(info.ModTime())
	prev.size, prev.mod = info.Size(), info.ModTime()

	data, err := fsys.ReadFile(a.Document)
	if err != nil {
		a.Presence = PresenceUnreadable
		a.Diagnostics = []diag.Diagnostic{unreadableDiagnostic(a.Document, err)}
		return
	}

	state, ds := agentstate.Decode(a.Document, data, s.decodeOptions(now))

	if malformed(ds) {
		if !stable {
			prev.unstable = 1
		} else {
			prev.unstable++
		}
		if prev.unstable < s.opts.SettleReads {
			// The document is being rewritten. Reporting the torn reading as
			// invalid would make the bar flicker on every cycle of a normally
			// behaving agent.
			a.Presence = PresenceSettling
			a.Diagnostics = append(settlingDiagnostics(a.Document), prev.goodDiags...)
			if prev.haveGood {
				a.State = prev.lastGood
			}
			s.age(a, now)
			return
		}
		a.Presence = PresenceInvalid
		a.State, a.Diagnostics = state, ds
		return
	}

	prev.unstable = 0
	a.State, a.Diagnostics = state, ds

	if worstOf(ds) == diag.Error {
		// The document parsed and still declares nothing usable — an
		// unrecognized status, or a member the contract refuses. That is a
		// different fact from "the agent is idle", and rendering it as a
		// status would state something the document does not.
		a.Presence = PresenceInvalid
		return
	}

	prev.lastGood, prev.haveGood, prev.goodDiags = state, true, ds
	a.Presence = PresenceDeclared
	s.age(a, now)

	if a.Age > s.staleAfter(state) {
		a.Presence = PresenceStale
		a.Diagnostics = append(a.Diagnostics, staleDiagnostic(a.Document, a.Age, s.staleAfter(state)))
	}
}

// age records how long since the agent said it wrote the document, clamping a
// small lead to zero: a workspace on a shared mount is written by a host whose
// clock is not this one, and rendering a negative age is worse than rendering
// zero.
func (s *Scanner) age(a *Agent, now time.Time) {
	stamp := a.State.UpdatedAt
	if stamp.IsZero() {
		stamp = a.State.StartedAt
	}
	if stamp.IsZero() {
		return
	}
	a.Age = now.Sub(stamp)
	if a.Age < 0 && -a.Age <= s.opts.SkewTolerance {
		a.Age = 0
	}
}

func (s *Scanner) staleAfter(st agentstate.State) time.Duration {
	if st.Heartbeat > 0 {
		return st.Heartbeat
	}
	return s.opts.StaleAfter
}

// worstOf returns the most serious severity among the diagnostics.
func worstOf(ds []diag.Diagnostic) diag.Severity {
	worst := diag.Info
	for _, d := range ds {
		if d.Severity > worst {
			worst = d.Severity
		}
	}
	return worst
}

// malformed reports whether the document failed to decode as a JSON object at
// all, which is the only class of failure the settle rule withholds. A document
// that parses but declares an unknown status is wrong however many times it is
// read, so reporting it is never delayed.
func malformed(ds []diag.Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
		return d.Code == diag.CodeNotJSON || d.Code == diag.CodeNotObject
	})
}

// runPattern matches the directory names a run is inferred from when it
// declares no identity of its own.
var runPattern = regexp.MustCompile(`^(?:run[-_]?\d+|\d{4}[-_]\d{2}[-_]\d{2}[-_T].*|\d{8}[-_]\d{6}.*|\d+|run|(?:attempt|try|iter|iteration)[-_]?\d*)$`)

// Document is one state document found in the workspace, wherever it lies.
type Document struct {
	// Path is the workspace-relative path of the document.
	Path string `json:"path"`
	// Agent is the workspace directory it belongs to.
	Agent string `json:"agent"`
	// Run is the run directory it belongs to, empty for an agent's own
	// document.
	Run string `json:"run,omitempty"`
	// State is what it decoded to.
	State agentstate.State `json:"state"`
	// Diagnostics are the findings it raised.
	Diagnostics []diag.Diagnostic `json:"diagnostics,omitempty"`
}

// Documents returns every state document in the workspace, an agent's own and
// those recorded under its runs, each decoded.
//
// A run's document declares the same contract as an agent's, so a gate that
// checked only the agent's would pass a workspace whose recorded runs are
// malformed — and a run document is the one a reader consults after the agent
// that wrote it has gone.
func (s *Scanner) Documents() []Document {
	now := s.opts.Now()
	var out []Document

	for _, a := range s.Scan().Agents {
		if a.Document != "" {
			out = append(out, Document{
				Path: a.Document, Agent: a.Dir, State: a.State, Diagnostics: a.Diagnostics,
			})
		}
		for _, r := range s.Runs(a.Dir) {
			doc, ok := s.readRunDocument(r, now)
			if ok {
				out = append(out, doc)
			}
		}
	}
	slices.SortFunc(out, func(x, y Document) int { return cmp.Compare(x.Path, y.Path) })
	return out
}

// readRunDocument decodes the state document a run records, if it has one.
func (s *Scanner) readRunDocument(r Run, now time.Time) (Document, bool) {
	fsys := s.root.FS()
	if fsys == nil {
		return Document{}, false
	}
	for _, name := range agentstate.StateFiles() {
		doc := path.Join(r.Dir, name)
		data, err := fsys.ReadFile(doc)
		if err != nil {
			continue
		}
		state, ds := agentstate.Decode(doc, data, s.decodeOptions(now))
		return Document{Path: doc, Agent: r.Agent, Run: r.ID, State: state, Diagnostics: ds}, true
	}
	return Document{}, false
}

// Runs returns the runs recorded under an agent workspace.
func (s *Scanner) Runs(agentDir string) []Run {
	fsys := s.root.FS()
	if fsys == nil {
		return nil
	}

	var runs []Run
	for _, sub := range agentstate.RunDirs {
		dir := path.Join(agentDir, sub)
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			runs = append(runs, s.readRun(fsys, agentDir, path.Join(dir, e.Name()), e.Name()))
		}
	}

	entries, err := fsys.ReadDir(agentDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || agentstate.IsSignalDir(e.Name()) || agentstate.IsRunDir(e.Name()) {
				continue
			}
			if runPattern.MatchString(e.Name()) {
				runs = append(runs, s.readRun(fsys, agentDir, path.Join(agentDir, e.Name()), e.Name()))
			}
		}
	}

	slices.SortFunc(runs, func(x, y Run) int {
		if n := y.StartedAt.Compare(x.StartedAt); n != 0 {
			return n
		}
		return cmp.Compare(x.ID, y.ID)
	})
	return runs
}

// readRun reads one run directory, preferring the identity the run declares
// over the shape of its directory name.
func (s *Scanner) readRun(fsys fsx.FS, agentDir, dir, name string) Run {
	r := Run{ID: name, Agent: agentDir, Dir: dir}

	if info, err := fsys.Stat(dir); err == nil {
		r.StartedAt = info.ModTime()
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return r
	}
	r.Files = len(entries)

	for _, docName := range agentstate.StateFiles() {
		if !slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return e.Name() == docName }) {
			continue
		}
		data, readErr := fsys.ReadFile(path.Join(dir, docName))
		if readErr != nil {
			break
		}
		st, _ := agentstate.Decode(path.Join(dir, docName), data, s.decodeOptions(s.opts.Now()))
		r.Status = st.Status
		if st.RunID != "" {
			r.ID, r.Declared = st.RunID, true
		}
		if !st.StartedAt.IsZero() {
			r.StartedAt = st.StartedAt
		}
		break
	}
	return r
}

// The diagnostics this package raises about a workspace rather than about a
// document's contents. Each goes through [diag.About], which neutralizes every
// member a workspace can influence — a path here is a directory name the
// workspace chose.

func rootLostDiagnostic() diag.Diagnostic {
	return diag.About(diag.CodeRootLost, "", "",
		"The workspace root could not be read.",
		"Check that the path exists and the mount is available.",
		"")
}

func unreadableDiagnostic(p string, err error) diag.Diagnostic {
	hint := "Check the file's permissions."
	if errors.Is(err, fs.ErrNotExist) {
		hint = "The document was removed between being listed and being read."
	}
	return diag.About(diag.CodeUnreadable, p, "",
		"The state document could not be read.", hint, err.Error())
}

func tooLargeDiagnostic(p string, size, limit int64) diag.Diagnostic {
	return diag.About(diag.CodeDocumentTooLarge, p, "",
		"The state document is larger than the per-document ceiling.",
		"A state document is a status declaration; move bulk output into an artifact.",
		humanBytes(size)+" exceeds "+humanBytes(limit))
}

func staleDiagnostic(p string, age, limit time.Duration) diag.Diagnostic {
	return diag.About(diag.CodeStale, p, "",
		"The state document has not been rewritten within its heartbeat.",
		"The agent may have stopped without declaring a terminal status.",
		age.Round(time.Second).String()+" since the declared write, heartbeat "+limit.String())
}

func settlingDiagnostics(p string) []diag.Diagnostic {
	return []diag.Diagnostic{diag.About(diag.CodeSettling, p, "",
		"The state document changed between readings, so its last complete reading stands.",
		"Write the document atomically — to a temporary file in the same directory, then rename over the target.",
		"")}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strings.TrimSpace(itoa(n) + " B")
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return itoa(n/div) + string("KMGT"[exp]) + "iB"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
