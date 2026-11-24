package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const sweepInterval = 10 * time.Minute

const sweepAfter = 25 * time.Hour

var errSweepWait = errors.New("the sweep did not end in time")

type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

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

// Sweep removes the challenges past the counting window in batches and returns how many it removed.
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
