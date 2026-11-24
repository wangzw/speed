package sms

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/sms/budget"
)

const otherPhone = "+8613800199999"

func (r *rig) recordLedger(a budget.Attempt) budget.ID {
	r.t.Helper()
	if a.Channel == "" {
		a.Channel = budget.ChannelAuthn
	}
	if a.Purpose == "" {
		a.Purpose = authn.SMSPurposeLogin
	}
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = r.clock.read()
	}
	if a.Outcome == "" {
		a.Outcome = budget.OutcomeSent
	}
	id, err := r.ledger.Record(context.Background(), a)
	if err != nil {
		r.t.Fatalf("recording a ledger attempt: %v", err)
	}
	return id
}

func (r *rig) recordRefundedLedger(a budget.Attempt) budget.ID {
	r.t.Helper()
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = r.clock.read()
	}
	refunded := a.AttemptedAt
	a.RefundedAt = &refunded
	a.Outcome = budget.OutcomeRejected
	return r.recordLedger(a)
}

func (r *rig) recordSuppressedLedger(a budget.Attempt) budget.ID {
	r.t.Helper()
	if a.AttemptedAt.IsZero() {
		a.AttemptedAt = r.clock.read()
	}
	refunded := a.AttemptedAt
	a.RefundedAt = &refunded
	a.Suppressed = true
	a.Outcome = budget.OutcomePending
	return r.recordLedger(a)
}

