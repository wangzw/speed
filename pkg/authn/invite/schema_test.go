package invite

import (
	"testing"
	"time"
)

// TestSendCountDefaultsToTheFirstSend checks that a row leaving the counter to the
// table counts its first send as one.
func TestSendCountDefaultsToTheFirstSend(t *testing.T) {
	h := newHarness(t)
	now := h.clock.read()
	if err := h.handle.Exec(
		`INSERT INTO authn_invitations
			(id, tenant_id, email, token_digest, status, invited_by, expires_at, last_sent_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"inv-default", tenant, "default@example.com", "digest-default", statusPending,
		string(adminID), now.Add(time.Hour), now, now, now,
	).Error; err != nil {
		t.Fatalf("inserting a row that leaves send_count to the table: %v", err)
	}
	if got := h.row("inv-default").SendCount; got != 1 {
		t.Errorf("send_count default = %d, want the first send counted as one", got)
	}
}
