package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/sms"
)

const stopWait = 5 * time.Second

var errPoolWait = errors.New("the sending pool did not drain in time")

type pool struct {
	queue   chan sms.Resolved
	workers int

	parent   context.Context
	stop     chan struct{}
	stopOnce sync.Once
	started  bool
	wg       sync.WaitGroup
	done     chan struct{}
}

func newPool(workers, queueSize int) *pool {
	return &pool{
		queue:   make(chan sms.Resolved, queueSize),
		workers: workers,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (p *product) poolFull() bool {
	q := p.pool.queue
	return len(q) >= cap(q)
}

func (p *product) enqueue(r sms.Resolved) bool {
	select {
	case p.pool.queue <- r:
		return true
	default:
		return false
	}
}

func (p *product) startPool(ctx context.Context) {
	if p.pool.started {
		return
	}
	p.pool.started = true
	parent := context.WithoutCancel(ctx)
	runCtx, cancel := context.WithCancel(parent)
	p.pool.parent = parent
	for range p.pool.workers {
		p.pool.wg.Add(1)
		go func() {
			defer p.pool.wg.Done()
			p.work(runCtx)
		}()
	}
	go func() {
		p.pool.wg.Wait()
		close(p.pool.done)
	}()
	go func() {
		<-p.pool.stop
		cancel()
	}()
}

func (p *product) work(runCtx context.Context) {
	for {
		select {
		case m := <-p.pool.queue:
			p.deliverOne(runCtx, m)
		case <-runCtx.Done():
			drainCtx, cancel := context.WithTimeout(p.pool.parent, p.stopBound)
			p.drainQueue(drainCtx)
			cancel()
			return
		}
	}
}

func (p *product) drainQueue(ctx context.Context) {
	for {
		select {
		case m := <-p.pool.queue:
			p.deliverOne(ctx, m)
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

func (p *product) stopPool() error {
	if p.pool == nil || !p.pool.started {
		return nil
	}
	p.pool.stopOnce.Do(func() { close(p.pool.stop) })
	timer := time.NewTimer(p.stopBound)
	defer timer.Stop()
	select {
	case <-p.pool.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: %s", errPoolWait, ModuleName)
	}
}

func (p *product) deliverOne(ctx context.Context, r sms.Resolved) {
	started := time.Now()
	receipt, err := p.sender.Send(ctx, r)
	elapsed := time.Since(started)
	switch {
	case err == nil:
		p.meters.sent(ctx, outcomeSent)
		p.infoSent(ctx, receipt, elapsed)
	case errors.Is(err, sms.ErrUncertain):
		p.meters.sent(ctx, outcomeUncertain)
		p.warnNotSent(ctx, outcomeUncertain, err, carrierAttributes(receipt, elapsed)...)
	case errors.Is(err, sms.ErrThrottled):
		p.meters.sent(ctx, outcomeThrottled)
		p.warnNotSent(ctx, outcomeThrottled, err, carrierAttributes(receipt, elapsed)...)
		p.refundChallenge(ctx, r.DedupeID)
	case errors.Is(err, sms.ErrRejected):
		p.meters.sent(ctx, outcomeRejected)
		p.warnNotSent(ctx, outcomeRejected, err, carrierAttributes(receipt, elapsed)...)
		p.refundChallenge(ctx, r.DedupeID)
	case errors.Is(err, sms.ErrUnroutable):
		// A destination this carrier cannot carry is a definite failure recorded under its own outcome, so an operator reads a deployment limit rather than a refusal.
		p.meters.sent(ctx, outcomeUnroutable)
		p.warnNotSent(ctx, outcomeUnroutable, err, carrierAttributes(receipt, elapsed)...)
		p.refundChallenge(ctx, r.DedupeID)
	default:
		// An error outside the closed set is a carrier defect, read as uncertain, never as a delivery.
		p.meters.sent(ctx, outcomeUncertain)
		p.warnNotSent(ctx, outcomeUncertain, err, carrierAttributes(receipt, elapsed)...)
	}
}

func (p *product) refundChallenge(ctx context.Context, id string) {
	if id == "" {
		return
	}
	now := p.clock.Now()
	eventID, err := p.register.refund(ctx, id, now)
	if err != nil {
		p.logger.WarnContext(ctx, ModuleName+": a challenge could not be refunded and stays counted",
			slog.String("cause", err.Error()))
	}
	if eventID == 0 || p.ledger == nil {
		return
	}
	if err := p.ledger.MarkRefunded(ctx, eventID, now); err != nil {
		p.logger.WarnContext(ctx, ModuleName+": a ledger attempt could not be refunded and stays counted",
			slog.String("cause", err.Error()))
	}
}

func carrierAttributes(receipt sms.Receipt, elapsed time.Duration) []any {
	attrs := []any{slog.Int64("duration_ms", elapsed.Milliseconds())}
	if receipt.ProviderID != "" {
		attrs = append(attrs, slog.String("provider_id", receipt.ProviderID))
	}
	return attrs
}

func (p *product) infoSent(ctx context.Context, receipt sms.Receipt, elapsed time.Duration) {
	p.logger.InfoContext(ctx, ModuleName+": a verification code was sent",
		slog.String("outcome", outcomeSent),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
		slog.String("provider_id", receipt.ProviderID))
}

func (p *product) warnNotSent(ctx context.Context, outcome string, err error, carrier ...any) {
	attrs := []any{slog.String("outcome", outcome), slog.String("cause", err.Error())}
	attrs = append(attrs, carrier...)
	p.logger.WarnContext(ctx, ModuleName+": a verification code was not sent", attrs...)
}
