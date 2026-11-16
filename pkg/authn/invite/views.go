package invite

import (
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The states the acceptance read publishes. A stored pending row whose deadline
// has passed reads expired; expiry is derived from the clock, never stored.
const (
	viewStatePending  = "pending"
	viewStateExpired  = "expired"
	viewStateAccepted = "accepted"
	viewStateRevoked  = "revoked"
)

// readState judges a row for the acceptance read: a terminal row by its stored
// state, and a pending one by its deadline.
func readState(row invitationRow, now time.Time) string {
	switch row.Status {
	case statusAccepted:
		return viewStateAccepted
	case statusRevoked:
		return viewStateRevoked
	}
	if !utc(row.ExpiresAt).After(now) {
		return viewStateExpired
	}
	return viewStatePending
}

// The views the routes answer with. Each is built field by field from a value
// type so that a field added later does not reach a client by being there. No
// view carries a token except the issue answer, and that one carries it only
// when the notice did not reach the address. The types themselves are declared
// in operations.go, where the fragment reads their names and descriptions.

// invitationViewOf builds one invitation's view. It never carries the token.
func invitationViewOf(v Invitation, tenantName string) AuthnInviteInvitation {
	return AuthnInviteInvitation{
		ID:         v.ID,
		Tenant:     tenantName,
		Email:      v.Email,
		Status:     v.Status.String(),
		InvitedBy:  string(v.InvitedBy),
		ExpiresAt:  v.ExpiresAt.UTC(),
		AcceptedAt: instant(v.AcceptedAt),
		CreatedAt:  v.CreatedAt.UTC(),
	}
}

// instant is t as an RFC 3339 instant in UTC, and nil for the zero time, which
// a view publishes as null.
func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	at := t.UTC()
	return &at
}

// issuedViewOf builds the answer of an issue: the invitation's view, the
// notice's outcome, and the acceptance token only when the notice did not reach
// the address. A sent notice means the recipient holds the link already, so the
// answer must not carry the plaintext token a second time; a failed or
// unconfigured notice is the case where the inviter has to hand it over. The
// token must not be logged.
func issuedViewOf(issued Issued, delivery authn.InviteDelivery, tenantName string) AuthnInviteIssued {
	view := AuthnInviteIssued{
		Invitation:    invitationViewOf(issued.Invitation, tenantName),
		EmailDelivery: delivery.String(),
	}
	if delivery != authn.InviteSent {
		view.Token = issued.Token
	}
	return view
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
