package sms

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms"
)

// The setting keys the new budget dimensions are read under. They are written as
// paths here rather than through the package constants so that the behaviour
// cases can be compiled against the module before the settings are declared;
// after the declaration the two spellings must agree, which
// TestTheNewBudgetKeysMatchTheReaderPaths holds.
const (
	perSourceAttemptHourlyPathLiteral  = "authn.sms.per-source-attempt-hourly"
	perSourceDistinctHourlyPathLiteral = "authn.sms.per-source-distinct-hourly"
	globalAttemptLoginPathLiteral      = "authn.sms.global-attempt-hourly-login"
	globalAttemptMFAPathLiteral        = "authn.sms.global-attempt-hourly-mfa"
	globalAttemptEnrollPathLiteral     = "authn.sms.global-attempt-hourly-enroll"
	globalAttemptResetPathLiteral      = "authn.sms.global-attempt-hourly-reset"
)

// TestTheRetunedSMSBudgetDefaultsAreWhatSettingsReport holds the values the
// redesign moves the existing budgets to. Every one is read through the reader,
// so a default that a settings store cannot serve fails the case.
func TestTheRetunedSMSBudgetDefaultsAreWhatSettingsReport(t *testing.T) {
	r := newReader(nil, (&logBuffer{}).logger())
	if got := r.resendInterval(); got != 30*time.Second {
		t.Errorf("resend-interval = %v, want 30s", got)
	}
	if defaults.PerFlowSends != 5 {
		t.Errorf("per-flow-sends default = %d, want 5", defaults.PerFlowSends)
	}
	if got := r.perPhoneHourly(); got != 10 {
		t.Errorf("per-phone-hourly = %d, want 10", got)
	}
	if got := r.perPhoneDaily(); got != 25 {
		t.Errorf("per-phone-daily = %d, want 25", got)
	}
	if got := r.perSourceHourly(); got != 300 {
		t.Errorf("per-source-hourly = %d, want 300", got)
	}
	if got := r.perSourceDaily(); got != 1000 {
		t.Errorf("per-source-daily = %d, want 1000", got)
	}
	if got := r.perAccountHourly(); got != 20 {
		t.Errorf("per-account-hourly = %d, want 20", got)
	}
	for purpose, want := range map[string]int{
		authn.SMSPurposeLogin:  500,
		authn.SMSPurposeMFA:    300,
		authn.SMSPurposeEnroll: 100,
		authn.SMSPurposeReset:  200,
	} {
		if got := r.globalHourly(purpose); got != want {
			t.Errorf("global-hourly-%s = %d, want %d", purpose, got, want)
		}
	}
}

// TestTheAttemptBudgetRefusesAfterFailedRefundedSends holds the attempt
// dimension: every send that reaches the carrier and is definitively refused
// refunds its send budget, so the send budgets never fill; the attempt budget
// counts the failed refunded rows and refuses the next attempt. The source's raw
// send budget and its distinct-destination budget are lifted, so the attempt
// dimension is the only one that can refuse.
func TestTheAttemptBudgetRefusesAfterFailedRefundedSends(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perSourceAttemptHourlyPathLiteral, 2)

	const source = "203.0.113.30"
	for i := range 2 {
		if _, err := r.sendFrom(source, fmt.Sprintf("+86138006%05d", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		// The carrier definitively refuses the send, so the send budgets are
		// given back and only the attempt stays counted.
		r.deliverQueued(t, sms.ErrRejected)
		r.clock.advance(31 * time.Second)
	}
	_, err := r.sendFrom(source, "+8613800699999")
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the send after the attempt budget returned %v, want ErrThrottled", err)
	}
	// The sends really were refunded: a source that has spent no attempt budget
	// still sends.
	if _, err := r.sendFrom("203.0.113.31", "+8613800699998"); err != nil {
		t.Fatalf("a send from another source was refused: %v", err)
	}
}

