package invite

import (
	"context"
	"errors"
	"testing"

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
			if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
				t.Fatalf("row status = %d, want revoked after %s", got, change)
			}
			ctx := h.acceptCtx("acct-member", "member@example.com")
			if _, err := h.p.Accept(ctx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationRevoked) {
				t.Errorf("accepting after %s gave %v, want ErrInvitationRevoked", change, err)
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
