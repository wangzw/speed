package invite

import (
	"context"
	"strings"
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

// invitationState judges an invitation: a terminal one by its stored state, a
// superseded one as expired even before its deadline — a later invitation took
// the live slot, so the link grants nothing — and an ordinary pending one by its
// deadline. The management view, the listing's two pending filters and the
// acceptance read all judge it this way.
func invitationState(status InvitationStatus, expiresAt time.Time, superseded bool, now time.Time) string {
	switch status {
	case InvitationAccepted:
		return viewStateAccepted
	case InvitationRevoked:
		return viewStateRevoked
	}
	if superseded || !utc(expiresAt).After(now) {
		return viewStateExpired
	}
	return viewStatePending
}

// readState judges a row for the acceptance read: a terminal row by its stored
// state, a superseded one as expired, and a pending one by its deadline.
func readState(row invitationRow, now time.Time) string {
	status := InvitationPending
	switch row.Status {
	case statusAccepted:
		status = InvitationAccepted
	case statusRevoked:
		status = InvitationRevoked
	}
	return invitationState(status, row.ExpiresAt, row.SupersededAt != nil, now)
}

// The views the routes answer with. Each is built field by field from a value
// type so that a field added later does not reach a client by being there. No
// view carries a token except the issue answer, and that one carries it only
// when the notice did not reach the address. The types themselves are declared
// in operations.go, where the fragment reads their names and descriptions.

// invitationViewOf builds one invitation's view. It never carries the token.
// The state is judged here, on the server's clock, so a client never derives it
// from its own clock and disagrees with the listing's filter. A row a later
// invitation replaced reads expired even while its own deadline is still ahead:
// superseded_at is the record that its live slot is gone. The inviter's name
// and username are absent when the invitation has no issuer (a host command) or
// the account is gone; the issuer identifier itself stays in invited_by.
func invitationViewOf(v Invitation, tenantName string, now time.Time, inviter inviterNames) AuthnInviteInvitation {
	return AuthnInviteInvitation{
		ID:                v.ID,
		Tenant:            tenantName,
		Email:             v.Email,
		Status:            v.Status.String(),
		State:             invitationState(v.Status, v.ExpiresAt, v.SupersededAt != nil, now),
		InvitedBy:         string(v.InvitedBy),
		InvitedByName:     inviter.displayName,
		InvitedByUsername: inviter.username,
		Role:              v.Role,
		ExpiresAt:         v.ExpiresAt.UTC(),
		AcceptedAt:        nullableUTC(v.AcceptedAt),
		RevokedAt:         nullableUTC(v.RevokedAt),
		LastSentAt:        v.LastSentAt.UTC(),
		SendCount:         v.SendCount,
		CreatedAt:         v.CreatedAt.UTC(),
	}
}

// invitationViewsOf builds a page's views, resolving each issuer once for the
// whole page. An issuer whose name cannot be read is left unnamed: the page is
// still every invitation the store holds, and the inviter's names are omitted
// rather than the page failing.
func (p *product) invitationViewsOf(ctx context.Context, invitations []Invitation, tenantName string, now time.Time) []AuthnInviteInvitation {
	names := p.inviterNamesOf(ctx, invitations)
	views := make([]AuthnInviteInvitation, len(invitations))
	for i, inv := range invitations {
		views[i] = invitationViewOf(inv, tenantName, now, names[inv.InvitedBy])
	}
	return views
}

// issuedViewOf builds the answer of an issue: the invitation's view, the
// notice's outcome, and the acceptance token only when the notice did not reach
// the address. A sent notice means the recipient holds the link already, so the
// answer must not carry the plaintext token a second time; a failed or
// unconfigured notice is the case where the inviter has to hand it over. The
// token must not be logged.
//
// The invitation has already been written and its notice attempted before this
// answer is built, so an inviter whose name cannot be read is left unnamed
// rather than failing the request: the token in this answer is the only copy of
// the acceptance link, and a resend's old link is already void.
func (p *product) issuedViewOf(ctx context.Context, issued Issued, delivery authn.InviteDelivery, tenantName string) AuthnInviteIssued {
	names := p.inviterNamesOf(ctx, []Invitation{issued.Invitation})
	view := AuthnInviteIssued{
		Invitation: invitationViewOf(
			issued.Invitation, tenantName, p.at(), names[issued.Invitation.InvitedBy]),
		EmailDelivery: delivery.String(),
	}
	if delivery != authn.InviteSent {
		view.Token = issued.Token
	}
	return view
}

// maskedEmail renders an invited address so the person it was sent to can
// recognize it without the link disclosing the whole address to anyone holding
// it. The local part keeps its first character and the domain is kept whole, so
// "alice@example.com" reads "a***@example.com": enough to tell which of several
// addresses received the invitation, not enough to reach it. Masking happens
// here, on the server, so every caller of the link read sees the same form; the
// management face still reads the full address, because it is for the
// administrators who typed it. An address without a domain, which normalization
// never produces, masks to nothing rather than to a guessed shape.
func maskedEmail(email string) string {
	local, domain, found := strings.Cut(email, "@")
	if !found || domain == "" {
		return ""
	}
	if local == "" {
		return "***@" + domain
	}
	return local[:1] + "***@" + domain
}

// pendingLinkOf builds the link a pending invitation shows: the organization's
// name and display name, the inviter's display name, the masked address and the
// deadline. Both names are carried because an organization need not have chosen
// a display name; the client shows the display name and falls back to the name,
// which an organization always has. The issuer's account identifier is not part
// of it.
func pendingLinkOf(inv Invitation, description authn.TenantDescription, inviter string) AuthnInviteLink {
	expires := inv.ExpiresAt.UTC()
	return AuthnInviteLink{
		Tenant:            description.Name,
		TenantDisplayName: description.DisplayName,
		InvitedByName:     inviter,
		Email:             maskedEmail(inv.Email),
		ExpiresAt:         &expires,
		Role:              inv.Role,
	}
}

// acceptedLinkOf builds the link an accepted invitation shows: the
// organization's name and display name, so the client can offer the
// organization the caller belongs to. The address and the inviter are not part
// of it.
func acceptedLinkOf(description authn.TenantDescription) AuthnInviteLink {
	return AuthnInviteLink{
		Tenant:            description.Name,
		TenantDisplayName: description.DisplayName,
	}
}

// acceptViewOf builds the answer of an acceptance: the membership that was
// written and the role that was bound beside it, absent when the invitation
// carried none.
func acceptViewOf(accepted Accepted, tenantName string) AuthnInviteAccept {
	return AuthnInviteAccept{
		Account:   string(accepted.Account),
		Tenant:    tenantName,
		Active:    accepted.Active,
		Source:    accepted.Source,
		SSOExempt: accepted.SSOExempt,
		Role:      accepted.Role,
	}
}
