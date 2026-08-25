//go:build linux

package fsx

import "syscall"

// Superblock magic numbers, from the kernel's magic.h.
const (
	magicNFS     = 0x6969
	magicSMB     = 0x517B
	magicCIFS    = 0xFF534D42
	magicFUSE    = 0x65735546
	magicEXT     = 0xEF53
	magicXFS     = 0x58465342
	magicBTRFS   = 0x9123683E
	magicTMPFS   = 0x01021994
	magicOVERLAY = 0x794C7630
	magicZFS     = 0x2FC12FC1
	magicF2FS    = 0xF2F52010
	magicCEPH    = 0x00C36400
)

func classify(dir string) Filesystem {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Filesystem{Kind: KindUnknown, Type: "unprobeable"}
	}
	switch int64(st.Type) {
	case magicEXT:
		return Filesystem{Kind: KindLocal, Type: "ext"}
	case magicXFS:
		return Filesystem{Kind: KindLocal, Type: "xfs"}
	case magicBTRFS:
		return Filesystem{Kind: KindLocal, Type: "btrfs"}
	case magicTMPFS:
		return Filesystem{Kind: KindLocal, Type: "tmpfs"}
	case magicOVERLAY:
		return Filesystem{Kind: KindLocal, Type: "overlay"}
	case magicZFS:
		return Filesystem{Kind: KindLocal, Type: "zfs"}
	case magicF2FS:
		return Filesystem{Kind: KindLocal, Type: "f2fs"}
	case magicNFS:
		return Filesystem{Kind: KindNetwork, Type: "nfs"}
	case magicSMB, magicCIFS:
		return Filesystem{Kind: KindNetwork, Type: "smb"}
	case magicCEPH:
		return Filesystem{Kind: KindNetwork, Type: "ceph"}
	case magicFUSE:
		return Filesystem{Kind: KindFuse, Type: "fuse"}
	default:
		return Filesystem{Kind: KindUnknown, Type: "unrecognized"}
	}
}