// TestASuccessfulSendDoesNotSpendTheAttemptBudget holds the other side of the
// attempt dimension: it counts the sends a failure refunded, not every send. A
// source whose sends go out stays below its attempt limit however many it makes,
// while a send the carrier refuses and refunds spends it.
func TestASuccessfulSendDoesNotSpendTheAttemptBudget(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perSourceAttemptHourlyPathLiteral, 1)

	const source = "203.0.113.50"
	for i := range 3 {
		if _, err := r.sendFrom(source, fmt.Sprintf("+86138009%05d", i)); err != nil {
			t.Fatalf("successful send %d returned %v, want success", i, err)
		}
		r.deliverQueued(t, nil)
		r.clock.advance(31 * time.Second)
	}
	// A send the carrier refuses is admitted, then refunded, and it is the one
	// that spends the attempt budget.
	if _, err := r.sendFrom(source, "+8613800999990"); err != nil {
		t.Fatalf("the failing send was refused before it spent the attempt budget: %v", err)
	}
	r.deliverQueued(t, sms.ErrRejected)
	r.clock.advance(31 * time.Second)
	if _, err := r.sendFrom(source, "+8613800999991"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the send after a failed and refunded one returned %v, want ErrThrottled (the attempt budget)", err)
	}
}

// TestTheGlobalAttemptBudgetCountsOnlyFailedSends holds the same rule on the
// deployment-wide attempt dimension: a purpose's successful sends do not spend
// its attempt budget, a failed refunded send does, and another purpose keeps its
// own budget.
func TestTheGlobalAttemptBudgetCountsOnlyFailedSends(t *testing.T) {
	r := newResetRig(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalAttemptLoginPathLiteral, 1)

	for i := range 2 {
		if _, err := r.loginTo(fmt.Sprintf("+86138010%05d", i)); err != nil {
			t.Fatalf("successful login send %d returned %v, want success", i, err)
		}
		r.deliverQueued(t, nil)
		r.clock.advance(31 * time.Second)
	}
	// The failing login send is admitted and refunded; it fills the budget.
	if _, err := r.loginTo("+8613801099990"); err != nil {
		t.Fatalf("the failing login send was refused before it spent the attempt budget: %v", err)
	}
	r.deliverQueued(t, sms.ErrRejected)
	r.clock.advance(31 * time.Second)
	if _, err := r.loginTo("+8613801099991"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the login send after a failed and refunded one returned %v, want ErrThrottled", err)
	}
	// The reset purpose's attempt budget is its own.
	if _, err := r.resetTo("+8613801088888"); err != nil {
		t.Fatalf("a reset send was refused by the login attempt budget: %v", err)
	}
}

// TestTheDistinctDestinationBudgetRefusesAChangingSource holds the
// distinct-destination dimension: a source that keeps sending to a new number
// reaches the distinct limit long before its raw send budget, and the next new
// number is refused. Another source has its own distinct window.
func TestTheDistinctDestinationBudgetRefusesAChangingSource(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perSourceDistinctHourlyPathLiteral, 2)

	const source = "203.0.113.40"
	for i := range 2 {
		if _, err := r.sendFrom(source, fmt.Sprintf("+86138007%05d", i)); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		r.clock.advance(31 * time.Second)
	}
	// The source's raw hourly budget is far from its limit, so the distinct
	// dimension is what refuses the third number.
	if _, err := r.sendFrom(source, "+8613800799999"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a third number from one source returned %v, want ErrThrottled (the distinct budget)", err)
	}
	if _, err := r.sendFrom("203.0.113.41", "+8613800788888"); err != nil {
		t.Fatalf("a send from another source was refused: %v", err)
	}
}

// TestTheGlobalAttemptBudgetIsPerPurpose holds the deployment-wide attempt
// dimension: a purpose's attempts that all failed and were refunded still fill
// its attempt budget, while another purpose keeps its own.
func TestTheGlobalAttemptBudgetIsPerPurpose(t *testing.T) {
	r := newResetRig(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(globalAttemptLoginPathLiteral, 2)

	for i := range 2 {
		if _, err := r.loginTo(fmt.Sprintf("+86138008%05d", i)); err != nil {
			t.Fatalf("login send %d: %v", i, err)
		}
		r.deliverQueued(t, sms.ErrRejected)
		r.clock.advance(31 * time.Second)
	}
	if _, err := r.loginTo("+8613800899999"); !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("the login send after the attempt budget returned %v, want ErrThrottled", err)
	}
	// The reset purpose's attempt budget is its own.
	if _, err := r.resetTo("+8613800888888"); err != nil {
		t.Fatalf("a reset send was refused by the login attempt budget: %v", err)
	}
}
