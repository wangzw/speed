package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/sms"
)

func (r *rig) deliverQueued(t *testing.T, result error) {
	t.Helper()
	var msg sms.Resolved
	select {
	case msg = <-r.p.pool.queue:
	default:
		t.Fatal("the send queued no message")
	}
	r.sender.setResult(result)
	r.p.deliverOne(context.Background(), msg)
}

func (r *rig) refundedAt(t *testing.T, id string) any {
	t.Helper()
	v, ok := r.column(id, "refunded_at")
	if !ok {
		t.Fatalf("the challenge %s is not in the table", id)
	}
	return v
}

// TestACertainCarrierFailureRefundsTheSend checks that a certain carrier failure refunds the send.
func TestACertainCarrierFailureRefundsTheSend(t *testing.T) {
	for _, result := range []error{sms.ErrRejected, sms.ErrUnroutable, sms.ErrThrottled} {
		t.Run(result.Error(), func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			r.liftBudgets()
			r.store.set(resendIntervalPath, 30*time.Second)
			r.store.set(perPhoneHourlyPath, 1)

			id, err := r.sendLogin()
			if err != nil {
				t.Fatalf("the first Send: %v", err)
			}
			var msg sms.Resolved
			select {
			case msg = <-r.p.pool.queue:
			default:
				t.Fatal("the send queued no message")
			}
			code := msg.Params["code"]
			r.sender.setResult(result)
			r.p.deliverOne(context.Background(), msg)

			if r.refundedAt(t, id) == nil {
				t.Errorf("the challenge carries no refund marker after a certain failure")
			}
			if v, _ := r.column(id, "consumed_at"); v == nil {
				t.Errorf("the challenge is still live after a certain failure: its code was never delivered")
			}
			if err := r.p.Verify(context.Background(), id, code); !errors.Is(err, authn.ErrInvalidCredentials) {
				t.Errorf("Verify with the undelivered code returned %v, want ErrInvalidCredentials", err)
			}

			r.clock.advance(31 * time.Second)
			if _, err := r.sendLogin(); err != nil {
				t.Fatalf("a send after a certain failure returned %v, want success: the failed send's budget was not given back", err)
			}
		})
	}
}

// TestAnUncertainCarrierFailureStaysCounted checks that an uncertain carrier failure stays counted.
func TestAnUncertainCarrierFailureStaysCounted(t *testing.T) {
	cases := []struct {
		name   string
		result error
	}{
		{name: "the uncertain sentinel", result: sms.ErrUncertain},
		{name: "an error outside the closed set", result: errors.New("carrier defect")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			r.liftBudgets()
			r.store.set(resendIntervalPath, 30*time.Second)
			r.store.set(perPhoneHourlyPath, 1)

			id, err := r.sendLogin()
			if err != nil {
				t.Fatalf("the first Send: %v", err)
			}
			r.deliverQueued(t, c.result)
			if r.refundedAt(t, id) != nil {
				t.Errorf("the challenge carries a refund marker after an uncertain outcome")
			}
			if v, _ := r.column(id, "consumed_at"); v != nil {
				t.Errorf("the challenge was spent after an uncertain outcome: it must stay live")
			}
			r.clock.advance(31 * time.Second)

			if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
				t.Fatalf("a send after an uncertain outcome returned %v, want ErrThrottled: an uncertain send must stay counted", err)
			}
		})
	}
}

// TestARegistryRefusalRefundsTheSend checks that a registry refusal refunds the send.
func TestARegistryRefusalRefundsTheSend(t *testing.T) {
	for _, refusal := range []error{sms.ErrRejected, sms.ErrNotSendable} {
		t.Run(refusal.Error(), func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			r.liftBudgets()
			r.store.set(resendIntervalPath, 30*time.Second)
			r.store.set(perPhoneHourlyPath, 1)
			r.registry.setResolveErr(refusal)

			id, err := r.sendLogin()
			if err != nil {
				t.Fatalf("Send with a refusing registry returned %v, want success", err)
			}
			if r.refundedAt(t, id) == nil {
				t.Errorf("the challenge carries no refund marker after the registry refused to build the message")
			}
			if v, _ := r.column(id, "consumed_at"); v == nil {
				t.Errorf("the challenge is still live after the registry refused to build the message")
			}

			r.clock.advance(31 * time.Second)
			if _, err := r.sendLogin(); err != nil {
				t.Fatalf("a send after the registry refused returned %v, want success: the refused send's budget was not given back", err)
			}
		})
	}
}

