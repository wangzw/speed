package invite

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// TestSettlingASuspendedMemberLeavesAnExpiredInvitationPending checks that a settlement over an expired row leaves it pending rather than revoked.
func TestSettlingASuspendedMemberLeavesAnExpiredInvitationPending(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.members.hold(authn.Membership{Tenant: tenant, Account: "acct-alice", Active: false})
	h.members.duringRead = func() { h.clock.advance(DefaultTTL + time.Minute) }

	_, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token})
	if !errors.Is(err, ErrInvitationMembershipInactive) {
		t.Fatalf("accepting as a suspended member gave %v, want ErrInvitationMembershipInactive", err)
	}
	row := h.row(issued.Invitation.ID)
	if row.Status != statusPending {
		t.Errorf("the expired invitation's stored status = %d, want it left pending so it reads expired", row.Status)
	}
	if row.RevokedAt != nil {
		t.Errorf("the expired invitation was revoked at %v, want it left expired", row.RevokedAt)
	}
	for _, entry := range h.audit.entries() {
		if entry.Action == actionRevoke && entry.Object == issued.Invitation.ID {
			t.Errorf("the unsettled invitation left a revocation audit row: %+v", entry)
		}
	}
}
