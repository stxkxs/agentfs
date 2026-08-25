//go:build darwin

package fsx

import (
	"strings"
	"syscall"
)

func classify(dir string) Filesystem {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Filesystem{Kind: KindUnknown, Type: "unprobeable"}
	}
	name := fstypename(st.Fstypename[:])
	switch {
	case name == "apfs" || name == "hfs" || name == "ufs":
		return Filesystem{Kind: KindLocal, Type: name}
	case name == "nfs" || name == "smbfs" || name == "afpfs" || name == "webdav":
		return Filesystem{Kind: KindNetwork, Type: name}
	case strings.HasPrefix(name, "osxfuse") || strings.HasPrefix(name, "macfuse") ||
		strings.Contains(name, "fuse"):
		return Filesystem{Kind: KindFuse, Type: name}
	default:
		return Filesystem{Kind: KindUnknown, Type: name}
	}
}

// fstypename decodes the NUL-terminated filesystem name statfs reports. The
// name is ASCII, so a byte outside it means the buffer is not what this build
// expects and the name is reported as far as it was readable.
func fstypename(raw []int8) string {
	b := make([]byte, 0, len(raw))
	for _, c := range raw {
		if c <= 0 || c > 0x7E {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
