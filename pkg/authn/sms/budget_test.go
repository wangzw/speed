package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vislake/speed/pkg/authn"
)

func (r *rig) liftBudgets() {
	r.store.set(perPhoneHourlyPath, 1000)
	r.store.set(perPhoneDailyPath, 1000)
	r.store.set(perSourceHourlyPath, 1000)
	r.store.set(perSourceDailyPath, 1000)
	r.store.set(perSourceDistinctHourlyPath, 1000)
	r.store.set(perSourceDistinctDailyPath, 1000)
	r.store.set(perSourceAttemptHourlyPath, 100000)
	r.store.set(globalLoginPath, 1000000)
	r.store.set(globalMFAPath, 1000000)
	r.store.set(globalEnrollPath, 1000000)
	r.store.set(globalResetPath, 1000000)
	r.store.set(globalAttemptLoginPath, 1000000)
	r.store.set(globalAttemptMFAPath, 1000000)
	r.store.set(globalAttemptEnrollPath, 1000000)
	r.store.set(globalAttemptResetPath, 1000000)
	r.store.set(perAccountHourlyPath, 1000)
	r.store.set(ceilingHourlyPath, 1000000)
}

func (r *rig) loginTo(number string) (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: number, Deliver: true,
	})
}

// TestTheResendIntervalThrottlesWithARetryAfter checks that the resend interval throttles with a Retry-After.
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

// TestThePerPhoneHourlyBudgetThrottles checks that the per-phone hourly budget throttles.
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

// TestThePerPhoneDailyBudgetThrottles checks that the per-phone daily budget throttles.
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

// TestTheGlobalBudgetIsPerPurposeAndThrottles checks that the global budget is per purpose and throttles.
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
	if want := 59*time.Minute + 29*time.Second; throttled.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", throttled.RetryAfter, want)
	}
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138001", Account: acct, Deliver: true,
	}); err != nil {
		t.Fatalf("an mfa send was refused by the login budget: %v", err)
	}
}

// TestTheGlobalBudgetCountsOnlyRealSends checks that the global budget counts only real sends.
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

	if _, err := r.loginTo("+8613800138012"); err != nil {
		t.Fatalf("the first real send was refused: %v", err)
	}
	r.clock.advance(31 * time.Second)
	_, err := r.loginTo("+8613800138013")
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second real send returned %v, want ErrThrottled (the global budget)", err)
	}
}

// TestASuppressedSendStillSpendsThePhoneBudget checks that a suppressed send still spends the phone budget.
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

// TestTheAccountBudgetCountsOnlyMFAAndEnroll checks that the account budget counts only mfa and enroll.
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
	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeMFA, Phone: "+8613800138002", Account: acct, Deliver: true,
	})
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the second mfa send returned %v, want ErrThrottled (the account budget)", err)
	}
	if _, err := r.loginTo("+8613800138003"); err != nil {
		t.Fatalf("a login send was refused by the account budget: %v", err)
	}
}
