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

// stopWait is how long Stop waits for a background goroutine to end. The
// context Stop is given has had its cancellation taken off, so this is the
// bound that is the module's own.
const stopWait = 5 * time.Second

// errPoolWait is what Stop returns when the pool did not drain within stopWait.
var errPoolWait = errors.New("the sending pool did not drain in time")

// pool is the bounded worker pool that sends queued messages off the request
// path. The queue is in this process and its elements are never stored; the
// plaintext code lives in them and nowhere else while it waits. An element is a
// resolved message: the request path resolves a message against the registry and
// queues what the carrier is handed.
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

// newPool builds a pool of the given size. The queue is allocated here; the
// workers start in Start.
func newPool(workers, queueSize int) *pool {
	return &pool{
		queue:   make(chan sms.Resolved, queueSize),
		workers: workers,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// full reports whether the queue has no room, which the send path checks before
// writing a row so that a full queue is refused before anything is written.
func (p *product) poolFull() bool {
	q := p.pool.queue
	return len(q) >= cap(q)
}

// enqueue hands a resolved message to the pool without blocking. It reports
// false when the queue is full, which the caller records as a drop.
func (p *product) enqueue(r sms.Resolved) bool {
	select {
	case p.pool.queue <- r:
		return true
	default:
		return false
	}
}

// startPool launches the workers. It runs in Start, once. A second call after
// one is already running does nothing.
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
	// Cancelling the run context is what tells the workers to drain; the cancel
	// belongs to the pool and is called when Stop closes the stop channel.
	go func() {
		<-p.pool.stop
		cancel()
	}()
}

// work drains the queue until the run context is cancelled, then empties what is
// left within the drain bound and returns. The run context is cancelled only at
// Stop, so a message is sent with the context of the worker's whole life.
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

// drainQueue sends whatever is already queued, until the queue is empty or the
// context is done.
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

// stopPool ends the workers and waits for them, at most stopBound. A message
// still queued is sent while the bound lasts; one the bound passes is dropped,
// and the user resends.
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

// deliverOne calls the carrier for one queued message and records how the send
// ended. The result never reaches the request that triggered the send: the
// carrier's answer is a metric, a log line, and — when the failure is certain —
// a refund on the challenge row. A certain failure is one that shows the message
// did not enter the carrier's pipeline: a rejection, an unroutable destination
// or a throttled call. An uncertain outcome, and an error outside the closed set,
// leave the row counted, which is the direction that cannot do harm if the
// message did go out.
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
		p.refundChallenge(ctx, r)
	case errors.Is(err, sms.ErrRejected):
		p.meters.sent(ctx, outcomeRejected)
		p.warnNotSent(ctx, outcomeRejected, err, carrierAttributes(receipt, elapsed)...)
		p.refundChallenge(ctx, r)
	case errors.Is(err, sms.ErrUnroutable):
		// A message this carrier cannot carry to this destination is a definite
		// failure, like a rejection and unlike an uncertain outcome: the message is
		// not defective, but nothing the caller changes about it makes this
		// deployment carry it. It is recorded under its own outcome so an operator
		// reads a deployment limit rather than a refusal they can send themselves
		// around.
		p.meters.sent(ctx, outcomeUnroutable)
		p.warnNotSent(ctx, outcomeUnroutable, err, carrierAttributes(receipt, elapsed)...)
		p.refundChallenge(ctx, r)
	default:
		// An error outside the closed set is a defect of the carrier. It is read
		// as uncertain — never as a delivery and never as a retry — which is the
		// direction that cannot do harm if the message did go out.
		p.meters.sent(ctx, outcomeUncertain)
		p.warnNotSent(ctx, outcomeUncertain, err, carrierAttributes(receipt, elapsed)...)
	}
}

// refundChallenge gives the send budgets back for a challenge whose message
// certainly did not reach the carrier, and spends the row in the same statement
// so a code that was never delivered cannot pass the check. The row is named by
// the dedupe identifier the send wrote into the resolved message. A message
// without one was not queued by Send and has no row to refund.
//
// A refund that cannot be written leaves the row counted and the code valid;
// the send has already failed, so the outcome is unchanged and the failure is
// recorded on its own line.
func (p *product) refundChallenge(ctx context.Context, r sms.Resolved) {
	if r.DedupeID == "" {
		return
	}
	if err := p.register.refund(ctx, r.DedupeID, p.clock.Now()); err != nil {
		p.logger.WarnContext(ctx, ModuleName+": a challenge could not be refunded and stays counted",
			slog.String("cause", err.Error()))
	}
}

// carrierAttributes is what a line about one call to the carrier carries beyond
// the outcome: how long the call took, and the carrier's own identifier for the
// send when it gave one. Nothing a message is made of enters it — not the
// number, not the code, not a template parameter.
func carrierAttributes(receipt sms.Receipt, elapsed time.Duration) []any {
	attrs := []any{slog.Int64("duration_ms", elapsed.Milliseconds())}
	if receipt.ProviderID != "" {
		attrs = append(attrs, slog.String("provider_id", receipt.ProviderID))
	}
	return attrs
}

// infoSent writes the line for a code the carrier accepted. The line carries the
// outcome, the carrier's identifier and the call's duration, so a report of a
// message that never arrived can be matched against the carrier's own record.
func (p *product) infoSent(ctx context.Context, receipt sms.Receipt, elapsed time.Duration) {
	p.logger.InfoContext(ctx, ModuleName+": a verification code was sent",
		slog.String("outcome", outcomeSent),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
		slog.String("provider_id", receipt.ProviderID))
}

// warnNotSent writes the line for a code that did not go out, whatever stopped
// it — a carrier that refused the send, a registry that would not resolve the
// message, or a carrier that failed outside the sentinels. The line carries the
// outcome and the cause's own error text, which the carrier keeps free of the
// code and its credentials. A send stopped before the carrier is reached has no
// call to report and passes no carrier attributes.
func (p *product) warnNotSent(ctx context.Context, outcome string, err error, carrier ...any) {
	attrs := []any{slog.String("outcome", outcome), slog.String("cause", err.Error())}
	attrs = append(attrs, carrier...)
	p.logger.WarnContext(ctx, ModuleName+": a verification code was not sent", attrs...)
}
