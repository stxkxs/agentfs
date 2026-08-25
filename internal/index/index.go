// Package index holds the workspace tree the file pane renders.
//
// Two properties bound its cost. Children are read when a directory is opened
// rather than when the workspace is scanned, so starting against a workspace of
// any size reads one directory. And a change batch is applied by reloading only
// the loaded directories the batch names, so one agent writing one file costs
// one directory read rather than a walk of the workspace.
//
// Symlinks are recorded and never followed. A link is a leaf whatever it points
// at, which makes a link cycle unrepresentable rather than merely bounded, and
// combined with the confined root in [fsx] it means no path outside the
// workspace is ever read.
//
// The index names the directories it needs and adopts the results; it does not
// read them itself. A read of a network export can block for as long as the
// mount's timeout, and an interface that performed it inline would freeze under
// exactly the conditions agentfs exists to observe. [Index.Pending] reports
// what to read and [Index.Adopt] takes the answer, so the caller decides which
// goroutine pays for it.
package index

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/stxkxs/agentfs/internal/fsx"
	"github.com/stxkxs/agentfs/internal/watch"
)

// Limits bound what the index holds.
type Limits struct {
	// MaxDepth is how far below the root a directory may be opened.
	MaxDepth int
	// MaxEntriesPerDir caps the members held for one directory. A directory
	// with more is held to the cap and marked truncated, which the pane
	// renders, rather than silently shortened.
	MaxEntriesPerDir int
	// MaxNodes caps the whole index.
	MaxNodes int
	// RecentFor is how long after a change a node is marked recent.
	RecentFor time.Duration
}

// DefaultLimits returns the ceilings the index applies when a caller supplies
// none.
func DefaultLimits() Limits {
	return Limits{MaxDepth: 32, MaxEntriesPerDir: 5000, MaxNodes: 200000, RecentFor: 3 * time.Second}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxEntriesPerDir <= 0 {
		l.MaxEntriesPerDir = d.MaxEntriesPerDir
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = d.MaxNodes
	}
	if l.RecentFor <= 0 {
		l.RecentFor = d.RecentFor
	}
	return l
}

// Node is one file or directory.
type Node struct {
	// Name is the base name.
	Name string
	// Path is workspace-relative and slash-separated. The root's path is ".".
	Path string
	// IsDir reports whether the node's children can be read.
	IsDir bool
	// IsLink reports a symbolic link, which is never followed.
	IsLink bool
	// Size is the file's length in bytes.
	Size int64
	// ModTime is the filesystem's modification time.
	ModTime time.Time
	// ChangedAt is when a change batch last named this node.
	ChangedAt time.Time
	// Truncated reports that the directory holds more members than
	// [Limits.MaxEntriesPerDir], so what is held is a prefix.
	Truncated bool
	// Unreadable holds the error that prevented the directory being read.
	Unreadable error

	children []*Node
	loaded   bool
	expanded bool
	parent   *Node
}

// Children returns the loaded members, which is nil for a directory that has
// not been opened.
func (n *Node) Children() []*Node { return n.children }

// Loaded reports whether the directory's members have been read.
func (n *Node) Loaded() bool { return n.loaded }

// Expanded reports whether the node is showing its members.
func (n *Node) Expanded() bool { return n.expanded }

// ChildCount returns the number of loaded members, and whether the count is
// known. A collapsed directory that has been opened before still knows its
// count, which is what lets the pane label the disclosure with what is inside
// it rather than with the fact that something is.
func (n *Node) ChildCount() (int, bool) {
	if !n.loaded {
		return 0, false
	}
	return len(n.children), true
}

// Row is one visible line of the tree.
type Row struct {
	Node  *Node
	Depth int
}

// Stats reports what the index holds and what it refused to hold.
type Stats struct {
	// Nodes is the number of nodes held.
	Nodes int
	// Rows is the number of visible lines.
	Rows int
	// LoadedDirs is the number of directories whose members have been read.
	LoadedDirs int
	// TruncatedDirs is the number of directories held to the per-directory cap.
	TruncatedDirs int
	// DepthTruncated is the number of directories not opened because they lie
	// below the depth ceiling.
	DepthTruncated int
	// UnreadableDirs is the number of directories that could not be read.
	UnreadableDirs int
	// NodeCeilingHit reports that the index reached [Limits.MaxNodes].
	NodeCeilingHit bool
	// Reads is the number of directory reads performed over the index's life.
	Reads int
}

