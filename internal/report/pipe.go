package report

import (
	"errors"
	"io"
	"syscall"
)

// IsBrokenPipe reports whether err is a write to a pipe whose reader is gone.
//
// It is the ordinary end of a piped invocation — a reader such as head closes
// the read end once it has the lines it wanted — and not a fault. Every
// write path in this package returns it rather than swallowing it, because a
// caller that ignores a write error goes on emitting into a void and exits
// reporting success. A caller that recognizes it stops and exits without
// reporting an internal error.
func IsBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}
