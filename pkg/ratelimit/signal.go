package ratelimit

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
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

	// The shield's two classes. They are the quota's threshold classes for the
	// second layer: a failed read of the shield's settings, and a value outside
	// one of its domains. They are kept apart from the quota's because the two
	// layers read different items on different paths, and one line for both
	// would not say which layer stood aside.
	reasonShieldRead   reason = "reading the address shield's thresholds failed"
	reasonShieldDomain reason = "an address shield threshold is outside its domain"
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
	reasonShieldRead,
	reasonShieldDomain,
}

// recoveryQuiet is how long a class has to go without failing before a
// decision that evaluates it may report it recovered.
//
// A failure that holds for some decisions and not others alternates with
// decisions that pass: one tenant's row out of its domain, one key holding
// something this module did not write, a backend that answers some calls and
// not others. Cleared by the first passing decision, such a class would write
// a failure line and a recovery line for every alternation, for as long as the
// condition lasts. With the quiet period it writes one line when it starts and
// one when it has really stopped.
const recoveryQuiet = time.Minute

// signal reports the classes of failure that made this layer stand aside.
//
// Each class carries its own flag and its own count. The first failure of a
// class writes one line; the recovery writes another; the failures in between
// only accumulate in the count, so a sustained failure does not fill the
// output, whether it holds for every decision or for some of them. The counts
// stay in the process and are not exposed.
//
// The lines go to the process's standard error and not through the logging
// module, for the same reason the logging module reports its own write
// failures that way: the reporting chain may be absent, or switched off
// entirely.
type signal struct {
	out io.Writer
	// now reads the clock the quiet period is measured on. A test pins it.
	now func() time.Time

	mu          sync.Mutex
	failing     map[reason]bool
	lastFailure map[reason]time.Time
	counts      map[reason]int64

	// written counts the lines, so a test can watch the reporting without
	// reading the process's standard error.
	written atomic.Int64
}

func newSignal() *signal {
	return newSignalTo(os.Stderr)
}

func newSignalTo(out io.Writer) *signal {
	return &signal{
		out:         out,
		now:         time.Now,
		failing:     make(map[reason]bool, len(reasons)),
		lastFailure: make(map[reason]time.Time, len(reasons)),
		counts:      make(map[reason]int64, len(reasons)),
	}
}

// settle accounts for one decision: it reports the class that decision
// tripped, if any, and clears every class the decision evaluated without
// tripping, once that class has been quiet for recoveryQuiet.
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
			s.report(r, whereSeen(r, d))
			continue
		}
		s.recover(r)
	}
}

// whereSeen is what a failure line adds about the decision that first tripped
// the class, when the class has a scope worth naming.
//
// The thresholds are read from the settings layer of the request's tenant, so
// a value out of its domain is most often one tenant's row, and the line names
// the tenant the failure was first seen for: without it the operator knows a
// row is bad and not whose. It is where the failure was seen, not a claim that
// the row belongs to that tenant — the read falls back to the global row and
// the declared default. The counter classes add nothing: their key embeds the
// identity string, which is not for this line to repeat.
func whereSeen(r reason, d decision) string {
	if r != reasonThresholdRead && r != reasonThresholdDomain {
		return ""
	}
	if d.tenant == "" {
		return " (first seen for a request without a tenant)"
	}
	return fmt.Sprintf(" (first seen for tenant %q)", d.tenant)
}

func (s *signal) report(r reason, where string) {
	s.mu.Lock()
	s.counts[r]++
	s.lastFailure[r] = s.now()
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
	fmt.Fprintf(s.out, "%s: letting requests through: %s%s\n", ModuleName, r, where)
}

func (s *signal) recover(r reason) {
	s.mu.Lock()
	quiet := s.failing[r] && s.now().Sub(s.lastFailure[r]) >= recoveryQuiet
	if quiet {
		s.failing[r] = false
	}
	s.mu.Unlock()
	if !quiet {
		return
	}
	s.written.Add(1)
	//nolint:errcheck // the writer is the report sink itself.
	fmt.Fprintf(s.out, "%s: back to limiting: %s\n", ModuleName, r)
}