// Index is the workspace tree.
type Index struct {
	root    *fsx.Root
	limits  Limits
	tree    *Node
	rows    []Row
	stats   Stats
	byPath  map[string]*Node
	pending map[string]struct{}
	// reopen names directories a rebuild must open again once they are read,
	// so a rebuild does not cost a reader the place they were in. It is
	// discarded once the rebuild it belongs to has no read outstanding.
	reopen map[string]struct{}
	now    func() time.Time
}

// New returns an index over root, with the root directory read. It reads
// exactly one directory, whatever the workspace contains.
func New(root *fsx.Root, limits Limits) *Index {
	ix := NewDeferred(root, limits)
	ix.Drain()
	return ix
}

// NewDeferred returns an index that has read nothing. The root directory is
// pending, so the caller reads it off whichever goroutine it chooses and hands
// the result to [Index.Adopt].
func NewDeferred(root *fsx.Root, limits Limits) *Index {
	ix := &Index{
		root:    root,
		limits:  limits.withDefaults(),
		now:     time.Now,
		byPath:  make(map[string]*Node),
		pending: make(map[string]struct{}),
	}
	ix.tree = &Node{Name: path.Base(root.Name()), Path: ".", IsDir: true, expanded: true}
	ix.byPath["."] = ix.tree
	ix.request(ix.tree)
	return ix
}

// Pending returns the directories the index needs read, in path order. It is
// empty when the index is up to date with what it has been told.
func (ix *Index) Pending() []string {
	if len(ix.pending) == 0 {
		return nil
	}
	return slices.Sorted(maps(ix.pending))
}

// Read performs the read for one pending directory. It is the operation a
// caller runs off the goroutine that owns the index.
func (ix *Index) Read(p string) Loaded {
	l := Loaded{Path: p, At: ix.now()}
	fsys := ix.root.FS()
	if fsys == nil {
		l.Err = fsx.ErrRootLost
		return l
	}
	l.Entries, l.Err = fsys.ReadDir(p)
	return l
}

// Loaded is the result of reading one directory.
type Loaded struct {
	// Path is the directory that was read.
	Path string
	// Entries are its members, or nil when the read failed.
	Entries []fs.DirEntry
	// Err is why the read failed.
	Err error
	// At is when the read happened.
	At time.Time
}

// Adopt records a directory's members, keeping the identity and open state of
// members that survived so a reader's cursor and expansions are preserved.
func (ix *Index) Adopt(l Loaded) {
	delete(ix.pending, l.Path)
	defer ix.settleReopen()

	n, ok := ix.byPath[l.Path]
	if !ok || !n.IsDir {
		return
	}
	at := l.At
	if at.IsZero() {
		at = ix.now()
	}

	prev := make(map[string]*Node, len(n.children))
	for _, c := range n.children {
		prev[c.Name] = c
	}
	for _, c := range n.children {
		ix.forget(c)
	}
	n.children = nil
	n.Truncated = false
	ix.absorb(n, l, at, prev)
	ix.flatten()
}

// settleReopen discards the reopen set once the rebuild that recorded it has no
// read outstanding. The set describes the tree at the moment the rebuild
// started; held past that, it reopens a directory the operator closed after it.
func (ix *Index) settleReopen() {
	if len(ix.pending) == 0 {
		ix.reopen = nil
	}
}

// Drain reads and adopts every pending directory on the calling goroutine,
// repeating until nothing is pending. It is what a one-shot command uses, where
// blocking is the point.
func (ix *Index) Drain() {
	for range ix.limits.MaxNodes {
		reqs := ix.Pending()
		if len(reqs) == 0 {
			return
		}
		for _, p := range reqs {
			ix.Adopt(ix.Read(p))
		}
	}
}

// Root returns the root node.
func (ix *Index) Root() *Node { return ix.tree }

