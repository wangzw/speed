package invite

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestALostLiveSlotIsThePendingRefusal holds the index path rather than the
// pre-check. A create callback inserts a conflicting live row inside the same
// transaction after every target check has passed, so the issuance statement
// itself meets the partial unique index and the refusal is the 409
// invitation-pending the race produces. The failed transaction is rolled back
// whole, leaving neither the invitation nor the row the callback wrote.
//
// The concurrent case beside it proves the same answer against a real race, which
// only some interleavings take this path; this one takes it every time.
func TestALostLiveSlotIsThePendingRefusal(t *testing.T) {
	h := newHarness(t)
	var fired atomic.Bool
	const callbackName = "test:conflicting-live-row"
	if err := h.handle.Callback().Create().Before("gorm:create").Register(callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Table != tableInvitations {
				return
			}
			if !fired.CompareAndSwap(false, true) {
				return
			}
			now := h.clock.read()
			err := tx.Exec(
				`INSERT INTO authn_invitations
					(id, tenant_id, email, token_digest, status, invited_by, expires_at, last_sent_at, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				"inv-conflict", tenant, "alice@example.com", "digest-conflict",
				statusPending, string(adminID), now.Add(time.Hour), now, now, now,
			).Error
			if err != nil {
				_ = tx.AddError(err)
			}
		}); err != nil {
		t.Fatalf("registering the callback: %v", err)
	}
	// The handle belongs to this case alone, so the callback needs no removal:
	// taking it off would write a GORM warning for a handle that is about to be
	// discarded.
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "urn:speed:authn:invitation-pending") {
		t.Errorf("body %s does not carry invitation-pending", w.Body.String())
	}
	if !fired.Load() {
		t.Fatal("the callback never wrote the conflicting row, so the pre-check answered instead of the index")
	}
	if got := h.rowCount(); got != 0 {
		t.Errorf("the rolled-back transaction left %d rows, want none", got)
	}
}
