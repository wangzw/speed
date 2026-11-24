package invite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func (p *product) refuseSystemTenant(ctx context.Context, tenant string) error {
	description, err := p.tenantDescs.TenantDescriptionOf(ctx, tenant)
	if err != nil {
		return failed("reading the tenant's description", err)
	}
	if description.System {
		return fmt.Errorf("%w", ErrInvitationTargetInvalid)
	}
	return nil
}

// refuseIneligibleAddress refuses an address already in the tenant: any
// membership row counts, whether the member is active or suspended.
func (p *product) refuseIneligibleAddress(ctx context.Context, tenant, email string) error {
	id, err := p.accounts.Lookup(ctx, authn.IdentifierEmail, email)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return nil
	case err != nil:
		return failed("reading the account of the invited address", err)
	}
	account, err := p.accounts.Get(ctx, id)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return nil
	case err != nil:
		return failed("reading the account of the invited address", err)
	}
	if account.Kind != authn.KindUser {
		return fmt.Errorf("%w", ErrInvitationTargetInvalid)
	}
	_, member, err := p.memberships.Membership(ctx, tenant, id)
	if err != nil {
		return failed("reading the invited address's membership", err)
	}
	if member {
		return fmt.Errorf("%w", ErrInvitationMember)
	}
	return nil
}

func (p *product) refusePendingInvitation(ctx context.Context, tenant, email string, now time.Time) error {
	var live int64
	err := p.conn(ctx).Model(&invitationRow{}).
		Where("tenant_id = ? AND email = ? AND status = ? AND expires_at > ?",
			tenant, email, statusPending, now).
		Count(&live).Error
	if err != nil {
		return failed("reading a pending invitation", err)
	}
	if live > 0 {
		return fmt.Errorf("%w", ErrInvitationPending)
	}
	return nil
}
