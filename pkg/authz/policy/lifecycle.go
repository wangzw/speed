package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	err := p.crossing(ctx).Where("account_id = ?", string(id)).Delete(&bindingRow{}).Error
	if err != nil {
		return fmt.Errorf("authz/policy: clearing the bindings of the deleted account %q: %w", id, err)
	}
	return p.afterWrite(ctx)
}

func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

func (p *product) MembershipRemoved(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant == "" {
		return nil
	}
	return p.ClearAccountBindings(ctx, tenant, id.String())
}

func (p *product) CheckMembershipRemoval(ctx context.Context, id authn.AccountID, tenant string) error {
	if tenant == "" {
		return nil
	}
	rows, err := tenantAdminRows(ctx, p.handle, tenant, false)
	if err != nil {
		return err
	}
	if lastTenantAdminAfterRemoval(rows, id.String()) {
		return fmt.Errorf("%w: account %q is the last %q of tenant %q", authn.ErrLastTenantAdmin,
			id, authz.TenantAdminRole, tenant)
	}
	return nil
}

const membershipChangeRemoved = "removed"

func (p *product) onMembershipChanged(ctx context.Context, evt event.Event) error {
	var payload authn.MembershipChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return nil
	}
	if payload.Change != membershipChangeRemoved || payload.AccountID == "" || evt.TenantID == "" {
		return nil
	}
	return p.ClearAccountBindings(ctx, evt.TenantID, payload.AccountID)
}
