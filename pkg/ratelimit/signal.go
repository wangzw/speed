package ratelimit

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type reason string

const (
	reasonCounterRead       reason = "reading the previous window's count failed"
	reasonCounterNotNumeric reason = "the previous window's key holds no number"
	reasonCounterIncrement  reason = "incrementing the current window's count failed"
	reasonThresholdRead     reason = "reading the thresholds failed"
	reasonThresholdDomain   reason = "the window or the limit is outside its domain"

	reasonShieldRead   reason = "reading the address shield's thresholds failed"
	reasonShieldDomain reason = "an address shield threshold is outside its domain"
)

var reasons = []reason{
	reasonCounterRead,
	reasonCounterNotNumeric,
	reasonCounterIncrement,
	reasonThresholdRead,
	reasonThresholdDomain,
	reasonShieldRead,
	reasonShieldDomain,
}

const recoveryQuiet = time.Minute

type signal struct {
	out io.Writer
	now func() time.Time

	mu          sync.Mutex
	failing     map[reason]bool
	lastFailure map[reason]time.Time
	counts      map[reason]int64

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

func (s *signal) settle(d decision) {
	for _, r := range d.evaluated {
		if r == d.tripped {
			s.report(r, whereSeen(r, d))
			continue
		}
		s.recover(r)
	}
}

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
