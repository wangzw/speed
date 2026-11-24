package sso

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const (
	claimsEvery              = time.Hour
	assertionsEvery          = 10 * time.Minute
	unverifiedClaimRetention = 7 * 24 * time.Hour
	stopWait                 = 5 * time.Second
)

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
		claims := time.NewTicker(p.claimsEvery)
		defer claims.Stop()
		assertions := time.NewTicker(p.assertionsEvery)
		defer assertions.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-claims.C:
				if err := p.sweepClaims(runCtx); err != nil && runCtx.Err() == nil {
					p.logger.Warn(diagnosticPrefix+ModuleName+": sweeping the domain claims failed",
						slog.String("cause", err.Error()))
				}
			case <-assertions.C:
				if err := p.sweepAssertions(runCtx); err != nil && runCtx.Err() == nil {
					p.logger.Warn(diagnosticPrefix+ModuleName+": sweeping the assertion identifiers failed",
						slog.String("cause", err.Error()))
				}
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

// Sweep runs one round of both sweeps. It is the entry the goroutine takes on each period.
func (p *product) Sweep(ctx context.Context) error {
	if err := p.sweepClaims(ctx); err != nil {
		return err
	}
	return p.sweepAssertions(ctx)
}

func (p *product) sweepClaims(ctx context.Context) error {
	cutoff := p.now().Add(-unverifiedClaimRetention)
	for {
		removed, err := p.register.removeStaleClaims(ctx, cutoff)
		if err != nil {
			return p.unavailable(err)
		}
		if removed < sweepBatch {
			return nil
		}
	}
}

func (p *product) sweepAssertions(ctx context.Context) error {
	cutoff := p.now()
	for {
		removed, err := p.register.removeExpiredAssertions(ctx, cutoff)
		if err != nil {
			return p.unavailable(err)
		}
		if removed < sweepBatch {
			return nil
		}
	}
}
