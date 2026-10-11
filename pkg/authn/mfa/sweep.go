package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// sweepInterval is how often the sweep runs. Every replica runs it, and the
// deletes are conditional, so two replicas sweeping at once delete nothing twice.
const sweepInterval = time.Hour

// stopWait is how long Stop waits for the sweep to end. The context Stop is given
// has had its cancellation taken off, so this is the bound that is the module's
// own.
const stopWait = 5 * time.Second

// errSweepWait is what Stop returns when the sweep did not end within stopWait.
var errSweepWait = errors.New("the sweep did not end in time")

// sweeper is the bounded goroutine that removes pending rows past pending-ttl.
type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startSweep starts the goroutine. It sweeps once as it starts, so a process
// that restarts more often than the interval still sweeps, and then every
// sweepInterval until stopped.
func (m *product) startSweep(ctx context.Context) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	sw := &sweeper{cancel: cancel, done: make(chan struct{})}
	m.sweeper = sw
	interval := m.sweepEvery
	go func() {
		defer close(sw.done)
		defer cancel()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if _, err := m.Sweep(runCtx); err != nil && runCtx.Err() == nil {
				m.logger.Warn(ModuleName+": sweeping the pending factors failed", slog.String("cause", err.Error()))
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopSweep ends the goroutine and waits for it, at most stopWait. A sweep that
// is in the middle of a statement is cancelled with the statement.
func (m *product) stopSweep() error {
	sw := m.sweeper
	if sw == nil {
		return nil
	}
	sw.cancel()
	timer := time.NewTimer(m.stopBound)
	defer timer.Stop()
	select {
	case <-sw.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: %s", errSweepWait, ModuleName)
	}
}

// Sweep removes the pending rows that have been pending longer than pending-ttl,
// and returns how many it removed. A row is removed by a statement conditional on
// its still being pending, so a registration that finishes while the sweep reads
// its row keeps it, and a row of a registration still inside pending-ttl is never
// read at all. The data a factor keeps for a removed row is removed after the row,
// and a failure there is a Warn and leaves that data behind.
func (m *product) Sweep(ctx context.Context) (int, error) {
	cutoff := m.clock.Now().Add(-m.settings.pendingTTL())
	total := 0
	for {
		batch, err := m.register.expiredPending(ctx, cutoff)
		if err != nil {
			return total, err
		}
		removedInBatch := 0
		for _, rec := range batch {
			removed, err := m.register.removePending(ctx, rec.ID)
			if err != nil {
				return total, err
			}
			if removed {
				removedInBatch++
				m.cleanKind(ctx, rec)
			}
		}
		total += removedInBatch
		if len(batch) < sweepBatch || removedInBatch == 0 {
			return total, nil
		}
	}
}
