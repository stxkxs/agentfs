package report

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func TestIsBrokenPipe(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"epipe", syscall.EPIPE, true},
		{"wrapped epipe", fmt.Errorf("write scan envelope: %w", syscall.EPIPE), true},
		{"twice wrapped epipe", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", syscall.EPIPE)), true},
		{"path error", &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}, true},
		{"closed in-process pipe", io.ErrClosedPipe, true},
		{"wrapped closed in-process pipe", fmt.Errorf("write: %w", io.ErrClosedPipe), true},
		{"connection reset", syscall.ECONNRESET, false},
		{"no space", syscall.ENOSPC, false},
		{"closed file", fs.ErrClosed, false},
		{"unrelated", errors.New("device is on fire"), false},
		{"short write", io.ErrShortWrite, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBrokenPipe(tt.err); got != tt.want {
				t.Errorf("IsBrokenPipe(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsBrokenPipeOnClosedInProcessPipe(t *testing.T) {
	pr, pw := io.Pipe()
	if err := pr.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}

	err := NewEnvelope(KindScan, testVersion, "/w", CodeOK).WriteJSON(pw)
	if err == nil {
		t.Fatal("writing to a pipe with a closed reader succeeded")
	}
	if !IsBrokenPipe(err) {
		t.Errorf("IsBrokenPipe(%v) = false", err)
	}
}

func TestIsBrokenPipeOnClosedOSPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("EPIPE is the POSIX report for a write whose pipe reader is gone")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe: %v", err)
	}
	defer func() {
		if err := pw.Close(); err != nil {
			t.Errorf("close write end: %v", err)
		}
	}()
	if err := pr.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}

	writeErr := NewStream(pw).Write(RecordChange, "k", testInstant, "payload")
	if writeErr == nil {
		t.Fatal("writing to a pipe with a closed reader succeeded")
	}
	if !IsBrokenPipe(writeErr) {
		t.Errorf("IsBrokenPipe(%v) = false", writeErr)
	}
}
