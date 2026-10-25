//go:build integration

package sms

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// integrationSource is the client address the source-dimension cases send from.
const integrationSource = "203.0.113.7"

// budgetLockKey builds one of the advisory-lock keys the design documents, so a
// case takes the key the module is supposed to take rather than one derived from
// its implementation.
func budgetLockKey(dimension, value string) string {
	return "authn.sms:lock:" + dimension + ":" + value
}

// insertCountRow writes a challenge row in the transaction it is given, so a
// case can hold a count row uncommitted while another send runs. Time is written
// in UTC, as the module writes it. sourceMac is the row's source dimension, nil
// for a row with no source.
func insertCountRow(t *testing.T, tx *gorm.DB, id, purpose, phoneMac string, account, sourceMac *string, created time.Time) {
	t.Helper()
	var accountVal any
	if account != nil {
		accountVal = *account
	}
	var sourceVal any
	if sourceMac != nil {
		sourceVal = *sourceMac
	}
	if err := tx.Exec("INSERT INTO "+challengeTable+
		" (id, purpose, phone_mac, source_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, 0, 5, ?, ?)",
		id, purpose, phoneMac, sourceVal, accountVal, false, "mac", created.UTC(), created.Add(5*time.Minute).UTC()).Error; err != nil {
		t.Fatalf("inserting a count row: %v", err)
	}
}

// holdBudgetLock begins a transaction, takes the documented advisory lock on key
// with raw SQL, and runs write if it is set, all in that one transaction. The
// returned function commits, which both releases the lock and makes the row
// visible. A failure to commit fails the case.
func holdBudgetLock(t *testing.T, h *gorm.DB, key string, write func(*gorm.DB)) func() {
	t.Helper()
	tx := h.Begin()
	if tx.Error != nil {
		t.Fatalf("beginning the holding transaction: %v", tx.Error)
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
		_ = tx.Rollback()
		t.Fatalf("taking the documented lock %q: %v", key, err)
	}
	if write != nil {
		write(tx)
	}
	return func() {
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("committing the holding transaction: %v", err)
		}
	}
}

// TestTheBudgetIsCheckedUnderTheSendsOwnLocks holds that the budget is a hard
// cap rather than a soft one: the count is read only after the send has taken
// the documented lock, so a send that overlaps another on a dimension sees the
// other's row once it is committed and refuses.
//
// The case takes the documented advisory-lock key itself and inserts the count
// row uncommitted in the same transaction. While that transaction is open the
// send under test must not return; a send that read its budget before taking the
// lock would see no row (it is uncommitted) and go through. After the commit it
// must re-read, see the row, and refuse. The limit is set through the settings
// store, which is where the module reads it, and is not a hardcoded number in
// the code under test.
func TestTheBudgetIsCheckedUnderTheSendsOwnLocks(t *testing.T) {
	cases := []struct {
		name string
		key  func(*rig) string
		row  func(*rig, *gorm.DB)
		tune func(*rig)
		send func(*rig) error
		want error
	}{
		{
			name: "phone",
			key:  func(r *rig) string { return budgetLockKey("phone", r.database.BlindIndex(phone)) },
			row: func(r *rig, tx *gorm.DB) {
				insertCountRow(t, tx, "count-phone", purposeLogin, r.database.BlindIndex(phone), nil, nil, r.clock.read().Add(-31*time.Second))
			},
			tune: func(r *rig) { r.store.set(perPhoneHourlyPath, 1) },
			send: func(r *rig) error {
				_, err := r.sendLogin()
				return err
			},
			want: authn.ErrThrottled,
		},
		{
			name: "account",
			key:  func(r *rig) string { return budgetLockKey("account", string(acct)) },
			row: func(r *rig, tx *gorm.DB) {
				// Another number of the same account: the account dimension is the
				// one this case exercises, and the send's own number stays empty.
				account := string(acct)
				insertCountRow(t, tx, "count-account", authn.SMSPurposeMFA,
					r.database.BlindIndex("+8613800138002"), &account, nil, r.clock.read().Add(-31*time.Second))
			},
			tune: func(r *rig) { r.store.set(perAccountHourlyPath, 1) },
			send: func(r *rig) error {
				_, err := r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeMFA, Phone: phone, Account: acct, Deliver: true,
				})
				return err
			},
			want: authn.ErrThrottled,
		},
		{
			name: "global",
			key:  func(*rig) string { return budgetLockKey("global", purposeLogin) },
			row: func(r *rig, tx *gorm.DB) {
				insertCountRow(t, tx, "count-global", purposeLogin,
					r.database.BlindIndex("+8613800138002"), nil, nil, r.clock.read().Add(-31*time.Second))
			},
			tune: func(r *rig) { r.store.set(globalLoginPath, 1) },
			send: func(r *rig) error {
				_, err := r.sendLogin()
				return err
			},
			want: authn.ErrUnavailable,
		},
		{
			name: "source",
			key:  func(r *rig) string { return budgetLockKey("source", r.database.BlindIndex(integrationSource)) },
			row: func(r *rig, tx *gorm.DB) {
				// Another number from the same source: the source dimension is the
				// one this case exercises, and the send's own number stays empty.
				sourceMac := r.database.BlindIndex(integrationSource)
				insertCountRow(t, tx, "count-source", purposeLogin,
					r.database.BlindIndex("+8613800138002"), nil, &sourceMac, r.clock.read().Add(-31*time.Second))
			},
			tune: func(r *rig) { r.store.set(perSourceHourlyPath, 1) },
			send: func(r *rig) error {
				_, err := r.p.Send(context.Background(), authn.SendCode{
					Purpose: authn.SMSPurposeLogin, Phone: phone, Source: integrationSource, Deliver: true,
				})
				return err
			},
			want: authn.ErrThrottled,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database := openDialect(t, "postgres")
			r := newRig(t, database, newSender())
			r.allow()
			r.liftBudgets()
			// Past the resend interval but inside the hour, so the dimension under
			// test is the one that refuses.
			r.store.set(resendIntervalPath, 30*time.Second)
			c.tune(r)

			release := holdBudgetLock(t, r.h, c.key(r), func(tx *gorm.DB) { c.row(r, tx) })

			done := make(chan error, 1)
			go func() { done <- c.send(r) }()

			waiting, early := theResendReachedTheLock(t, r.h, done)
			if !waiting {
				release()
				t.Fatalf("the send did not wait for the %s lock, so its budget was not read under it; it returned %v", c.name, early)
			}
			// The backend is seen waiting on the lock, so the send cannot have
			// returned here.
			select {
			case err := <-done:
				release()
				t.Fatalf("the send completed while its backend was waiting on the %s lock: %v", c.name, err)
			default:
			}

			release()

			select {
			case err := <-done:
				if !errors.Is(err, c.want) {
					t.Fatalf("the send returned %v after the count row committed, want %v", err, c.want)
				}
			case <-time.After(lockWaitBudget):
				t.Fatalf("the send was still blocked %s after the %s lock was released", lockWaitBudget, c.name)
			}
			if n := r.rowCount(); n != 1 {
				t.Errorf("the table holds %d rows, want 1 (the count row alone): a refused send writes none", n)
			}
		})
	}
}

