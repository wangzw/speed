package sso

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// The sweep schedule. Every replica runs the sweep, and the deletes are
// conditional, so two replicas sweeping at once never remove one row twice.
const (
	// claimsEvery is how often the unverified domain claims are looked at. A
	// claim is removed only once it is past the retention window, so the pace
	// only bounds how late that happens.
	claimsEvery = time.Hour
	// assertionsEvery is how often the expired assertion identifiers are looked
	// at. They are removed as soon as their condition has passed, and the pace
	// bounds how long a spent identifier stays in the table.
	assertionsEvery = 10 * time.Minute
	// unverifiedClaimRetention is how long an unverified claim is kept before the
	// sweep removes it: long enough for a tenant to publish the TXT record, short
	// enough that a squatting claim does not stay forever.
	unverifiedClaimRetention = 7 * 24 * time.Hour
	// stopWait is how long Stop waits for the sweep goroutine to end.
	stopWait = 5 * time.Second
)

// errSweepWait is what Stop returns when the sweep did not end within stopWait.
var errSweepWait = errors.New("the sweep did not end in time")

// sweeper is the bounded goroutine that removes the expired assertion
// identifiers and the domain claims left unverified.
type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startSweep starts the goroutine. It sweeps on each table's own period until
// stopped: the unverified claims hourly and the assertion identifiers every ten
// minutes. A failure is a Warn and the goroutine keeps going, because the next
// period tries again and the deletes are conditional.
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

// stopSweep ends the goroutine and waits for it, at most stopWait.
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

// Sweep runs one round of both sweeps. It is the entry the goroutine takes on
// each period, and a test drives it directly.
func (p *product) Sweep(ctx context.Context) error {
	if err := p.sweepClaims(ctx); err != nil {
		return err
	}
	return p.sweepAssertions(ctx)
}

// sweepClaims removes the domain claims left unverified past the retention
// window, in batches, until a batch comes back short.
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

// sweepAssertions removes the assertion identifiers past their condition, in
// batches, until a batch comes back short.
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
