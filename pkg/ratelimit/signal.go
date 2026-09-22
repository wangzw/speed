package ratelimit

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// reason is one class of failure that makes this layer let a request through.
//
// The classes are kept apart rather than folded into a single "something went
// wrong" state: they do not imply one another. While the counter backend is
// unreachable, the thresholds class still has to be able to report itself, and
// a shared flag would keep it from ever writing a line.
type reason string

const (
	// reasonCounterRead covers an unreachable backend as well: the primitives
	// answer a failed read with one error, and which of the two it was is not
	// a distinction this layer can draw.
	reasonCounterRead       reason = "reading the previous window's count failed"
	reasonCounterNotNumeric reason = "the previous window's key holds no number"
	reasonCounterIncrement  reason = "incrementing the current window's count failed"
	reasonThresholdRead     reason = "reading the thresholds failed"
	reasonThresholdDomain   reason = "the window or the limit is outside its domain"
)

// reasons is every class this module reports, named once so the per-class maps
// are sized from the same list the classes are declared in rather than from a
// number kept beside it.
var reasons = []reason{
	reasonCounterRead,
	reasonCounterNotNumeric,
	reasonCounterIncrement,
	reasonThresholdRead,
	reasonThresholdDomain,
}

// signal reports the classes of failure that made this layer stand aside.
//
// Each class carries its own flag and its own count. The first failure of a
// class writes one line; the recovery writes another; the failures in between
// only accumulate in the count, so a sustained outage does not fill the
// output. The counts stay in the process and are not exposed.
//
// The lines go to the process's standard error and not through the logging
// module, for the same reason the logging module reports its own write
// failures that way: the reporting chain may be absent, or switched off
// entirely.
type signal struct {
	out io.Writer

	mu      sync.Mutex
	failing map[reason]bool
	counts  map[reason]int64

	// written counts the lines, so a test can watch the reporting without
	// reading the process's standard error.
	written atomic.Int64
}

func newSignal() *signal {
	return newSignalTo(os.Stderr)
}

func newSignalTo(out io.Writer) *signal {
	return &signal{
		out:     out,
		failing: make(map[reason]bool, len(reasons)),
		counts:  make(map[reason]int64, len(reasons)),
	}
}

// settle accounts for one decision: it reports the class that decision
// tripped, if any, and clears every class the decision evaluated without
// tripping.
//
// A class clears in a decision that evaluated it and did not trip it — not in
// one that succeeded outright, and not in one that returned at an earlier
// class. Such a decision never asked about the classes behind it, so clearing
// them there would print "back to limiting" for a condition nothing checked.
// A class ahead of the trip is still cleared while one behind it keeps
// failing, which is what keeps a fixed condition from waiting on an unfixed
// one.
func (s *signal) settle(d decision) {
	for _, r := range d.evaluated {
		if r == d.tripped {
			s.report(r)
			continue
		}
		s.recover(r)
	}
}

func (s *signal) report(r reason) {
	s.mu.Lock()
	s.counts[r]++
	first := !s.failing[r]
	s.failing[r] = true
	s.mu.Unlock()
	if !first {
		return
	}
	s.written.Add(1)
	// The write failure is dropped, and there is nowhere to put it: the
	// writer is the report sink itself, so a line that does not get there has
	// no second channel left to travel by.
	//nolint:errcheck // the writer is the report sink itself.
	fmt.Fprintf(s.out, "%s: letting requests through: %s\n", ModuleName, r)
}

func (s *signal) recover(r reason) {
	s.mu.Lock()
	was := s.failing[r]
	s.failing[r] = false
	s.mu.Unlock()
	if !was {
		return
	}
	s.written.Add(1)
	//nolint:errcheck // the writer is the report sink itself.
	fmt.Fprintf(s.out, "%s: back to limiting: %s\n", ModuleName, r)
}
