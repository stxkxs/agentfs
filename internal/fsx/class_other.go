//go:build !linux && !darwin

package fsx

// classify reports an unprobeable filesystem on platforms whose statfs shape
// agentfs does not read. [KindUnknown] selects the conservative detection mode,
// so an unprobed platform observes changes rather than assuming events arrive.
func classify(string) Filesystem {
	return Filesystem{Kind: KindUnknown, Type: "unprobed"}
}
