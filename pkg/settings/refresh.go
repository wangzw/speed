package settings

import (
	"context"
	"time"
)

// The periodic refresh is the backstop under the change events.
//
// Events are the fast path: a write reloads its own replica at once and the
// bus carries the news to the others. But the bus promises no delivery count,
// so a replica that never receives one would otherwise serve a stale value
// until it restarted. The ticker closes that gap, at the price of an interval
// of staleness in the worst case.
//
// A refresh interval of zero turns the ticker off. That is a deliberate
// choice a host can make: it accepts convergence through events alone, and
// with it the possibility that a change this replica never hears about stays
// invisible until the process restarts.

// startRefresh begins the periodic reload, unless the configured interval is
// zero.
func (s *settingsStore) startRefresh(ctx context.Context) {
	if s.config.refresh <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancelRefresh = cancel
	interval := s.config.refresh
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reload(ctx)
			}
		}
	}()
}

// stopRefresh cancels the ticker.
//
// Nothing waits for the goroutine: the work it may be in the middle of is a
// read that builds a snapshot nothing holds yet, so a cancelled refresh leaves
// nothing to collect. Close does not wait either, for the same reason.
func (s *settingsStore) stopRefresh() {
	if s.cancelRefresh != nil {
		s.cancelRefresh()
		s.cancelRefresh = nil
	}
}
