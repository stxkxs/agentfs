package report

import "bytes"

// errWriter fails every write with a fixed error.
type errWriter struct{ err error }

func (w errWriter) Write(p []byte) (int, error) { return 0, w.err }

// flakyWriter fails the failOn-th write, counting from one, and buffers the
// rest. It is how a test observes what a producer does after a write it could
// not complete.
type flakyWriter struct {
	failOn int
	err    error
	n      int
	buf    bytes.Buffer
}

func (w *flakyWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n == w.failOn {
		return 0, w.err
	}
	return w.buf.Write(p)
}

func (w *flakyWriter) String() string { return w.buf.String() }
