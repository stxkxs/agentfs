package cli

import (
	"errors"
	"testing"
)

// stopping fails its first write and records every write after it.
type stopping struct {
	err   error
	after []string
}

func (s *stopping) Write(b []byte) (int, error) {
	if s.err != nil {
		err := s.err
		s.err = nil
		return 0, err
	}
	s.after = append(s.after, string(b))
	return len(b), nil
}

// A command writes many lines and can act on only one outcome. The first
// failure is the one it acts on, and nothing follows it: a result whose first
// half was lost is not completed as though it were whole.
func TestAPrinterKeepsTheFirstFailureAndWritesNothingAfterIt(t *testing.T) {
	t.Parallel()
	first := errors.New("device is on fire")
	w := &stopping{err: first}

	p := newPrinter(w)
	p.write([]byte("the head of a result"))
	p.printf("count %d\n", 1)
	p.println("a line")
	p.write([]byte("the tail"))

	if !errors.Is(p.Err(), first) {
		t.Errorf("Err() = %v, want %v", p.Err(), first)
	}
	if len(w.after) != 0 {
		t.Errorf("writing continued past the failure: %q", w.after)
	}
}
