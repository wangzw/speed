package journal

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// signal reports the first write failure and the recovery from it.
//
// It writes to standard error and not through the log chain, and that is the
// one thing about it worth stating. The log chain is where a report would
// ordinarily go and it is exactly what cannot be used here: an empty output
// list is a legitimate configuration that turns the whole chain off, and the
// moment writes start failing is the moment a report is worth most. The log
// module reports its own failures the same way, for the same reason.
//
// A fault produces one line and its end produces one more. Every failure while
// the last one still stands is counted and not written, so a table that is gone
// for an hour does not flush a line per entry; the count is not observable from
// outside, which is why this is reported as a gap in what a host can notice
// rather than as a metric.
//
// One instance guards one writer, built with the product and shared by the two
// paths that report through it. Statement capture does not report here at all:
// its row and the write it records are one unit of work, so its failure is
// raised on that work rather than reported.
type signal struct {
	mu     sync.Mutex
	failed bool
	count  int
	out    io.Writer
}

// newSignal builds a signal writing to standard error.
func newSignal() *signal { return &signal{out: os.Stderr} }

// fail reports a failure, once per fault.
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

// recover reports that writing works again, once per fault.
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

// report writes one line, on the writer this signal was built with.
//
// The write failure is dropped, and there is nowhere to put it: the signal's
// writer is the standard error, so a line that does not get there has no second
// channel left to be reported through. Reporting it would need a channel that
// does not itself depend on this one working.
func (s *signal) report(format string, args ...any) {
	//nolint:errcheck // the writer is the report sink itself.
	fmt.Fprintf(s.out, diagnosticPrefix+ModuleName+": "+format+"\n", args...)
}