// Rows returns the visible lines, outermost first.
func (ix *Index) Rows() []Row { return ix.rows }

// Stats returns what the index holds.
func (ix *Index) Stats() Stats {
	s := ix.stats
	s.Rows = len(ix.rows)
	return s
}

// Lookup returns the node at a workspace-relative path.
func (ix *Index) Lookup(p string) (*Node, bool) {
	n, ok := ix.byPath[p]
	return n, ok
}

// VisibleDirs returns the directories a change source should sweep: the root,
// and every directory whose members are being displayed. Handing this to
// [watch.Observer.Track] is what makes sweep cost a function of what is on
// screen rather than of workspace size.
//
// A collapsed directory is excluded. Its members are not rendered, and opening
// it reads it again, so sweeping it would spend operations on a difference
// nobody is looking at.
func (ix *Index) VisibleDirs() []string {
	seen := map[string]struct{}{".": {}}
	out := []string{"."}
	for _, r := range ix.rows {
		n := r.Node
		if !n.IsDir || !n.loaded || !n.expanded {
			continue
		}
		if _, dup := seen[n.Path]; dup {
			continue
		}
		seen[n.Path] = struct{}{}
		out = append(out, n.Path)
	}
	return out
}

// Expand opens a directory. When its members have not been read, the directory
// becomes pending and its rows appear once the caller adopts the read.
func (ix *Index) Expand(p string) {
	n, ok := ix.byPath[p]
	if !ok || !n.IsDir {
		return
	}
	if !n.loaded {
		ix.request(n)
	}
	n.expanded = true
	ix.flatten()
}

// Collapse closes a directory, keeping what was read so reopening it costs
// nothing.
func (ix *Index) Collapse(p string) {
	if n, ok := ix.byPath[p]; ok && n.IsDir {
		n.expanded = false
		ix.flatten()
	}
}

// Toggle opens a closed directory and closes an open one.
func (ix *Index) Toggle(p string) {
	n, ok := ix.byPath[p]
	if !ok || !n.IsDir {
		return
	}
	if n.expanded {
		ix.Collapse(p)
		return
	}
	ix.Expand(p)
}

// Apply folds a change batch into the index, reloading only the loaded
// directories the batch names.
//
// A batch that reports a resync is applied as a rebuild, because such a batch
// is not a complete account of what changed and applying it would leave the
// index disagreeing with the filesystem.
func (ix *Index) Apply(b watch.Batch) {
	if b.Seeded {
		return
	}
	if b.Resync || b.RootRecovered {
		ix.Rebuild()
		return
	}

	at := b.At
	if at.IsZero() {
		at = ix.now()
	}

	dirty := make(map[string]struct{})
	for _, c := range b.Changes {
		if n, ok := ix.byPath[c.Path]; ok {
			n.ChangedAt = at
		}
		parent := path.Dir(c.Path)
		if parent == "" || c.Path == "." {
			parent = "."
		}
		if p, ok := ix.byPath[parent]; ok && p.IsDir && p.loaded {
			dirty[parent] = struct{}{}
		}
	}

	for _, p := range slices.Sorted(maps(dirty)) {
		ix.pending[p] = struct{}{}
	}
	ix.flatten()
}

// Rebuild discards everything below the root and reads it again, preserving
// which directories were open so the operator's place is not lost.
func (ix *Index) Rebuild() {
	open := make(map[string]struct{})
	for p, n := range ix.byPath {
		if n.IsDir && n.expanded {
			open[p] = struct{}{}
		}
	}

	ix.byPath = make(map[string]*Node)
	ix.pending = make(map[string]struct{})
	ix.stats = Stats{Reads: ix.stats.Reads}
	ix.tree = &Node{Name: ix.tree.Name, Path: ".", IsDir: true, expanded: true}
	ix.byPath["."] = ix.tree
	ix.reopen = open
	ix.request(ix.tree)
	ix.flatten()
}

// request marks a directory as needing a read, unless it lies below the depth
// ceiling.
func (ix *Index) request(n *Node) {
	if depthOf(n.Path) >= ix.limits.MaxDepth {
		if !n.loaded {
			ix.stats.DepthTruncated++
		}
		n.loaded, n.Truncated = true, true
		return
	}
	ix.pending[n.Path] = struct{}{}
}

