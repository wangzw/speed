package invite

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestAnInvitationViewWritesNullForAnAbsentTime holds the view's shape: a time
// an invitation does not have yet is absent, not the zero instant. The
// management face reads null and leaves the column empty; a fabricated
// 0001-01-01T00:00:00Z reads as a real moment to every client. last_sent_at is
// not one of the absent times: issuance writes it, so it is always a moment.
func TestAnInvitationViewWritesNullForAnAbsentTime(t *testing.T) {
	issuedAt := time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC)
	view := invitationViewOf(Invitation{
		ID:         "inv-1",
		Tenant:     "tenant-a",
		Email:      "alice@example.com",
		Status:     InvitationPending,
		ExpiresAt:  issuedAt.Add(DefaultTTL),
		LastSentAt: issuedAt,
		CreatedAt:  issuedAt,
	}, "acme", issuedAt, inviterNames{})

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	for _, field := range []string{"accepted_at", "revoked_at"} {
		if !bytes.Contains(encoded, []byte(`"`+field+`":null`)) {
			t.Errorf("the view writes %s as something other than null: %s", field, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(`"last_sent_at":"2031-03-04T12:00:00Z"`)) {
		t.Errorf("the view does not write the send instant: %s", encoded)
	}
	if bytes.Contains(encoded, []byte("0001-01-01T00:00:00Z")) {
		t.Errorf("the view writes the zero instant: %s", encoded)
	}
}

// TestAnAcceptedInvitationViewWritesTheAcceptanceTime holds the other side: a
// time the invitation does have is written as the moment it happened.
func TestAnAcceptedInvitationViewWritesTheAcceptanceTime(t *testing.T) {
	acceptedAt := time.Date(2031, 3, 4, 12, 30, 0, 0, time.UTC)
	view := invitationViewOf(Invitation{
		ID:         "inv-1",
		Tenant:     "tenant-a",
		Status:     InvitationAccepted,
		ExpiresAt:  acceptedAt.Add(DefaultTTL),
		AcceptedAt: &acceptedAt,
		LastSentAt: acceptedAt,
		CreatedAt:  acceptedAt,
	}, "acme", acceptedAt, inviterNames{})
	if view.AcceptedAt == nil || !view.AcceptedAt.Equal(acceptedAt) {
		t.Errorf("accepted_at = %v, want the acceptance instant %v", view.AcceptedAt, acceptedAt)
	}
	if view.RevokedAt != nil {
		t.Errorf("revoked_at = %v, want it absent on an accepted invitation", view.RevokedAt)
	}
}

// TestTheDomainInvitationKeepsAbsentTimesAbsent holds the seam the views are
// built from: the row's NULL columns reach the value type as absent, not as the
// zero time a caller would have to tell apart by convention. The value is read
// through its JSON form, which is the contract's shape, so the case holds the
// whole path from the row to the answer.
func TestTheDomainInvitationKeepsAbsentTimesAbsent(t *testing.T) {
	sentAt := time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC)
	inv := toInvitation(invitationRow{
		ID: "inv-1", TenantID: "tenant-a", Email: "alice@example.com",
		Status: statusPending, InvitedBy: "acct-admin",
		ExpiresAt:  time.Date(2031, 3, 11, 12, 0, 0, 0, time.UTC),
		LastSentAt: sentAt,
		CreatedAt:  time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC),
	})
	if inv.InvitedBy != authn.AccountID("acct-admin") {
		t.Errorf("invited_by = %q, want the stored issuer", inv.InvitedBy)
	}
	view := invitationViewOf(inv, "acme", sentAt, inviterNames{})
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"accepted_at":null`)) {
		t.Errorf("a NULL column reached the view as something other than null: %s", encoded)
	}
}
