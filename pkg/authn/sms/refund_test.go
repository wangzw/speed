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

// deliverQueued hands the message the last send queued to the worker's delivery
// path, with the carrier answering result. The pool is not started, so the case
// decides when the worker runs and the challenge row is settled when this
// returns.
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

// refundedAt reports the refund marker of one row, and fails the case when the
// row is not there. A nil answer is a row that was not refunded, on both
// engines.
func (r *rig) refundedAt(t *testing.T, id string) any {
	t.Helper()
	v, ok := r.column(id, "refunded_at")
	if !ok {
		t.Fatalf("the challenge %s is not in the table", id)
	}
	return v
}

// TestACertainCarrierFailureRefundsTheSend holds the refund: a send the carrier
// certainly did not put into its pipeline — a rejection, a throttle or an
// unroutable destination — gives its budgets back, so the next send inside the
// same window is admitted where the spent row would have refused it. The code
// is invalidated in the same statement, so a code that was never delivered
// cannot pass Verify.
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

// TestAnUncertainCarrierFailureStaysCounted holds the other side: an outcome
// that leaves it open whether the message went out is read as sent, so its row
// keeps spending the budget and the next send in the window is refused. This is
// a guard rather than a reproduction: it passed before the refund existed, and
// it is what keeps the refund from silently widening to the uncertain class.
func TestAnUncertainCarrierFailureStaysCounted(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneHourlyPath, 1)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	r.deliverQueued(t, sms.ErrUncertain)
	r.clock.advance(31 * time.Second)

	if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a send after an uncertain outcome returned %v, want ErrThrottled: an uncertain send must stay counted", err)
	}
}

// TestARefundedRowCountsAgainstNoBudget holds that the refund reaches every
// dimension: a certain failure gives back the number, source, account and
// deployment-wide budgets alike, so a later send that shares only the refunded
// row's dimension is admitted where that row would have refused it.
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

// TestARefundIsWrittenAndCountedOnEveryDialect holds the refund statement and
// the budget predicate on both engines: the marker is written, the row is
// spent, and the phone budget stops counting it.
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
		r.clock.advance(31 * time.Second)
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a send after the refund returned %v, want success", err)
		}
	})
}

// TestASuppressedRowKeepsItsCountingRule holds the asymmetry the refund must
// not disturb: a suppressed row still spends the number budget and still does
// not spend the deployment-wide one. It is a guard, not a reproduction: the rule
// predates the refund and these counts must read the same after it.
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
