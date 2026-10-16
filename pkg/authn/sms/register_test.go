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
