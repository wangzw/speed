package invite

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// onMembershipChanged revokes the pending invitations a member holds in the
// tenant when that membership is suspended or removed. An invitation is a
// pending grant of a membership, and a member who is no longer a member has
// nothing left to grant: the link addressed to them must stop working then, not
// wait for its deadline.
//
// The event is a hint and the invitation row is the authority: the update is
// conditional on the row still being pending and not yet expired, so it revokes
// no accepted or already revoked invitation and no expired one, and a redelivery
// changes nothing. An expired row grants nothing and keeps its stored pending
// state, so it must keep reading expired rather than become revoked after the
// fact. The account's email addresses are read from the directory; an account
// that a deletion has already erased has no address to match and leaves the rows
// alone, because its own deletion path revokes them through the same event while
// the address is still readable.
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

// The membership changes that withdraw what a non-member may still redeem. The
// names are the payload's closed set, spelled as authn.MembershipChanged writes
// them.
const (
	membershipChangeSuspended = "suspended"
	membershipChangeRemoved   = "removed"
)

// RevokeTenantInvitations implements authn.InvitationRevoker. Deleting an
// organization ends every grant it could still hand out, so every pending,
// unexpired invitation of the tenant is revoked; an accepted, already revoked or
// expired row is untouched, and the update is conditional so a repeated call
// changes nothing. The tenant ownership module calls it once the deletion has
// committed.
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
