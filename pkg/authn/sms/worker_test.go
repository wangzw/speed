package sms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/sms"
)

// TestTheWorkerRecordsEachCarrierOutcome checks that the worker records each carrier outcome.
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
		{"unroutable", sms.ErrUnroutable, outcomeUnroutable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.sender.setResult(c.result)
			r.p.deliverOne(context.Background(), sms.Resolved{To: phone, Params: map[string]string{"code": "123456"}})
			if got := r.counter(sendsName, attribute.String(outcomeAttribute, c.outcome)); got != 1 {
				t.Errorf("outcome %s = %d, want 1", c.outcome, got)
			}
		})
	}
}

// TestEachCarrierCallWritesOneLineWithoutSecrets checks that each carrier call writes one line without secrets.
func TestEachCarrierCallWritesOneLineWithoutSecrets(t *testing.T) {
	const code = "654321"
	cases := []struct {
		name    string
		result  error
		level   string
		outcome string
	}{
		{"sent", nil, "level=INFO", outcomeSent},
		{"rejected", sms.ErrRejected, "level=WARN", outcomeRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.sender.setResult(c.result)
			r.p.deliverOne(context.Background(), sms.Resolved{To: phone, Params: map[string]string{"code": code}})

			lines := r.logs.lines("outcome=" + c.outcome)
			if len(lines) != 1 {
				t.Fatalf("the carrier call wrote %d lines with outcome %s, want 1:\n%s",
					len(lines), c.outcome, r.logs.String())
			}
			line := lines[0]
			for _, want := range []string{c.level, "duration_ms=", "provider_id=provider-1"} {
				if !strings.Contains(line, want) {
					t.Errorf("the line omits %q: %s", want, line)
				}
			}
			if c.result != nil && !strings.Contains(line, "cause=") {
				t.Errorf("the failure line omits the cause: %s", line)
			}
			if strings.Contains(line, phone) || strings.Contains(line, code) {
				t.Errorf("the line carries the number or the code: %s", line)
			}
		})
	}
}

// TestACarrierErrorOutsideTheClosedSetIsUncertain checks that a carrier error outside the closed set is uncertain.
func TestACarrierErrorOutsideTheClosedSetIsUncertain(t *testing.T) {
	r := newRigSQLite(t)
	r.sender.setResult(errors.New("a carrier defect"))
	r.p.deliverOne(context.Background(), sms.Resolved{To: phone})
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeUncertain)); got != 1 {
		t.Errorf("an unrecognised carrier error recorded outcome uncertain %d times, want 1", got)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeSent)); got != 0 {
		t.Errorf("an unrecognised carrier error recorded sent %d times, want 0", got)
	}
}

// TestEveryOutcomeIsRecorded checks that every outcome is recorded.
func TestEveryOutcomeIsRecorded(t *testing.T) {
	r := newRigSQLite(t)
	ctx := context.Background()

	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrUnsupportedRegion) {
		t.Fatalf("the empty-table send returned %v", err)
	}
	r.allow()

	if _, err := r.p.Send(ctx, authn.SendCode{Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false}); err != nil {
		t.Fatalf("the suppressed send returned %v", err)
	}

	for range cap(r.p.pool.queue) {
		r.p.pool.queue <- sms.Resolved{To: phone}
	}
	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("the full-queue send returned %v", err)
	}
	for len(r.p.pool.queue) > 0 {
		<-r.p.pool.queue
	}
	r.clock.advance(2 * time.Hour)

	r.registry.setResolveErr(sms.ErrNotSendable)
	if _, err := r.loginTo("+8613800138012"); err != nil {
		t.Fatalf("a not-sendable send returned %v", err)
	}
	r.registry.setResolveErr(nil)
	r.clock.advance(2 * time.Hour)

	r.store.set(globalLoginPath, 1)
	if _, err := r.loginTo("+8613800138009"); err != nil {
		t.Fatalf("the first budgeted send returned %v", err)
	}
	if _, err := r.loginTo("+8613800138010"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the budget-exhausted send returned %v", err)
	}

	for _, res := range []error{nil, sms.ErrUncertain, sms.ErrThrottled, sms.ErrRejected, sms.ErrUnroutable} {
		r.sender.setResult(res)
		r.p.deliverOne(ctx, sms.Resolved{To: phone})
	}

	for _, outcome := range []string{
		outcomeSent, outcomeUncertain, outcomeThrottled, outcomeRejected, outcomeUnroutable,
		outcomeNotSendable, outcomeUnsupportedRegion, outcomeBudgetExhausted, outcomeSuppressed,
		outcomeDropped,
	} {
		if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcome)); got < 1 {
			t.Errorf("outcome %s was never recorded", outcome)
		}
	}
}

// TestTheWorkersDrainQueuedMessagesOffTheRequestPath checks that workers drain queued messages off the request path.
func TestTheWorkersDrainQueuedMessagesOffTheRequestPath(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	delivered := make(chan struct{}, 1)
	r.sender.onSend = func(sms.Resolved) {
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
	case <-time.After(coretest.Scale(5 * time.Second)):
		t.Fatal("the worker never called the carrier")
	}
}

// TestStoppingAnUnstartedPoolReturnsAtOnce checks that stopping an unstarted pool returns at once.
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

// TestStopDrainsTheQueueWithinItsBound checks that Stop drains the queue within its bound.
func TestStopDrainsTheQueueWithinItsBound(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	block := make(chan struct{})
	first := make(chan struct{}, 1)
	r.sender.onSend = func(sms.Resolved) {
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
