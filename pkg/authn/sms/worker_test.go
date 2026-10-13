package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

// TestTheWorkerRecordsEachCarrierOutcome holds the carrier classification: the
// three sentinels a carrier returns map to their outcome, and a success maps to
// sent.
func TestTheWorkerRecordsEachCarrierOutcome(t *testing.T) {
	cases := []struct {
		name    string
		result  error
		outcome string
	}{
		{"sent", nil, outcomeSent},
		{"uncertain", sms.ErrUncertain, outcomeUncertain},
		{"throttled", sms.ErrThrottled, outcomeThrottled},
		{"rejected", sms.ErrRejected, outcomeRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.sender.setResult(c.result)
			r.p.deliverOne(context.Background(), sms.Message{To: phone, Params: map[string]string{"code": "123456"}})
			if got := r.counter(sendsName, attribute.String(outcomeAttribute, c.outcome)); got != 1 {
				t.Errorf("outcome %s = %d, want 1", c.outcome, got)
			}
		})
	}
}

// TestACarrierErrorOutsideTheClosedSetIsUncertain holds the defect direction: an
// error that is none of the three sentinels is read as uncertain, never as a
// delivery and never as a retry.
func TestACarrierErrorOutsideTheClosedSetIsUncertain(t *testing.T) {
	r := newRigSQLite(t)
	r.sender.setResult(errors.New("a carrier defect"))
	r.p.deliverOne(context.Background(), sms.Message{To: phone})
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeUncertain)); got != 1 {
		t.Errorf("an unrecognised carrier error recorded outcome uncertain %d times, want 1", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeSent)); got != 0 {
		t.Errorf("an unrecognised carrier error recorded sent %d times, want 0", got)
	}
}

// TestEveryOutcomeIsRecorded holds the closed set of the outcome attribute: each
// value is reachable from a real path, so a dashboard's set is the whole set.
func TestEveryOutcomeIsRecorded(t *testing.T) {
	r := newRigSQLite(t)
	ctx := context.Background()

	// unsupported-region: the allowed table is empty.
	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrUnsupportedRegion) {
		t.Fatalf("the empty-table send returned %v", err)
	}
	r.allow()

	// suppressed: Deliver false.
	if _, err := r.p.Send(ctx, authn.SendCode{Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false}); err != nil {
		t.Fatalf("the suppressed send returned %v", err)
	}

	// dropped: the queue is full.
	for range cap(r.p.pool.queue) {
		r.p.pool.queue <- sms.Message{To: phone}
	}
	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("the full-queue send returned %v", err)
	}
	for len(r.p.pool.queue) > 0 {
		<-r.p.pool.queue
	}
	// Move on so the rows written above are past every counting window.
	r.clock.advance(2 * time.Hour)

	// budget-exhausted: the global login budget is spent. A second number is used
	// so the per-number budgets are not what refuses.
	r.store.set(globalLoginPath, 1)
	if _, err := r.loginTo("+8613800138009"); err != nil {
		t.Fatalf("the first budgeted send returned %v", err)
	}
	if _, err := r.loginTo("+8613800138010"); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("the budget-exhausted send returned %v", err)
	}

	// The four carrier outcomes, driven directly.
	for _, res := range []error{nil, sms.ErrUncertain, sms.ErrThrottled, sms.ErrRejected} {
		r.sender.setResult(res)
		r.p.deliverOne(ctx, sms.Message{To: phone})
	}

	for _, outcome := range []string{
		outcomeSent, outcomeUncertain, outcomeThrottled, outcomeRejected,
		outcomeUnsupportedRegion, outcomeBudgetExhausted, outcomeSuppressed, outcomeDropped,
	} {
		if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcome)); got < 1 {
			t.Errorf("outcome %s was never recorded", outcome)
		}
	}
}

// TestTheWorkersDrainQueuedMessagesOffTheRequestPath holds the pool: a started
// pool sends what Send queued, without the request waiting for the carrier.
func TestTheWorkersDrainQueuedMessagesOffTheRequestPath(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	delivered := make(chan struct{}, 1)
	r.sender.onSend = func(sms.Message) {
		select {
		case delivered <- struct{}{}:
		default:
		}
	}
	r.startPool()
	defer r.stopPool()

	id, err := r.sendLogin()
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id == "" {
		t.Fatal("Send returned an empty challenge identifier")
	}
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never called the carrier")
	}
}

// TestStoppingAnUnstartedPoolReturnsAtOnce holds the guard: a product whose pool
// was never started stops cleanly and promptly, so a module whose Start failed
// (a missing table or root key) still stops without a stall or a spurious error.
func TestStoppingAnUnstartedPoolReturnsAtOnce(t *testing.T) {
	r := newRigSQLite(t)
	start := time.Now()
	if err := r.p.stopPool(); err != nil {
		t.Fatalf("stopping an unstarted pool returned %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed >= r.p.stopBound {
		t.Errorf("stopping an unstarted pool took %v, want well under the bound %v", elapsed, r.p.stopBound)
	}
}

// TestStopDrainsTheQueueWithinItsBound holds the shutdown: a message queued when
// Stop is called is sent while the bound lasts.
func TestStopDrainsTheQueueWithinItsBound(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	// A pool of one worker that is slow enough that the queue holds the next
	// message when Stop is called.
	block := make(chan struct{})
	first := make(chan struct{}, 1)
	r.sender.onSend = func(sms.Message) {
		select {
		case first <- struct{}{}:
			<-block
		default:
		}
	}
	r.startPool()

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	<-first
	// Move past the resend interval, then queue a second message that waits while
	// the worker is blocked.
	r.clock.advance(2 * time.Minute)
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the second Send: %v", err)
	}
	close(block)
	if err := r.p.stopPool(); err != nil {
		t.Fatalf("stopPool: %v", err)
	}
	if got := r.sender.count(); got != 2 {
		t.Errorf("the carrier received %d messages, want 2 (the queue drained on Stop)", got)
	}
}
