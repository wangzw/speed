package invite

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

func (p *product) onMembershipChanged(ctx context.Context, evt event.Event) error {
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return nil
	}
	switch payload.Change {
	case membershipChangeSuspended, membershipChangeRemoved:
	default:
		return nil
	}
	if payload.AccountID == "" || evt.TenantID == "" {
		return nil
	}
	identifiers, err := p.accounts.Identifiers(ctx, authn.AccountID(payload.AccountID))
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return nil
		}
		return err
	}
	emails := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierEmail {
			emails = append(emails, identifier.Value)
		}
	}
	if len(emails) == 0 {
		return nil
	}
	now := p.at()
	return p.conn(ctx).Model(&invitationRow{}).
		Where("tenant_id = ? AND email IN ? AND status = ? AND expires_at > ?",
			evt.TenantID, emails, statusPending, now).
		Updates(map[string]any{"status": statusRevoked, "revoked_at": now, "updated_at": now}).Error
}

const (
	membershipChangeSuspended = "suspended"
	membershipChangeRemoved   = "removed"
)

// RevokeTenantInvitations revokes every pending, unexpired invitation of the
// tenant. An accepted, revoked or expired row is untouched, and a repeated call
// changes nothing.
func (p *product) RevokeTenantInvitations(ctx context.Context, tenant string) error {
	if tenant == "" {
		return nil
	}
	now := p.at()
	err := p.conn(ctx).Model(&invitationRow{}).
		Where("tenant_id = ? AND status = ? AND expires_at > ?", tenant, statusPending, now).
		Updates(map[string]any{"status": statusRevoked, "revoked_at": now, "updated_at": now}).Error
	if err != nil {
		return failed("revoking a tenant's pending invitations", err)
	}
	return nil
}
