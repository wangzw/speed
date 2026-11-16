package invite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// membershipChangedEvent is the event a case hands the subscriber.
func membershipChangedEvent(tenant, account, change string) event.Event {
	return event.Event{
		Type:     authn.EventMembershipChanged,
		TenantID: tenant,
		Payload:  authn.MembershipChanged{AccountID: account, Change: change},
	}
}

// TestSuspendingOrRemovingAMemberRevokesTheirPendingInvitations holds the rule:
// a member who is suspended or removed loses the invitations addressed to them
// in that tenant, and the token then answers the terminal revoked state.
func TestSuspendingOrRemovingAMemberRevokesTheirPendingInvitations(t *testing.T) {
	for _, change := range []string{membershipChangeSuspended, membershipChangeRemoved} {
		t.Run(change, func(t *testing.T) {
			h := newHarness(t)
			h.accounts.put("member@example.com", "acct-member")
			issued := h.issue(tenant, "member@example.com")

			if err := h.p.onMembershipChanged(context.Background(),
				membershipChangedEvent(tenant, "acct-member", change)); err != nil {
				t.Fatalf("handling the %s event: %v", change, err)
			}
			row := h.row(issued.Invitation.ID)
			if row.Status != statusRevoked {
				t.Fatalf("row status = %d, want revoked after %s", row.Status, change)
			}
			if row.RevokedAt == nil {
				t.Errorf("the %s path wrote no revoked_at", change)
			}
			ctx := h.acceptCtx("acct-member", "member@example.com")
			if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationRevoked) {
				t.Errorf("accepting after %s gave %v, want ErrInvitationRevoked", change, err)
			}
		})
	}
}

// TestASupersededExpiredRowIsNotRevoked holds the condition on both system
// writes: a suspended or removed member and a deleted organization end the
// grants that can still be redeemed, so a row whose deadline has passed is left
// as expired. Two expired rows make that condition tell a deadline judgement
// from the other predicates it could be confused with: a superseded row, which
// keeps its stored pending state and records only that it was replaced, and a
// row no later invitation replaced, which carries no superseded_at at all.
// Neither may be revoked, so an implementation that keys on superseded_at
// instead of the deadline is refused by the second row.
func TestASupersededExpiredRowIsNotRevoked(t *testing.T) {
	cases := []struct {
		name  string
		write func(*harness, string) error
	}{
		{
			name: "a suspended member",
			write: func(h *harness, address string) error {
				return h.p.onMembershipChanged(context.Background(),
					membershipChangedEvent(tenant, "acct-member", membershipChangeSuspended))
			},
		},
		{
			name: "a deleted organization",
			write: func(h *harness, _ string) error {
				return h.p.RevokeTenantInvitations(context.Background(), tenant)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.accounts.put("member@example.com", "acct-member")
			// The member's second address is expired and left unreplaced, so the
			// system write would meet it on the same predicate as a dead row with
			// no replacement history.
			h.accounts.put("member-second@example.com", "acct-member")
			unreplaced := h.issue(tenant, "member-second@example.com")
			old := h.issue(tenant, "member@example.com")
			h.clock.advance(DefaultTTL + time.Minute)
			// The second issuance supersedes the expired one: the old row keeps
			// its stored pending state and records only that it was replaced.
			live := h.issue(tenant, "member@example.com")

			if err := c.write(h, "member@example.com"); err != nil {
				t.Fatalf("the system write gave %v", err)
			}

			superseded := h.row(old.Invitation.ID)
			if superseded.Status != statusPending {
				t.Errorf("the superseded expired row reads status %d, want it left pending", superseded.Status)
			}
			if superseded.RevokedAt != nil {
				t.Errorf("the superseded expired row was revoked at %v, want it left as expired", superseded.RevokedAt)
			}
			if got := readState(superseded, h.clock.read()); got != viewStateExpired {
				t.Errorf("the superseded row reads %q, want %q", got, viewStateExpired)
			}
			expired := h.row(unreplaced.Invitation.ID)
			if expired.Status != statusPending {
				t.Errorf("the expired unreplaced row reads status %d, want it left pending", expired.Status)
			}
			if expired.RevokedAt != nil {
				t.Errorf("the expired unreplaced row was revoked at %v, want it left as expired", expired.RevokedAt)
			}
			if got := readState(expired, h.clock.read()); got != viewStateExpired {
				t.Errorf("the expired unreplaced row reads %q, want %q", got, viewStateExpired)
			}
			if got := h.row(live.Invitation.ID).Status; got != statusRevoked {
				t.Errorf("the live row reads status %d, want revoked", got)
			}
		})
	}
}

// TestSuspensionLeavesOtherRowsAndChangesAlone holds the condition on the write:
// only the pending invitation for that tenant and that member's address is
// revoked. Another tenant's invitation, another address's invitation, an
// accepted invitation and a membership that is added rather than withdrawn stay
// as they are.
func TestSuspensionLeavesOtherRowsAndChangesAlone(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("member@example.com", "acct-member")
	member := h.issue(tenant, "member@example.com")
	otherAddress := h.issue(tenant, "other@example.com")
	otherTenant := h.issue("tenant-b", "member@example.com")

	accepted := h.issue(tenant, "accepted@example.com")
	ctx := h.acceptCtx("acct-accepted", "accepted@example.com")
	if _, err := h.p.Accept(ctx, AcceptInvite{Token: accepted.Token}); err != nil {
		t.Fatalf("accepting the control invitation: %v", err)
	}

	if err := h.p.onMembershipChanged(context.Background(),
		membershipChangedEvent(tenant, "acct-member", membershipChangeSuspended)); err != nil {
		t.Fatalf("handling the suspension: %v", err)
	}

	if got := h.row(member.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("the member's own row = %d, want revoked", got)
	}
	if got := h.row(member.Invitation.ID).RevokedAt; got == nil {
		t.Error("the member's own row carries no revoked_at")
	}
	if got := h.row(otherAddress.Invitation.ID).Status; got != statusPending {
		t.Errorf("another address's row = %d, want pending", got)
	}
	if got := h.row(otherTenant.Invitation.ID).Status; got != statusPending {
		t.Errorf("another tenant's row = %d, want pending", got)
	}
	if got := h.row(accepted.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("an accepted row = %d, want accepted", got)
	}

	if err := h.p.onMembershipChanged(context.Background(),
		membershipChangedEvent(tenant, "acct-member", "added")); err != nil {
		t.Fatalf("handling the added event: %v", err)
	}
	if got := h.row(member.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("a non-withdrawing change altered the row to %d", got)
	}
}
