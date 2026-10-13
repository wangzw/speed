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
// plaintext code lives in them and nowhere else while it waits.
type pool struct {
	queue   chan sms.Message
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
		queue:   make(chan sms.Message, queueSize),
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

// enqueue hands a message to the pool without blocking. It reports false when
// the queue is full, which the caller records as a drop.
func (p *product) enqueue(m sms.Message) bool {
	select {
	case p.pool.queue <- m:
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
// carrier's answer is a metric and a log line and nothing else.
func (p *product) deliverOne(ctx context.Context, m sms.Message) {
	_, err := p.sender.Send(ctx, m)
	switch {
	case err == nil:
		p.meters.sent(ctx, outcomeSent)
	case errors.Is(err, sms.ErrUncertain):
		p.meters.sent(ctx, outcomeUncertain)
		p.warnCarrier(ctx, outcomeUncertain, err)
	case errors.Is(err, sms.ErrThrottled):
		p.meters.sent(ctx, outcomeThrottled)
		p.warnCarrier(ctx, outcomeThrottled, err)
	case errors.Is(err, sms.ErrRejected):
		p.meters.sent(ctx, outcomeRejected)
		p.warnCarrier(ctx, outcomeRejected, err)
	default:
		// An error outside the closed set is a defect of the carrier. It is read
		// as uncertain — never as a delivery and never as a retry — which is the
		// direction that cannot do harm if the message did go out.
		p.meters.sent(ctx, outcomeUncertain)
		p.warnCarrier(ctx, outcomeUncertain, err)
	}
}

// warnCarrier writes the line for a send the carrier did not accept. The line
// carries the outcome and the carrier's own error text, which the carrier must
// keep free of the code and its credentials.
func (p *product) warnCarrier(ctx context.Context, outcome string, err error) {
	p.logger.WarnContext(ctx, ModuleName+": a verification code was not accepted by the carrier",
		slog.String("outcome", outcome), slog.String("cause", err.Error()))
}