// TestConcurrentSendsDoNotOverrunTheBudget is the regression guard for the hard
// cap: many sends of one account race for a small account budget, each on a
// number of its own and split across the two purposes that count against an
// account, so the account dimension is what they share. The number accepted is
// the limit and not one more, and the same number of rows is written.
func TestConcurrentSendsDoNotOverrunTheBudget(t *testing.T) {
	database := openDialect(t, "postgres")
	r := newRig(t, database, newSender())
	r.allow()
	r.liftBudgets()

	const limit = 5
	const concurrent = 40
	r.store.set(resendIntervalPath, 30*time.Second)
	r.store.set(perAccountHourlyPath, limit)

	purposes := []string{authn.SMSPurposeMFA, authn.SMSPurposeEnroll}
	var wg sync.WaitGroup
	results := make(chan error, concurrent)
	for i := range concurrent {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A distinct number per send keeps the per-number budgets and the
			// resend interval out of it; the two purposes give the sends separate
			// global locks but one account lock.
			number := fmt.Sprintf("+86138002%05d", i)
			_, err := r.p.Send(context.Background(), authn.SendCode{
				Purpose: purposes[i%len(purposes)], Phone: number, Account: acct, Deliver: true,
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var accepted int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, authn.ErrThrottled):
			// A send over the budget, as it should be.
		default:
			t.Errorf("a losing send returned %v, want ErrThrottled", err)
		}
	}
	if accepted != limit {
		t.Errorf("%d sends were accepted, want exactly the account limit %d", accepted, limit)
	}
	if n := r.rowCount(); n != limit {
		t.Errorf("the table holds %d rows, want %d: an over-budget send writes none", n, limit)
	}
}

// TestConcurrentSendsFromOneSourceDoNotOverrunTheBudget is the regression guard
// for the source dimension: many sends from one address race for a small source
// budget, each to a number of its own, so the source is what they share. The
// number accepted is the limit and not one more, and the same number of rows is
// written.
func TestConcurrentSendsFromOneSourceDoNotOverrunTheBudget(t *testing.T) {
	database := openDialect(t, "postgres")
	r := newRig(t, database, newSender())
	r.allow()
	r.liftBudgets()

	const limit = 5
	const concurrent = 40
	r.store.set(perSourceHourlyPath, limit)

	var wg sync.WaitGroup
	results := make(chan error, concurrent)
	for i := range concurrent {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			number := fmt.Sprintf("+86138009%05d", i)
			_, err := r.p.Send(context.Background(), authn.SendCode{
				Purpose: authn.SMSPurposeLogin, Phone: number, Source: integrationSource, Deliver: true,
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var accepted int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, authn.ErrThrottled):
			// A send over the budget, as it should be.
		default:
			t.Errorf("a losing send returned %v, want ErrThrottled", err)
		}
	}
	if accepted != limit {
		t.Errorf("%d sends were accepted, want exactly the source limit %d", accepted, limit)
	}
	if n := r.rowCount(); n != limit {
		t.Errorf("the table holds %d rows, want %d: an over-budget send writes none", n, limit)
	}
}