// depthOf returns how far below the root a path lies.
func depthOf(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// absorb records a directory read against the node, applying the ceilings and
// carrying forward what the previous members knew.
func (ix *Index) absorb(n *Node, l Loaded, at time.Time, prev map[string]*Node) {
	wasLoaded := n.loaded
	n.loaded = true
	ix.stats.Reads++

	if l.Err != nil {
		n.Unreadable = l.Err
		ix.stats.UnreadableDirs++
		return
	}
	n.Unreadable = nil
	if !wasLoaded {
		ix.stats.LoadedDirs++
	}

	entries := sortEntries(l.Entries)
	if len(entries) > ix.limits.MaxEntriesPerDir {
		entries = entries[:ix.limits.MaxEntriesPerDir]
		ix.stats.TruncatedDirs++
		n.Truncated = true
	}

	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if ix.stats.Nodes >= ix.limits.MaxNodes {
			ix.stats.NodeCeilingHit = true
			n.Truncated = true
			break
		}
		child := ix.newNode(n, e)
		if old, existed := prev[child.Name]; existed {
			child.expanded = old.expanded
			child.ChangedAt = old.ChangedAt
			if old.Size != child.Size || !old.ModTime.Equal(child.ModTime) {
				child.ChangedAt = at
			}
		} else if len(prev) > 0 {
			child.ChangedAt = at
		}
		if _, reopening := ix.reopen[child.Path]; reopening {
			child.expanded = true
		}
		children = append(children, child)
		ix.byPath[child.Path] = child
		ix.stats.Nodes++
		if child.IsDir && child.expanded {
			ix.request(child)
		}
	}
	n.children = children
}

// forget removes a node and its descendants from the path map.
func (ix *Index) forget(n *Node) {
	delete(ix.byPath, n.Path)
	delete(ix.pending, n.Path)
	ix.stats.Nodes--
	for _, c := range n.children {
		ix.forget(c)
	}
}

func (ix *Index) newNode(parent *Node, e fs.DirEntry) *Node {
	child := &Node{
		Name:   e.Name(),
		Path:   join(parent.Path, e.Name()),
		IsDir:  e.IsDir(),
		IsLink: e.Type()&fs.ModeSymlink != 0,
		parent: parent,
	}
	if info, err := e.Info(); err == nil {
		child.Size = info.Size()
		child.ModTime = info.ModTime()
	}
	return child
}

func join(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

// sortEntries orders directories before files, each alphabetically, which is
// the order a reader scanning a tree expects.
func sortEntries(entries []fs.DirEntry) []fs.DirEntry {
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	return out
}

// flatten recomputes the visible rows.
func (ix *Index) flatten() {
	ix.rows = ix.rows[:0]
	ix.appendRows(ix.tree, -1)
}

func (ix *Index) appendRows(n *Node, depth int) {
	if n != ix.tree {
		ix.rows = append(ix.rows, Row{Node: n, Depth: depth})
	}
	if !n.IsDir || !n.expanded {
		return
	}
	for _, c := range n.children {
		ix.appendRows(c, depth+1)
	}
}

// maps returns a set's keys as a sequence, for sorted iteration.
func maps(m map[string]struct{}) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// Survey opens every directory the ceilings permit and returns what the index
// then holds.
//
// It is what a diagnostic command runs: the ceilings are only observable by
// reaching them, so a report on what agentfs can and cannot see in a workspace
// has to look. The walk is bounded by the very ceilings it reports — depth,
// entries per directory and total nodes — so a workspace large enough to make
// the walk expensive is one that stops it early and says so.
func (ix *Index) Survey() Stats {
	for range ix.limits.MaxNodes {
		ix.Drain()

		opened := false
		for _, n := range ix.byPath {
			if n.IsDir && !n.loaded && !n.expanded {
				n.expanded = true
				ix.request(n)
				opened = true
			}
		}
		if !opened {
			return ix.Stats()
		}
		if ix.stats.NodeCeilingHit {
			ix.Drain()
			return ix.Stats()
		}
	}
	return ix.Stats()
}
