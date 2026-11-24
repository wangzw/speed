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
	lockWaitBudget = 10 * time.Second
	lockWaitPoll   = 2 * time.Millisecond
)

func aResendIsWaitingForTheLock(t *testing.T, h *gorm.DB) bool {
	t.Helper()
	var waiting int64
	err := h.Raw(
		"SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() " +
			"AND wait_event_type = 'Lock' AND wait_event = 'advisory' AND pid <> pg_backend_pid()").
		Scan(&waiting).Error
	if err != nil {
		t.Fatalf("reading the waiting sessions: %v", err)
	}
	return waiting > 0
}

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

func init() {
	dialects = append(dialects, dialect{
		name: "postgres",
		open: func(t *testing.T) db.Database {
			return dbtest.Postgres(t, pgtest.Acquire(t), dbtest.Items{"encryption-key": rootKey})
		},
	})
}

// TestOverlappingResendsOfOneNumberSerialize checks that overlapping resends of one number serialize.
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
	if err := r.p.register.lock(txA, challengeRow{Purpose: purposeLogin, PhoneMAC: phoneMac}); err != nil {
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
		_, err := r.p.register.insert(context.Background(), challengeRow{
			ID: "B", Purpose: purposeLogin, PhoneMAC: phoneMac, CodeMAC: "mac-b",
			MaxAttempts: 5, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		}, nil, nil)
		done <- err
	}()

	waiting, early := theResendReachedTheLock(t, r.h, done)
	if !waiting {
		t.Fatalf("the second insert did not wait for the first: %v", early)
	}

	select {
	case err := <-done:
		t.Fatalf("the second insert completed while its backend was waiting on the lock: %v", err)
	default:
	}

	if err := txA.Commit().Error; err != nil {
		t.Fatalf("committing A: %v", err)
	}
	committed = true

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
