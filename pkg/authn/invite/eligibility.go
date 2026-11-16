package invite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// refuseSystemTenant refuses an invitation into the system tenant. The system
// tenant is the platform's own domain rather than an organization, and nothing
// is invited into it. The judgement comes from the required
// authn.TenantDescriptionResolver, and the refusal is the same one a service
// account earns. A read failure is an unavailable dependency, never a pass.
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

// refuseIneligibleAddress refuses an address that already belongs in the
// tenant: an account that is a member there, active or suspended, is
// ErrInvitationMember, and a service account's address is
// ErrInvitationTargetInvalid. Membership is judged by the read finding a row at
// all, not by its Active flag: a suspended member is still a member and is
// resumed through the membership surface, not through an invitation.
//
// An address no account holds is eligible; an account read that fails is an
// unavailable dependency, never a pass.
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
		// The lookup answered an account the read cannot find. It holds no
		// membership and no kind to judge, so it is treated as no account.
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

// refusePendingInvitation refuses an address that already holds an unexpired
// pending invitation in the tenant. Expiry is read from the clock: only a row
// whose deadline is still ahead holds the live slot, so an expired one leaves
// the address free for a new invitation.
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