// TestEveryAuthnBudgetReadsTheSharedLedger checks that every authentication budget reads the shared ledger.
func TestEveryAuthnBudgetReadsTheSharedLedger(t *testing.T) {
	const source = "203.0.113.90"
	earlier := func(r *rig) time.Time { return r.clock.read().Add(-31 * time.Second) }
	fromSource := func(r *rig) error {
		_, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Source: source, Deliver: true,
		})
		return err
	}
	login := func(r *rig) error {
		_, err := r.sendLogin()
		return err
	}
	mfa := func(r *rig) error {
		_, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeMFA, Phone: phone, Account: acct, Deliver: true,
		})
		return err
	}

	cases := []struct {
		name  string
		tune  func(*rig)
		write func(*rig)
		send  func(*rig) error
	}{
		{
			name: "phone-hourly",
			tune: func(r *rig) { r.store.set(perPhoneHourlyPath, 1) },
			write: func(r *rig) {
				r.recordLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(phone),
					AttemptedAt:    earlier(r),
				})
			},
			send: login,
		},
		{
			name: "phone-daily",
			tune: func(r *rig) { r.store.set(perPhoneDailyPath, 1) },
			write: func(r *rig) {
				r.recordLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(phone),
					AttemptedAt:    earlier(r),
				})
			},
			send: login,
		},
		{
			name: "source-hourly",
			tune: func(r *rig) { r.store.set(perSourceHourlyPath, 1) },
			write: func(r *rig) {
				mac := r.database.BlindIndex(source)
				r.recordLedger(budget.Attempt{
					SourceMAC:      &mac,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: fromSource,
		},
		{
			name: "source-daily",
			tune: func(r *rig) { r.store.set(perSourceDailyPath, 1) },
			write: func(r *rig) {
				mac := r.database.BlindIndex(source)
				r.recordLedger(budget.Attempt{
					SourceMAC:      &mac,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: fromSource,
		},
		{
			name: "source-distinct-hourly",
			tune: func(r *rig) { r.store.set(perSourceDistinctHourlyPath, 1) },
			write: func(r *rig) {
				mac := r.database.BlindIndex(source)
				r.recordLedger(budget.Attempt{
					SourceMAC:      &mac,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: fromSource,
		},
		{
			name: "source-distinct-daily",
			tune: func(r *rig) { r.store.set(perSourceDistinctDailyPath, 1) },
			write: func(r *rig) {
				mac := r.database.BlindIndex(source)
				r.recordLedger(budget.Attempt{
					SourceMAC:      &mac,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: fromSource,
		},
		{
			name: "source-attempt-hourly",
			tune: func(r *rig) { r.store.set(perSourceAttemptHourlyPath, 1) },
			write: func(r *rig) {
				mac := r.database.BlindIndex(source)
				r.recordRefundedLedger(budget.Attempt{
					SourceMAC:      &mac,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: fromSource,
		},
		{
			name: "global-hourly",
			tune: func(r *rig) { r.store.set(globalLoginPath, 1) },
			write: func(r *rig) {
				r.recordLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: login,
		},
		{
			name: "global-attempt-hourly",
			tune: func(r *rig) { r.store.set(globalAttemptLoginPath, 1) },
			write: func(r *rig) {
				r.recordRefundedLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: login,
		},
		{
			name: "account-hourly",
			tune: func(r *rig) { r.store.set(perAccountHourlyPath, 1) },
			write: func(r *rig) {
				account := string(acct)
				r.recordLedger(budget.Attempt{
					Purpose:        authn.SMSPurposeMFA,
					AccountID:      &account,
					DestinationMAC: r.database.BlindIndex(otherPhone),
					AttemptedAt:    earlier(r),
				})
			},
			send: mfa,
		},
		{
			name: "resend-interval",
			write: func(r *rig) {
				r.recordLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(phone),
					AttemptedAt:    r.clock.read().Add(-time.Second),
				})
			},
			send: login,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRigSQLite(t)
			r.allow()
			r.liftBudgets()
			r.store.set(resendIntervalPath, 30*time.Second)
			if c.tune != nil {
				c.tune(r)
			}
			c.write(r)
			if err := c.send(r); !errors.Is(err, authn.ErrThrottled) {
				t.Fatalf("the send over a ledger-only %s row returned %v, want ErrThrottled", c.name, err)
			}
		})
	}
}

// TestASuppressedLedgerRowSpendsOnlyTheAntiEnumerationDimensions checks that a suppressed ledger row spends only the anti-enumeration dimensions.
func TestASuppressedLedgerRowSpendsOnlyTheAntiEnumerationDimensions(t *testing.T) {
	const source = "203.0.113.91"
	sourceMAC := func(r *rig) *string {
		mac := r.database.BlindIndex(source)
		return &mac
	}
	suppressed := func(r *rig) budget.Attempt {
		return budget.Attempt{
			SourceMAC:      sourceMAC(r),
			DestinationMAC: r.database.BlindIndex(otherPhone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		}
	}
	sendFromSource := func(r *rig) error {
		_, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Source: source, Deliver: true,
		})
		return err
	}

	t.Run("the phone dimension counts it", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perPhoneHourlyPath, 1)
		r.recordSuppressedLedger(budget.Attempt{
			DestinationMAC: r.database.BlindIndex(phone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
		if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a send after a suppressed attempt returned %v, want ErrThrottled", err)
		}
	})

	t.Run("the source dimension counts it", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perSourceHourlyPath, 1)
		r.recordSuppressedLedger(suppressed(r))
		if err := sendFromSource(r); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a send from a source with a suppressed attempt returned %v, want ErrThrottled", err)
		}
	})

	t.Run("the global send budget leaves it out", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(globalLoginPath, 1)
		r.recordSuppressedLedger(suppressed(r))
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a real send after a suppressed attempt returned %v, want success: the global budget counted it", err)
		}
	})

	t.Run("the ceiling leaves it out", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(ceilingHourlyPath, 1)
		r.recordSuppressedLedger(suppressed(r))
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a real send after a suppressed attempt returned %v, want success: the ceiling counted it", err)
		}
	})

	t.Run("the attempt budget leaves it out", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perSourceAttemptHourlyPath, 1)
		r.recordSuppressedLedger(suppressed(r))
		if err := sendFromSource(r); err != nil {
			t.Fatalf("a send after a suppressed attempt returned %v, want success: the attempt budget counted it", err)
		}
	})
}

// TestTheAttemptBudgetCountsOnlyRefundedRows checks that the attempt budget counts only refunded rows.
func TestTheAttemptBudgetCountsOnlyRefundedRows(t *testing.T) {
	const source = "203.0.113.92"
	sourceMAC := func(r *rig) *string {
		mac := r.database.BlindIndex(source)
		return &mac
	}
	sendFromSource := func(r *rig) error {
		_, err := r.p.Send(context.Background(), authn.SendCode{
			Purpose: authn.SMSPurposeLogin, Phone: phone, Source: source, Deliver: true,
		})
		return err
	}

	t.Run("the source attempt budget ignores a successful row", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perSourceAttemptHourlyPath, 1)
		r.recordLedger(budget.Attempt{
			SourceMAC:      sourceMAC(r),
			DestinationMAC: r.database.BlindIndex(otherPhone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
		if err := sendFromSource(r); err != nil {
			t.Fatalf("a send after a successful attempt returned %v, want success: the attempt budget counted it", err)
		}
	})

	t.Run("the source attempt budget counts a refunded row", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(perSourceAttemptHourlyPath, 1)
		r.recordRefundedLedger(budget.Attempt{
			SourceMAC:      sourceMAC(r),
			DestinationMAC: r.database.BlindIndex(otherPhone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
		if err := sendFromSource(r); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a send after a refunded attempt returned %v, want ErrThrottled", err)
		}
	})

	t.Run("the global attempt budget ignores a successful row", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(globalAttemptLoginPath, 1)
		r.recordLedger(budget.Attempt{
			DestinationMAC: r.database.BlindIndex(otherPhone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
		if _, err := r.sendLogin(); err != nil {
			t.Fatalf("a send after a successful attempt returned %v, want success: the attempt budget counted it", err)
		}
	})

	t.Run("the global attempt budget counts a refunded row", func(t *testing.T) {
		r := newRigSQLite(t)
		r.allow()
		r.liftBudgets()
		r.store.set(resendIntervalPath, 30*time.Second)
		r.store.set(globalAttemptLoginPath, 1)
		r.recordRefundedLedger(budget.Attempt{
			DestinationMAC: r.database.BlindIndex(otherPhone),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
		if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
			t.Fatalf("a send after a refunded attempt returned %v, want ErrThrottled", err)
		}
	})
}

// TestTheResendIntervalReadsTheNewestLedgerAttempt checks that the resend interval reads the newest ledger attempt.
func TestTheResendIntervalReadsTheNewestLedgerAttempt(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.recordLedger(budget.Attempt{
		DestinationMAC: r.database.BlindIndex(phone),
		AttemptedAt:    r.clock.read().Add(-time.Second),
	})
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a send one second after a ledger attempt returned %v, want ErrThrottled", err)
	}
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("the refusal is %T, want *ThrottledError", err)
	}
	if want := 29 * time.Second; throttled.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v (the interval less the second elapsed)", throttled.RetryAfter, want)
	}
}

// TestTheResendIntervalCountsRefundedAndSuppressedAttempts checks that the resend interval counts refunded and suppressed attempts.
func TestTheResendIntervalCountsRefundedAndSuppressedAttempts(t *testing.T) {
	cases := []struct {
		name  string
		write func(*rig)
	}{
		{
			name: "refunded",
			write: func(r *rig) {
				r.recordRefundedLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(phone),
					AttemptedAt:    r.clock.read().Add(-time.Second),
				})
			},
		},
		{
			name: "suppressed",
			write: func(r *rig) {
				r.recordSuppressedLedger(budget.Attempt{
					DestinationMAC: r.database.BlindIndex(phone),
					AttemptedAt:    r.clock.read().Add(-time.Second),
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
			c.write(r)
			if _, err := r.sendLogin(); !errors.Is(err, authn.ErrThrottled) {
				t.Fatalf("a send after a %s attempt one second old returned %v, want ErrThrottled", c.name, err)
			}
		})
	}
}

// TestThePhoneDailyWindowReadsTheLedgerSeparately checks that the phone daily window reads the ledger separately.
func TestThePhoneDailyWindowReadsTheLedgerSeparately(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perPhoneDailyPath, 1)
	r.recordLedger(budget.Attempt{
		DestinationMAC: r.database.BlindIndex(phone),
		AttemptedAt:    r.clock.read().Add(-90 * time.Minute),
	})
	_, err := r.sendLogin()
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a send with one attempt inside the day and none inside the hour returned %v, want ErrThrottled (the daily budget)", err)
	}
	var throttled *authn.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("the refusal is %T, want *ThrottledError", err)
	}
	if want := 22*time.Hour + 30*time.Minute; throttled.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", throttled.RetryAfter, want)
	}
}

// TestTheSourceDistinctBudgetCountsLedgerDestinations checks that the source distinct budget counts ledger destinations.
func TestTheSourceDistinctBudgetCountsLedgerDestinations(t *testing.T) {
	const source = "203.0.113.93"
	r := newRigSQLite(t)
	r.allow()
	r.liftBudgets()
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perSourceDistinctHourlyPath, 2)
	mac := r.database.BlindIndex(source)
	for i := range 2 {
		r.recordLedger(budget.Attempt{
			SourceMAC:      &mac,
			DestinationMAC: r.database.BlindIndex(fmt.Sprintf("+861380018%04d", i)),
			AttemptedAt:    r.clock.read().Add(-31 * time.Second),
		})
	}
	_, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Source: source, Deliver: true,
	})
	if !errors.Is(err, authn.ErrThrottled) {
		t.Fatalf("a third destination from one source returned %v, want ErrThrottled (the distinct budget)", err)
	}
}
