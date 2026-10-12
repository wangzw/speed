package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/authn"
)

// liftBudgets sets every budget that is not the one under test high enough that
// it cannot be the refusal.
func (r *rig) liftBudgets() {
	r.store.set(perPhoneHourlyPath, 1000)
	r.store.set(perPhoneDailyPath, 1000)
	r.store.set(globalLoginPath, 1000000)
	r.store.set(globalMFAPath, 1000000)
	r.store.set(globalEnrollPath, 1000000)
	r.store.set(perAccountHourlyPath, 1000)
}

// loginTo sends a delivered login code for number.
func (r *rig) loginTo(number string) (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: number, Deliver: true,
	})
}

// TestTheResendIntervalThrottlesWithARetryAfter holds the first budget: a second
// send to the same number within the resend interval is ErrThrottled carrying the
// time left, and the same send after the interval goes through.
func TestTheResendIntervalThrottlesWithARetryAfter(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.store.set(resendIntervalPath, 2*time.Minute)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first Send: %v", err)
	}
	r.clock.advance(30 * time.Second)
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a resend within the interval returned %v, want ErrThrottled", err)
	}
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("the throttled error is %T, want *ThrottledError", err)
	}
	if want := 90 * time.Second; throttled.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", throttled.RetryAfter, want)
	}
	if got := r.counter(sendsName, attribute.String(outcomeAttribute, outcomeBudgetExhausted)); got != 1 {
		t.Errorf("outcome budget-exhausted = %d, want 1", got)
	}

	r.clock.advance(90 * time.Second)
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("a send after the interval returned %v, want success", err)
	}
}

// TestThePerPhoneHourlyBudgetThrottles holds the number dimension: the number's
// sends within an hour reach the limit and the next is refused, whatever its
// purpose.
func TestThePerPhoneHourlyBudgetThrottles(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneHourlyPath, 2)

	for i := range 2 {
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		r.clock.advance(31 * time.Second)
	}
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the third send within the hour returned %v, want ErrThrottled", err)
	}
}

// TestThePerPhoneDailyBudgetThrottles holds the daily dimension on its own: with
// the hourly budget lifted, the daily count is what refuses.
func TestThePerPhoneDailyBudgetThrottles(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneDailyPath, 2)

	for i := range 2 {
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		r.clock.advance(31 * time.Second)
	}
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the third send within the day returned %v, want ErrThrottled", err)
	}
}

// TestTheGlobalBudgetIsPerPurposeAndAnswersUnavailable holds the third dimension:
// the global budget of the login purpose spent refuses further login sends with
// ErrUnavailable, while the mfa purpose, with its own budget, still sends.
func TestTheGlobalBudgetIsPerPurposeAndAnswersUnavailable(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalLoginPath, 1)

	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("the first login send: %v", err)
	}
	r.clock.advance(31 * time.Second)
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrUnavailable) {
		t.Fatalf("the second login send returned %v, want ErrUnavailable (the global budget)", err)
	}
	// The mfa purpose has its own budget and is not touched by the login one.
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Deliver: true,
	}); err != nil {
		t.Fatalf("an mfa send was refused by the login budget: %v", err)
	}
}

// TestTheAccountBudgetCountsOnlyMFAAndEnroll holds the account dimension: two
// numbers of one account reach the account limit for the mfa purpose, while a
// login send of the same account is not counted by it.
func TestTheAccountBudgetCountsOnlyMFAAndEnroll(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perAccountHourlyPath, 1)

	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Deliver: true,
	}); err != nil {
		t.Fatalf("the first mfa send: %v", err)
	}
	// A different number of the same account is still the same account.
	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138002", Account: acct, Deliver: true,
	})
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second mfa send returned %v, want ErrThrottled (the account budget)", err)
	}
	// A login send has no account and is not counted by the account budget.
	if _, err := r.loginTo("+8613800138003"); err != nil {
		t.Fatalf("a login send was refused by the account budget: %v", err)
	}
}
