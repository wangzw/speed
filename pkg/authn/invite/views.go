package invite

import (
	"context"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

const (
	viewStatePending  = "pending"
	viewStateExpired  = "expired"
	viewStateAccepted = "accepted"
	viewStateRevoked  = "revoked"
)

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

// The view types are built field by field so a stored field added later does
// not reach a client; only the issue answer carries a token.

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

func (p *product) invitationViewsOf(ctx context.Context, invitations []Invitation, tenantName string, now time.Time) []AuthnInviteInvitation {
	names := p.inviterNamesOf(ctx, invitations)
	views := make([]AuthnInviteInvitation, len(invitations))
	for i, inv := range invitations {
		views[i] = invitationViewOf(inv, tenantName, now, names[inv.InvitedBy])
	}
	return views
}

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

func acceptedLinkOf(description authn.TenantDescription) AuthnInviteLink {
	return AuthnInviteLink{
		Tenant:            description.Name,
		TenantDisplayName: description.DisplayName,
	}
}

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