// TestADropAfterTheRowIsWrittenRefundsTheSend checks that a drop after the row is written refunds the send.
func TestADropAfterTheRowIsWrittenRefundsTheSend(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneHourlyPath, 1)

	entered := make(chan struct{})
	release := make(chan struct{})
	r.registry.setOnResolve(func() {
		close(entered)
		<-release
	})

	type sendResult struct {
		id  string
		err error
	}
	done := make(chan sendResult, 1)
	go func() {
		id, err := r.sendLogin()
		done <- sendResult{id: id, err: err}
	}()

	<-entered
	for len(r.p.pool.queue) < cap(r.p.pool.queue) {
		r.p.pool.queue <- sms.Resolved{}
	}
	close(release)

	got := <-done
	r.registry.setOnResolve(nil)
	if got.err != nil {
		t.Fatalf("Send returned %v, want success", got.err)
	}
	if r.refundedAt(t, got.id) == nil {
		t.Errorf("the challenge carries no refund marker after the message was dropped")
	}
	if v, _ := r.column(got.id, "consumed_at"); v == nil {
		t.Errorf("the challenge is still live after the message was dropped: its code was never delivered")
	}

	for len(r.p.pool.queue) > 0 {
		<-r.p.pool.queue
	}
	r.clock.advance(31 * time.Second)
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("a send after a dropped message returned %v, want success: the dropped send's budget was not given back", err)
	}
}

// TestARefundedRowCountsAgainstNoBudget checks that a refunded row counts against no budget.
func TestARefundedRowCountsAgainstNoBudget(t *testing.T) {
	cases := []struct {
		name  string
		tune  func(*rig)
		first func(*rig) (string, error)
		next  func(*rig) (string, error)
	}{
		{
			name: "phone",
			tune: func(r *rig) { r.store.set(perPhoneHourlyPath, 1) },
			first: func(r *rig) (string, error) {
				return r.sendLogin()
			},
			next: func(r *rig) (string, error) {
				return r.sendLogin()
			},
		},
		{
			name: "source",
			tune: func(r *rig) { r.store.set(perSourceHourlyPath, 1) },
			first: func(r *rig) (string, error) {
				return r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeLogin, Phone: phone, Source: "203.0.113.7", Deliver: true,
				})
			},
			next: func(r *rig) (string, error) {
				return r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Source: "203.0.113.7", Deliver: true,
				})
			},
		},
		{
			name: "account",
			tune: func(r *rig) { r.store.set(perAccountHourlyPath, 1) },
			first: func(r *rig) (string, error) {
				return r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeMFA, Phone: phone, Account: acct, Deliver: true,
				})
			},
			next: func(r *rig) (string, error) {
				return r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Deliver: true,
				})
			},
		},
		{
			name: "global",
			tune: func(r *rig) { r.store.set(globalLoginPath, 1) },
			first: func(r *rig) (string, error) {
				return r.sendLogin()
			},
			next: func(r *rig) (string, error) {
				return r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Deliver: true,
				})
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			r.liftBudgets()
			r.store.set(resendIntervalPath, 30*time.Second)
			c.tune(r)

			if _, err := c.first(r); err != nil {
				t.Fatalf("the first Send: %v", err)
			}
			r.deliverQueued(t, sms.ErrRejected)
			r.clock.advance(31 * time.Second)

			if _, err := c.next(r); err != nil {
				t.Fatalf("a send sharing only the refunded row's %s dimension returned %v, want success", c.name, err)
			}
		})
	}
}

// TestARefundIsWrittenAndCountedOnEveryDialect checks that a refund is written and counted on every dialect.
func TestARefundIsWrittenAndCountedOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, database db.Database, _ string) {
		r := newRig(t, database, newSender())
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perPhoneHourlyPath, 1)

		id, err := r.sendLogin()
		if err != nil {
			t.Fatalf("the first Send: %v", err)
		}
		r.deliverQueued(t, sms.ErrRejected)

		if r.refundedAt(t, id) == nil {
			t.Errorf("the challenge carries no refund marker")
		}
		if v, _ := r.column(id, "consumed_at"); v == nil {
			t.Errorf("the challenge is still live after the refund: its code was never delivered")
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a send after the refund returned %v, want success", err)
		}
	})
}

// TestASuppressedRowKeepsItsCountingRule checks that a suppressed row keeps its counting rule.
func TestASuppressedRowKeepsItsCountingRule(t *testing.T) {
	t.Run("phone", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perPhoneHourlyPath, 1)

		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false,
		}); err != nil {
			t.Fatalf("the suppressed send returned %v, want success", err)
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a send after a suppressed one returned %v, want ErrThrottled: the number budget still counts suppressed rows", err)
		}
	})

	t.Run("global", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(globalLoginPath, 1)

		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: false,
		}); err != nil {
			t.Fatalf("the suppressed send returned %v, want success", err)
		}
		r.clock.advance(31 * time.Second)
		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: "+8613800138001", Deliver: true,
		}); err != nil {
			t.Fatalf("a real send after a suppressed one returned %v, want success: the global budget does not count suppressed rows", err)
		}
	})
}
