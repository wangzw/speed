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
	done := make(chan struct{})
	s.cancelRefresh, s.refreshDone = cancel, done
	interval := s.config.refresh
	go func() {
		defer close(done)
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

// stopRefresh cancels the ticker and waits for its goroutine to return.
//
// The wait is what keeps the refresh from outliving Stop. A reload in flight
// at that point is a read on the database handle, and the database module
// stops and closes after this one: a read left running would meet the handle
// being closed and report a failure on an ordinary shutdown. The wait is as
// long as that read takes to notice the cancellation, since the read runs on
// the context this cancels.
func (s *settingsStore) stopRefresh() {
	if s.cancelRefresh == nil {
		return
	}
	s.cancelRefresh()
	<-s.refreshDone
	s.cancelRefresh, s.refreshDone = nil, nil
}
