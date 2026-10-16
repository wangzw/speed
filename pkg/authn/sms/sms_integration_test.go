//go:build integration

package sms

import (
	"context"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

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

	// B must wait for A's transaction: while A holds the lock, B cannot have
	// written. Without the lock, B would run to completion here.
	select {
	case err := <-done:
		t.Fatalf("the second insert did not wait for the first: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := txA.Commit().Error; err != nil {
		t.Fatalf("committing A: %v", err)
	}
	committed = true
	if err := <-done; err != nil {
		t.Fatalf("the second insert: %v", err)
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
