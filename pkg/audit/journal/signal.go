package journal

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// signal reports the first write failure and the recovery from it.
type signal struct {
	mu     sync.Mutex
	failed bool
	count  int
	out    io.Writer
}

// newSignal writes to standard error, not the log chain: an empty output list
// is a legal configuration that turns the chain off.
func newSignal() *signal { return &signal{out: os.Stderr} }

func (s *signal) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		s.count++
		return
	}
	s.failed = true
	s.count = 1
	s.report("writing an audit entry failed: %v", err)
}

func (s *signal) recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failed {
		return
	}
	s.failed = false
	s.report("writing audit entries recovered after %d failed", s.count)
	s.count = 0
}

func (s *signal) report(format string, args ...any) {
	//nolint:errcheck // the writer is the report sink itself.
	fmt.Fprintf(s.out, diagnosticPrefix+ModuleName+": "+format+"\n", args...)
}
