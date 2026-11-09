package invite

import (
	"github.com/vislake/speed/pkg/authn"
)

// The views the routes answer with. Each is built field by field from a value
// type so that a field added later does not reach a client by being there. No
// view carries a token except the one issue answer, which hands it out once.
// The types themselves are declared in operations.go, where the fragment reads
// their names and descriptions.

// invitationViewOf builds one invitation's view. It never carries the token.
func invitationViewOf(v Invitation, tenantName string) AuthnInviteInvitation {
	return AuthnInviteInvitation{
		ID:         v.ID,
		Tenant:     tenantName,
		Email:      v.Email,
		Status:     v.Status.String(),
		InvitedBy:  string(v.InvitedBy),
		ExpiresAt:  v.ExpiresAt.UTC(),
		AcceptedAt: v.AcceptedAt.UTC(),
		CreatedAt:  v.CreatedAt.UTC(),
	}
}

// issuedViewOf builds the answer of an issue: the invitation's view, the
// acceptance token handed out here once, and the notice's outcome. The token
// must not be logged.
func issuedViewOf(issued Issued, delivery authn.InviteDelivery, tenantName string) AuthnInviteIssued {
	return AuthnInviteIssued{
		Invitation:    invitationViewOf(issued.Invitation, tenantName),
		Token:         issued.Token,
		EmailDelivery: delivery.String(),
	}
}

// acceptViewOf builds the answer of an acceptance: the membership that was
// written.
func acceptViewOf(mem authn.Membership, tenantName string) AuthnInviteAccept {
	return AuthnInviteAccept{
		Account:   string(mem.Account),
		Tenant:    tenantName,
		Active:    mem.Active,
		Source:    mem.Source,
		SSOExempt: mem.SSOExempt,
	}
}
