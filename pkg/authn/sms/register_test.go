package sms

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestConsumePassesOnceAndRefusesTheSecondSubmit pins the one-winner gate at the
// layer that decides it: the conditional consume of a live row succeeds once and
// returns false the second time, which is what makes two correct submits of one
// challenge have exactly one winner. The concurrent-submit case does not pin
// this: with the increment gate ahead of it, its submits are serialized so the
// consume gate never gets to decide there.
func TestConsumePassesOnceAndRefusesTheSecondSubmit(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	id, _ := r.sentCode()
	now := r.clock.read().Add(time.Minute)

	first, err := r.p.register.consume(context.Background(), id, now)
	if err != nil {
		t.Fatalf("the first consume: %v", err)
	}
	if !first {
		t.Fatal("the first consume of a live row returned false, want true")
	}
	second, err := r.p.register.consume(context.Background(), id, now)
	if err != nil {
		t.Fatalf("the second consume: %v", err)
	}
	if second {
		t.Error("the second consume of a spent row returned true, want false")
	}
}

// TestAResendLeavesASpentRowsConsumedAtUnchanged holds the void's column guard:
// the void matches only rows whose consumed_at is still empty, so a resend does
// not overwrite the consumed_at of a row already consumed or voided.
func TestAResendLeavesASpentRowsConsumedAtUnchanged(t *testing.T) {
	r := newRigSQLite(t)
	r.allow()
	phoneMac := r.database.BlindIndex(phone)
	spentAt := base.Add(-30 * time.Second)
	r.insert("spent", purposeLogin, phoneMac, nil, false, "mac", 1, 5,
		base.Add(-2*time.Minute), base.Add(time.Minute), &spentAt)

	before, ok := r.column("spent", "consumed_at")
	if !ok || before == nil {
		t.Fatal("the spent row has no consumed_at to hold")
	}

	if err := r.p.register.insert(context.Background(), challengeRow{
		ID: "fresh", Purpose: purposeLogin, PhoneMAC: phoneMac, CodeMAC: "mac-new",
		MaxAttempts: 5, CreatedAt: r.clock.read(), ExpiresAt: r.clock.read().Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("the resend insert: %v", err)
	}

	after, _ := r.column("spent", "consumed_at")
	if after == nil {
		t.Fatal("the resend cleared the spent row's consumed_at")
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("the resend overwrote the spent row's consumed_at: before %v, after %v", before, after)
	}
}

// TestIncrementLeavesASpentRowAlone holds the increment's column guard: a row
// whose consumed_at is set is not counted, so a submit against a spent row does
// not advance its attempts.
func TestIncrementLeavesASpentRowAlone(t *testing.T) {
	r := newRigSQLite(t)
	at := base.Add(-time.Minute)
	r.insert("spent", purposeLogin, "m", nil, false, "mac", 0, 5,
		base.Add(-2*time.Minute), base.Add(time.Minute), &at)

	counted, err := r.p.register.increment(context.Background(), "spent", r.clock.read())
	if err != nil {
		t.Fatalf("increment: %v", err)
	}
	if counted {
		t.Error("increment counted a spent row, want it left alone")
	}
	if attempts, _ := r.column("spent", "attempts"); toInt(attempts) != 0 {
		t.Errorf("a spent row's attempts advanced to %v, want 0", attempts)
	}
}

// TestOverlappingResendsOfOneNumberSerialize holds the resend rule under
// overlap: two Send transactions for the same (number, purpose) cannot each read
// no live row and write their own, leaving two codes valid at once. The case
// forces the overlap by holding one transaction's lock open while the other
// runs, so it does not depend on a timing race. The lock is PostgreSQL-only
// (SQLite serializes writers itself), so this runs on that engine alone.
func TestOverlappingResendsOfOneNumberSerialize(t *testing.T) {
	database := openDialect(t, postgresDialect)
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
