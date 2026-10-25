//go:build integration

package sms

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

const (
	// lockWaitBudget bounds the wait for the second insert to be seen waiting on
	// the lock. The insert reaches the lock in the microseconds of one statement;
	// the budget only stops the case hanging if it never does, and a case that
	// saw neither completion nor the wait fails rather than passes.
	lockWaitBudget = 10 * time.Second
	// lockWaitPoll is how often the case looks while the second insert runs.
	lockWaitPoll = 2 * time.Millisecond
)

// aResendIsWaitingForTheLock reports whether a session on this database is
// blocked on a lock. Only this case's connections exist on the database it runs
// on, and the first transaction is the one holding the lock, so the only session
// that can be waiting is the second insert, held on the key the first took.
func aResendIsWaitingForTheLock(t *testing.T, h *gorm.DB) bool {
	t.Helper()
	var waiting int64
	err := h.Raw(
		"SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() " +
			"AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()").
		Scan(&waiting).Error
	if err != nil {
		t.Fatalf("reading the waiting sessions: %v", err)
	}
	return waiting > 0
}

// theResendReachedTheLock drives the second insert to the one of two states that
// decides the case. It reports held=true once the insert is seen waiting on the
// key the first transaction holds, and held=false with the insert's error once
// the insert completes first (its serialize took no lock). It fails if neither
// is reached within the budget, so an insert neither done nor seen waiting
// cannot pass as serialized: a slow machine that has not run the insert yet is
// not mistaken for the insert being blocked.
func theResendReachedTheLock(t *testing.T, h *gorm.DB, done <-chan error) (bool, error) {
	t.Helper()
	deadline := time.Now().Add(lockWaitBudget)
	for {
		select {
		case err := <-done:
			return false, err
		default:
		}
		if aResendIsWaitingForTheLock(t, h) {
			return true, nil
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second insert neither completed nor was seen waiting on the lock within %s", lockWaitBudget)
		}
		time.Sleep(lockWaitPoll)
	}
}

// The module runs on PostgreSQL as well in an integration run, over a database
// of the case's own on the server the compose stack brings up: Acquire fails the
// case when that server does not answer rather than skipping it.
func init() {
	dialects = append(dialects, dialect{
		name: "postgres",
		open: func(t *testing.T) db.Database {
			return dbtest.Postgres(t, pgtest.Acquire(t), dbtest.Items{"encryption-key": rootKey})
		},
	})
}

// TestOverlappingResendsOfOneNumberSerialize holds the resend rule under
// overlap: two Send transactions for the same (number, purpose) cannot each read
// no live row and write their own, leaving two codes valid at once. The case
// forces the overlap by holding one transaction's lock open while the other
// runs, so it does not depend on a timing race. The lock is PostgreSQL-only
// (SQLite serializes writers itself), so this runs on that engine alone.
func TestOverlappingResendsOfOneNumberSerialize(t *testing.T) {
	database := openDialect(t, "postgres")
	r := newRig(t, database, newSender())
	phoneMac := r.database.BlindIndex(phone)
	now := r.clock.read()

	txA := r.h.Begin()
	committed := false
	defer func() {
		if !committed {
			_ = txA.Rollback()
		}
	}()
	if err := r.p.register.serialize(txA, phoneMac, purposeLogin); err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	if err := txA.Exec("INSERT INTO "+challengeTable+
		" (id, purpose, phone_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, 0, 5, ?, ?)",
		"A", purposeLogin, phoneMac, nil, false, "mac-a", now.UTC(), now.Add(5*time.Minute).UTC()).Error; err != nil {
		t.Fatalf("inserting A: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- r.p.register.insert(context.Background(), challengeRow{
			ID: "B", Purpose: purposeLogin, PhoneMAC: phoneMac, CodeMAC: "mac-b",
			MaxAttempts: 5, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		})
	}()

	// B must wait for A's transaction: while A holds the lock, B's backend is
	// seen waiting on it, which is the state observed here rather than elapsed
	// time. Without the lock, B would run to completion and the case would see
	// that instead.
	waiting, early := theResendReachedTheLock(t, r.h, done)
	if !waiting {
		t.Fatalf("the second insert did not wait for the first: %v", early)
	}

	// The insert is blocked while its backend waits on the lock, so it cannot
	// have completed here.
	select {
	case err := <-done:
		t.Fatalf("the second insert completed while its backend was waiting on the lock: %v", err)
	default:
	}

	if err := txA.Commit().Error; err != nil {
		t.Fatalf("committing A: %v", err)
	}
	committed = true

	// With the first transaction committed, the second proceeds.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the second insert: %v", err)
		}
	case <-time.After(lockWaitBudget):
		t.Fatalf("the second insert was still blocked %s after the first transaction committed", lockWaitBudget)
	}

	var live int64
	if err := r.h.Table(challengeTable).
		Where("phone_mac = ? AND purpose = ? AND consumed_at IS NULL", phoneMac, purposeLogin).
		Count(&live).Error; err != nil {
		t.Fatalf("counting the live rows: %v", err)
	}
	if live != 1 {
		t.Errorf("%d live challenges for one number and purpose, want 1", live)
	}
}
