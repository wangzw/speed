package invite

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestRevokeRefusesTerminalInvitationsAndIsIdempotent checks that expired and
// accepted rows are refused and an already revoked one succeeds without moving its time.
func TestRevokeRefusesTerminalInvitationsAndIsIdempotent(t *testing.T) {
	t.Run("pending writes the revocation instant", func(t *testing.T) {
		h := newHarness(t)
		issued := h.issue(tenant, "alice@example.com")
		at := h.clock.read()
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("revoking: %v", err)
		}
		row := h.row(issued.Invitation.ID)
		if row.Status != statusRevoked {
			t.Errorf("status = %d, want revoked", row.Status)
		}
		if row.RevokedAt == nil || !row.RevokedAt.Equal(at) {
			t.Errorf("revoked_at = %v, want the revocation instant %v", row.RevokedAt, at)
		}
	})
	t.Run("expired", func(t *testing.T) {
		h := newHarness(t)
		issued, err := h.p.Issue(context.Background(), IssueInvite{
			Tenant: tenant, Email: "alice@example.com", ExpiresAt: h.clock.read().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("issuing: %v", err)
		}
		h.clock.advance(2 * time.Hour)
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationExpired) {
			t.Fatalf("revoking an expired invitation gave %v, want ErrInvitationExpired", err)
		}
		row := h.row(issued.Invitation.ID)
		if row.Status != statusPending || row.RevokedAt != nil {
			t.Errorf("the refused revoke left status %d and revoked_at %v, want pending and nil", row.Status, row.RevokedAt)
		}
	})
	t.Run("accepted", func(t *testing.T) {
		h := newHarness(t)
		issued := h.issue(tenant, "alice@example.com")
		if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
			t.Fatalf("accepting: %v", err)
		}
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); !errors.Is(err, ErrInvitationAlreadyAccepted) {
			t.Fatalf("revoking an accepted invitation gave %v, want ErrInvitationAlreadyAccepted", err)
		}
		if got := h.row(issued.Invitation.ID).RevokedAt; got != nil {
			t.Errorf("the refused revoke wrote revoked_at %v", got)
		}
	})
	t.Run("already revoked", func(t *testing.T) {
		h := newHarness(t)
		issued := h.issue(tenant, "alice@example.com")
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("revoking: %v", err)
		}
		first := h.row(issued.Invitation.ID).RevokedAt
		if first == nil {
			t.Fatal("the first revoke wrote no revoked_at")
		}
		h.clock.advance(time.Minute)
		if err := h.p.Revoke(context.Background(), tenant, issued.Invitation.ID); err != nil {
			t.Fatalf("revoking an already revoked invitation gave %v, want an idempotent success", err)
		}
		second := h.row(issued.Invitation.ID).RevokedAt
		if second == nil || !second.Equal(*first) {
			t.Errorf("the second revoke moved revoked_at from %v to %v", first, second)
		}
	})
}

// TestTheRevokeRouteAnswers409ForAnExpiredInvitation holds the problem code on
// the management face.
func TestTheRevokeRouteAnswers409ForAnExpiredInvitation(t *testing.T) {
	h := newHarness(t)
	issued, err := h.p.Issue(context.Background(), IssueInvite{
		Tenant: tenant, Email: "alice@example.com", ExpiresAt: h.clock.read().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	h.clock.advance(2 * time.Hour)
	h.asAdmin()
	w := h.request(http.MethodDelete, pathAdmin+"/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "urn:speed:authn:invitation-terminal") {
		t.Errorf("body %s does not carry invitation-terminal", w.Body.String())
	}
}

// TestTheRevokeRouteIsIdempotentForAnAlreadyRevokedInvitation checks that a
// repeated revoke over HTTP is a second 204 and keeps the recorded time.
func TestTheRevokeRouteIsIdempotentForAnAlreadyRevokedInvitation(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.asAdmin()
	path := pathAdmin + "/" + issued.Invitation.ID

	if w := h.request(http.MethodDelete, path, nil); w.Code != http.StatusNoContent {
		t.Fatalf("the first revoke answered %d, want 204: %s", w.Code, w.Body.String())
	}
	first := h.row(issued.Invitation.ID).RevokedAt
	if first == nil {
		t.Fatal("the first revoke wrote no revoked_at")
	}
	h.clock.advance(time.Minute)
	if w := h.request(http.MethodDelete, path, nil); w.Code != http.StatusNoContent {
		t.Fatalf("the second revoke answered %d, want the idempotent 204: %s", w.Code, w.Body.String())
	}
	second := h.row(issued.Invitation.ID).RevokedAt
	if second == nil || !second.Equal(*first) {
		t.Errorf("the second revoke moved revoked_at from %v to %v", first, second)
	}
}

// TestRevokeTenantInvitationsWritesRevokedAt checks that every pending row is
// revoked with its time and terminal rows are untouched.
func TestRevokeTenantInvitationsWritesRevokedAt(t *testing.T) {
	h := newHarness(t)
	pending := h.issue(tenant, "pending@example.com")
	accepted := h.issue(tenant, "accepted@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-accepted", "accepted@example.com"), AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	if err := h.p.RevokeTenantInvitations(context.Background(), tenant); err != nil {
		t.Fatalf("revoking the tenant's invitations: %v", err)
	}
	row := h.row(pending.Invitation.ID)
	if row.Status != statusRevoked || row.RevokedAt == nil {
		t.Errorf("the pending row reads status %d and revoked_at %v, want revoked with a time", row.Status, row.RevokedAt)
	}
	if got := h.row(accepted.Invitation.ID); got.Status != statusAccepted || got.RevokedAt != nil {
		t.Errorf("the accepted row changed to %d with revoked_at %v", got.Status, got.RevokedAt)
	}
}

// TestAFailedAcceptanceIsNotARevocation checks that a failed acceptance leaves
// the invitation pending with no revocation.
func TestAFailedAcceptanceIsNotARevocation(t *testing.T) {
	h := newHarness(t)
	h.members.err = authn.ErrBindingConflict
	issued := h.issue(tenant, "alice@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); !errors.Is(err, authn.ErrBindingConflict) {
		t.Fatalf("accepting over a conflicting binding gave %v, want ErrBindingConflict", err)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusPending || row.RevokedAt != nil {
		t.Errorf("the row reads status %d and revoked_at %v, want pending with no revocation", row.Status, row.RevokedAt)
	}
	if row.AcceptedAt != nil {
		t.Errorf("accepted_at = %v, want nil: the claim rolled back", row.AcceptedAt)
	}
}

// TestASuspendedMembershipRevokesWithRevokedAt checks that an acceptance over a
// suspended membership revokes the invitation with its time.
func TestASuspendedMembershipRevokesWithRevokedAt(t *testing.T) {
	h := newHarness(t)
	h.members.hold(authn.Membership{Tenant: tenant, Account: "acct-alice", Active: false})
	issued := h.issue(tenant, "alice@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationMembershipInactive) {
		t.Fatalf("accepting with a suspended membership gave %v, want ErrInvitationMembershipInactive", err)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusRevoked || row.RevokedAt == nil {
		t.Errorf("the row reads status %d and revoked_at %v, want revoked with a time", row.Status, row.RevokedAt)
	}
}
