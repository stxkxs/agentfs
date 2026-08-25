package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/stxkxs/agentfs/internal/fsx"
)

// Mode selects a change-detection strategy.
//
// The choice is a correctness one before it is a performance one. Kernel
// notification observes local VFS activity, so a write made by another client
// of a network export raises no event on this host: a notify-only watcher on
// such a mount reports a workspace that has not changed since it was opened,
// and looks healthy while doing it.
type Mode int

// The change-detection strategies.
const (
	// ModeAuto defers the choice to the filesystem probe. [Config.FilesystemMode]
	// resolves it.
	ModeAuto Mode = iota
	// ModeNotify subscribes to kernel change events and runs no sweep. It sees
	// every change to a filesystem whose writes all pass through this kernel,
	// and it costs nothing while the workspace is idle.
	ModeNotify
	// ModeSweep re-reads the tree on a period and subscribes to nothing. It is
	// the strategy that holds when no notification mechanism is available, and
	// the one a workspace under a watch-descriptor budget falls back to.
	ModeSweep
	// ModeHybrid subscribes and sweeps, so a change that raises no event still
	// surfaces within one sweep interval.
	ModeHybrid
)

// modeNames are the accepted spellings, indexed by [Mode].
var modeNames = [...]string{"auto", "notify", "sweep", "hybrid"}

// String returns the spelling [ParseMode] accepts for m. A mode outside the
// defined set renders as its number, so a misconfigured value is visible rather
// than disguised as a strategy.
func (m Mode) String() string {
	if m < 0 || int(m) >= len(modeNames) {
		return "mode(" + strconv.Itoa(int(m)) + ")"
	}
	return modeNames[m]
}

// ErrUnknownMode reports a watch-mode spelling that names no strategy.
var ErrUnknownMode = errors.New("unknown watch mode")

// ParseMode returns the mode named by s, matched without regard to case or
// surrounding space. An unrecognized spelling is an error wrapping
// [ErrUnknownMode] rather than a silent fall back to [ModeAuto], because a
// typo in a flag that selects change detection would otherwise be indetectable
// from the output.
func ParseMode(s string) (Mode, error) {
	want := strings.ToLower(strings.TrimSpace(s))
	for i, name := range modeNames {
		if name == want {
			return Mode(i), nil
		}
	}
	return ModeAuto, fmt.Errorf("%w %q: permitted values are %s",
		ErrUnknownMode, s, strings.Join(modeNames[:], ", "))
}

// FilesystemMode returns the strategy to run against a root that sits on a
// filesystem of kind k. A mode the operator asked for is returned unchanged.
//
// [ModeAuto] resolves to [ModeNotify] on a local filesystem, where kernel
// notification observes every write, and to [ModeHybrid] on every other kind.
// The sweep is what makes agentfs's claim about a network export true: events
// describe this kernel's VFS activity, and another client's write is not part
// of it.
func (c Config) FilesystemMode(k fsx.Kind) Mode {
	if c.Watch != ModeAuto {
		return c.Watch
	}
	switch k {
	case fsx.KindLocal:
		return ModeNotify
	case fsx.KindNetwork, fsx.KindFuse, fsx.KindUnknown:
		// Events on these kinds are incomplete, and so is a kind this build
		// does not recognize: both take the sweep-backed strategy below.
	}
	return ModeHybrid
}
