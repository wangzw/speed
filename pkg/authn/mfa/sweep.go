package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const sweepInterval = time.Hour

const stopWait = 5 * time.Second

var errSweepWait = errors.New("the sweep did not end in time")

type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

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

// Sweep removes pending rows past pending-ttl and returns how many it removed. Each delete is conditional on the row still being pending, so a registration that is finishing keeps its row.
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
