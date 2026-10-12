package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// sweepInterval is how often the sweep runs. Every replica runs it, and the
// deletes are conditional, so two replicas sweeping at once remove no row twice.
const sweepInterval = 10 * time.Minute

// sweepAfter is how long after a challenge's expiry the sweep may remove it. It
// is the longest counting window — one day — plus an hour of margin, so a row
// inside a window is never removed early and no budget is under-counted. Because
// a row's expiry is at most code-ttl past its creation and that is far under the
// margin, the expiry-based cutoff keeps every row a creation-based one would.
const sweepAfter = 25 * time.Hour

// errSweepWait is what Stop returns when the sweep did not end within stopWait.
var errSweepWait = errors.New("the sweep did not end in time")

// sweeper is the bounded goroutine that removes the challenges past the counting
// windows.
type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startSweep starts the goroutine. It sweeps once as it starts, so a process
// that restarts more often than the interval still sweeps, and then every
// sweepInterval until stopped.
func (p *product) startSweep(ctx context.Context) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	sw := &sweeper{cancel: cancel, done: make(chan struct{})}
	p.sweeper = sw
	go func() {
		defer close(sw.done)
		defer cancel()
		ticker := time.NewTicker(p.sweepEvery)
		defer ticker.Stop()
		for {
			if _, err := p.Sweep(runCtx); err != nil && runCtx.Err() == nil {
				p.logger.Warn(ModuleName+": sweeping the challenges failed", slog.String("cause", err.Error()))
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopSweep ends the goroutine and waits for it, at most stopBound.
func (p *product) stopSweep() error {
	sw := p.sweeper
	if sw == nil {
		return nil
	}
	sw.cancel()
	timer := time.NewTimer(p.stopBound)
	defer timer.Stop()
	select {
	case <-sw.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: %s", errSweepWait, ModuleName)
	}
}

// Sweep removes the challenges past the counting window, in batches, and returns
// how many it removed. The cutoff is the given margin before now; a row inside a
// window is never removed, so no budget is under-counted.
func (p *product) Sweep(ctx context.Context) (int64, error) {
	cutoff := p.clock.Now().Add(-sweepAfter)
	var total int64
	for {
		removed, err := p.register.removeExpired(ctx, cutoff)
		if err != nil {
			return total, err
		}
		total += removed
		if removed < sweepBatch {
			return total, nil
		}
	}
}
