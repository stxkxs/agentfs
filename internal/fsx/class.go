package fsx

// Kind classifies the filesystem a workspace root lives on. Change detection
// picks its mechanism from this, because kernel change notification observes
// local VFS activity and therefore reports nothing about a write made by
// another client of a network export.
type Kind int

// Filesystem kinds, ordered by how much agentfs can rely on kernel events.
const (
	// KindUnknown is a filesystem the probe did not recognize. It is treated
	// as [KindNetwork], because assuming events arrive is the failure that
	// shows nothing while looking healthy.
	KindUnknown Kind = iota
	// KindLocal is a filesystem whose writes all pass through this kernel, so
	// kernel notification observes every change.
	KindLocal
	// KindNetwork is an export another client can write without this kernel
	// seeing it: NFS, EFS, SMB.
	KindNetwork
	// KindFuse is a userspace filesystem. Writes made through this mount raise
	// events; changes made behind it — an object replaced in a bucket — do not.
	KindFuse
)

// String returns the lowercase name of the kind.
func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindNetwork:
		return "network"
	case KindFuse:
		return "fuse"
	case KindUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// EventsAreComplete reports whether kernel notification alone observes every
// change to this kind of filesystem.
func (k Kind) EventsAreComplete() bool { return k == KindLocal }

// Filesystem describes the filesystem under a path.
type Filesystem struct {
	// Kind is what change detection selects on.
	Kind Kind
	// Type is the name the operating system reports, for display.
	Type string
}

// Classify reports the filesystem under dir. It never fails: an unrecognized or
// unprobeable filesystem classifies as [KindUnknown], which selects the same
// conservative detection mode as a network export.
func Classify(dir string) Filesystem {
	return classify(dir)
}
