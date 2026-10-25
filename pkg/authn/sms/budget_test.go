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
	r.store.set(perSourceHourlyPath, 1000)
	r.store.set(perSourceDailyPath, 1000)
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

// TestTheGlobalBudgetIsPerPurposeAndThrottles holds the third dimension: the
// global budget of the login purpose spent refuses further login sends with a
// throttled error whose Retry-After is close to the window, while the mfa
// purpose, with its own budget, still sends.
func TestTheGlobalBudgetIsPerPurposeAndThrottles(t *testing.T) {
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
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second login send returned %v, want ErrThrottled (the global budget)", err)
	}
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("the throttled error is %T, want *ThrottledError", err)
	}
	// The oldest real row is 31s into the hour, so the window frees in 59m29s.
	if want := 59*time.Minute + 29*time.Second; throttled.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", throttled.RetryAfter, want)
	}
	// The mfa purpose has its own budget and is not touched by the login one.
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Deliver: true,
	}); err != nil {
		t.Fatalf("an mfa send was refused by the login budget: %v", err)
	}
}

// TestTheGlobalBudgetCountsOnlyRealSends holds the asymmetry between the global
// dimension and the rest: a suppressed row spends the number, account and source
// budgets but not the deployment-wide one, so anonymous requests for numbers
// nobody registered cannot exhaust the deployment. The number, account and
// source dimensions still count suppressed rows, which is what keeps the point
// of refusal from telling a registered number from an unregistered one.
func TestTheGlobalBudgetCountsOnlyRealSends(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalLoginPath, 1)

	for _, number := range []string{"+8613800138010", "+8613800138011"} {
		if _, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: number, Deliver: false,
		}); err != nil {
			t.Fatalf("a suppressed send to %s returned %v, want success", number, err)
		}
		r.clock.advance(31 * time.Second)
	}

	// The first real send is the only row the global budget counts, so it is
	// admitted and fills the limit.
	if _, err := r.loginTo("+8613800138012"); err != nil {
		t.Fatalf("the first real send was refused: %v", err)
	}
	r.clock.advance(31 * time.Second)
	_, err := r.loginTo("+8613800138013")
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second real send returned %v, want ErrThrottled (the global budget)", err)
	}
}

// TestASuppressedSendStillSpendsThePhoneBudget holds the other half of the
// asymmetry: a suppressed row still counts against the number budget, so a
// caller meets the same refusal at the same count whether the number is
// registered or not.
//
// It is a pairing guard, not a reproduction: the number dimension counted
// suppressed rows before the global budget began counting only real sends, so
// this case passed then too. It guards against over-correcting that change. The
// reproductions are TestTheGlobalBudgetIsPerPurposeAndThrottles and
// TestTheGlobalBudgetCountsOnlyRealSends.
func TestASuppressedSendStillSpendsThePhoneBudget(t *testing.T) {
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
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a send after a suppressed one returned %v, want ErrThrottled (the number budget)", err)
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
