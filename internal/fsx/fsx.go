// Package fsx is the read-only filesystem seam agentfs observes a workspace
// through.
//
// Two properties are structural rather than conventional. The seam exposes no
// method that writes, so no code path above it can modify an observed
// workspace. And a production root is an [os.Root], whose openat-based
// resolution refuses any path that leaves the root — including one reached
// through a symlink — which [os.DirFS] does not do.
package fsx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

// FS is the read-only capability set agentfs needs from a workspace root.
// [os.Root.FS] and [testing/fstest.MapFS] both satisfy it, so the production
// and test roots are the same type to every caller above this package.
type FS interface {
	fs.FS
	fs.StatFS
	fs.ReadDirFS
	fs.ReadFileFS
	fs.ReadLinkFS
}

// ErrRootLost reports that the workspace root itself became unreadable: an
// unmounted export, a stale NFS handle, or a deleted directory. It is distinct
// from a per-file error because every subsequent operation will fail until the
// root is reopened.
var ErrRootLost = errors.New("workspace root is unreadable")

// Root is a workspace root opened for reading.
type Root struct {
	name string

	mu   sync.RWMutex
	fsys FS
	os   *os.Root // nil when the root is synthetic
}

// Open opens dir as a confined workspace root.
func Open(dir string) (*Root, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	fsys, ok := r.FS().(FS)
	if !ok {
		// Releasing the descriptor is all that is left to do; the failure the
		// caller receives is the one that matters.
		_ = r.Close()
		return nil, errors.New("fsx: os.Root.FS does not satisfy fsx.FS")
	}
	return &Root{name: dir, fsys: fsys, os: r}, nil
}

// New returns a root backed by fsys, for tests and for any caller that
// supplies its own filesystem. A synthetic root reports itself healthy for as
// long as its root directory reads.
func New(name string, fsys FS) *Root {
	return &Root{name: name, fsys: fsys}
}

// Name returns the path the root was opened at.
func (r *Root) Name() string { return r.name }

// FS returns the filesystem the root reads through.
func (r *Root) FS() FS {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fsys
}

// Close releases the root's descriptor.
func (r *Root) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.os == nil {
		return nil
	}
	err := r.os.Close()
	r.os = nil
	return err
}

// Health reports whether the root is still readable. It wraps [ErrRootLost] so
// a caller can distinguish a vanished root from a per-file failure with
// [errors.Is].
func (r *Root) Health() error {
	fsys := r.FS()
	if fsys == nil {
		return ErrRootLost
	}
	if _, err := fsys.Stat("."); err != nil {
		return errors.Join(ErrRootLost, err)
	}
	return nil
}

// Reopen re-resolves the root's path, recovering from an unmount or a
// remount. It is a no-op for a synthetic root.
func (r *Root) Reopen() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.os == nil && r.fsys != nil {
		return nil
	}
	next, err := os.OpenRoot(r.name)
	if err != nil {
		return errors.Join(ErrRootLost, err)
	}
	fsys, ok := next.FS().(FS)
	if !ok {
		_ = next.Close()
		return errors.New("fsx: os.Root.FS does not satisfy fsx.FS")
	}
	if r.os != nil {
		// The old descriptor points at a root that is gone; closing it can
		// only fail in ways that do not change what happens next.
		_ = r.os.Close()
	}
	r.os, r.fsys = next, fsys
	return nil
}

// ReadRange reads at most len(buf) bytes of name starting at off, and reports
// the file's total size at the moment of the read. It reads only the requested
// range, so following a growing log costs the appended bytes rather than the
// whole file.
func (r *Root) ReadRange(name string, off int64, buf []byte) (n int, size int64, err error) {
	fsys := r.FS()
	if fsys == nil {
		return 0, 0, ErrRootLost
	}
	f, err := fsys.Open(name)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }() // A read-only file has nothing to flush.

	info, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	size = info.Size()
	if off >= size || len(buf) == 0 {
		return 0, size, nil
	}

	ra, ok := f.(io.ReaderAt)
	if !ok {
		return 0, size, errors.New("fsx: file does not support ranged reads")
	}
	n, err = ra.ReadAt(buf, off)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return n, size, err
}

// Clean normalizes a path reported by a change source into a workspace-relative
// slash path. It reports false for any path that is absolute, escapes the root,
// or is not a valid [io/fs] path, so an escaping event is dropped rather than
// resolved.
func Clean(rel string) (string, bool) {
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." {
		return ".", true
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
		return "", false
	}
	cleaned := path.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if !fs.ValidPath(cleaned) {
		return "", false
	}
	return cleaned, true
}
