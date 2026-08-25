package fsx

import (
	"io/fs"
	"sync"
)

// Op names a filesystem operation, for counting and for fault injection.
type Op string

// The operations the seam exposes.
const (
	OpOpen     Op = "open"
	OpStat     Op = "stat"
	OpLstat    Op = "lstat"
	OpReadDir  Op = "readdir"
	OpReadFile Op = "readfile"
	OpReadLink Op = "readlink"
)

// Ops is the per-operation call tally a [Counting] filesystem reports.
type Ops struct {
	Open     int
	Stat     int
	Lstat    int
	ReadDir  int
	ReadFile int
	ReadLink int
}

// Total returns the sum of every counter.
func (o Ops) Total() int {
	return o.Open + o.Stat + o.Lstat + o.ReadDir + o.ReadFile + o.ReadLink
}

// Counting wraps a filesystem and tallies the operations performed against it.
// Bounded-work claims are asserted against these counters rather than against
// elapsed time, so the assertion is about the algorithm and does not flake on a
// loaded machine.
type Counting struct {
	inner FS

	mu  sync.Mutex
	ops Ops
}

// NewCounting wraps inner in a counting filesystem.
func NewCounting(inner FS) *Counting { return &Counting{inner: inner} }

// Ops returns a snapshot of the tally.
func (c *Counting) Ops() Ops {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops
}

// Reset zeroes the tally.
func (c *Counting) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops = Ops{}
}

func (c *Counting) bump(f func(*Ops)) {
	c.mu.Lock()
	f(&c.ops)
	c.mu.Unlock()
}

// Open implements [io/fs.FS].
func (c *Counting) Open(name string) (fs.File, error) {
	c.bump(func(o *Ops) { o.Open++ })
	return c.inner.Open(name)
}

// Stat implements [io/fs.StatFS].
func (c *Counting) Stat(name string) (fs.FileInfo, error) {
	c.bump(func(o *Ops) { o.Stat++ })
	return c.inner.Stat(name)
}

// Lstat implements [io/fs.ReadLinkFS].
func (c *Counting) Lstat(name string) (fs.FileInfo, error) {
	c.bump(func(o *Ops) { o.Lstat++ })
	return c.inner.Lstat(name)
}

// ReadDir implements [io/fs.ReadDirFS].
func (c *Counting) ReadDir(name string) ([]fs.DirEntry, error) {
	c.bump(func(o *Ops) { o.ReadDir++ })
	return c.inner.ReadDir(name)
}

// ReadFile implements [io/fs.ReadFileFS].
func (c *Counting) ReadFile(name string) ([]byte, error) {
	c.bump(func(o *Ops) { o.ReadFile++ })
	return c.inner.ReadFile(name)
}

// ReadLink implements [io/fs.ReadLinkFS].
func (c *Counting) ReadLink(name string) (string, error) {
	c.bump(func(o *Ops) { o.ReadLink++ })
	return c.inner.ReadLink(name)
}

// Fault describes an error to inject.
type Fault struct {
	// Path is the workspace-relative path the fault applies to. An empty Path
	// matches every path.
	Path string
	// Op is the operation the fault applies to. An empty Op matches every
	// operation.
	Op Op
	// Err is returned in place of the real result.
	Err error
	// AfterN lets the first N matching calls succeed before the fault begins,
	// which is how a mid-read EIO and a race between ReadDir and Lstat are
	// expressed.
	AfterN int
}

// Faulty wraps a filesystem and injects errors, so partial-failure handling is
// a tested behaviour rather than an unexercised branch.
type Faulty struct {
	inner  FS
	mu     sync.Mutex
	faults []*Fault
	seen   map[string]int
}

// NewFaulty wraps inner and arms the given faults.
func NewFaulty(inner FS, faults ...Fault) *Faulty {
	f := &Faulty{inner: inner, seen: make(map[string]int)}
	for i := range faults {
		fault := faults[i]
		f.faults = append(f.faults, &fault)
	}
	return f
}

func (f *Faulty) check(op Op, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fault := range f.faults {
		if fault.Op != "" && fault.Op != op {
			continue
		}
		if fault.Path != "" && fault.Path != name {
			continue
		}
		key := string(op) + "\x00" + name + "\x00" + fault.Path + string(fault.Op)
		f.seen[key]++
		if f.seen[key] > fault.AfterN {
			return fault.Err
		}
	}
	return nil
}

// Open implements [io/fs.FS].
func (f *Faulty) Open(name string) (fs.File, error) {
	if err := f.check(OpOpen, name); err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return f.inner.Open(name)
}

// Stat implements [io/fs.StatFS].
func (f *Faulty) Stat(name string) (fs.FileInfo, error) {
	if err := f.check(OpStat, name); err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return f.inner.Stat(name)
}

// Lstat implements [io/fs.ReadLinkFS].
func (f *Faulty) Lstat(name string) (fs.FileInfo, error) {
	if err := f.check(OpLstat, name); err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: err}
	}
	return f.inner.Lstat(name)
}

// ReadDir implements [io/fs.ReadDirFS].
func (f *Faulty) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := f.check(OpReadDir, name); err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	return f.inner.ReadDir(name)
}

// ReadFile implements [io/fs.ReadFileFS].
func (f *Faulty) ReadFile(name string) ([]byte, error) {
	if err := f.check(OpReadFile, name); err != nil {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: err}
	}
	return f.inner.ReadFile(name)
}

// ReadLink implements [io/fs.ReadLinkFS].
func (f *Faulty) ReadLink(name string) (string, error) {
	if err := f.check(OpReadLink, name); err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	return f.inner.ReadLink(name)
}
