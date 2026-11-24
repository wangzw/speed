package personal

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

var _ authn.AccountLifecycle = (*product)(nil)

const personalMemberPage = 100

// AccountCreated completes the account's personal tenant, membership and
// tenant-admin binding; a service account or a deleted account is a no-op.
func (p *product) AccountCreated(ctx context.Context, id authn.AccountID) error {
	if p.accounts != nil {
		account, err := p.accounts.Get(ctx, id)
		switch {
		case errors.Is(err, authn.ErrAccountNotFound):
			return nil
		case err != nil:
			return fmt.Errorf("reading the account %s at creation: %w", id, err)
		}
		if account.Kind != authn.KindUser || account.Status == authn.StatusDeleted {
			return nil
		}
	}
	personalID := TenantID(id)
	held, found, err := p.lifecycleTenant(ctx, personalID)
	if err != nil {
		return err
	}
	if found && held.Status == tenant.StatusDeleted {
		return nil
	}
	if _, err := p.ensure(ctx, id); err != nil {
		return err
	}
	return nil
}

// AccountDisabled disables the tenant the account owns through the roster's account path.
func (p *product) AccountDisabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusDisabled)
}

// AccountEnabled lifts a disable the account made; an operator's disable stays.
func (p *product) AccountEnabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusActive)
}

func (p *product) setAccountStatus(ctx context.Context, id authn.AccountID, target tenant.Status) error {
	personalID := TenantID(id)
	held, found, err := p.lifecycleTenant(ctx, personalID)
	if err != nil {
		return err
	}
	if !found || held.Status == tenant.StatusDeleted {
		return nil
	}
	if err := p.roster.SetStatusByAccount(ctx, personalID, target); err != nil {
		if errors.Is(err, tenant.ErrTenantDeleted) {
			return nil
		}
		return fmt.Errorf("moving the personal tenant to %s: %w", target, err)
	}
	return nil
}

// AccountDeleted winds the tenant down in a fixed order: memberships, policy, audit
// row, tombstone. Each step is idempotent and the tombstone is the completion marker.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	personalID := TenantID(id)
	held, found, err := p.lifecycleTenant(ctx, personalID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if held.Status == tenant.StatusDeleted {
		return p.clearMemberships(ctx, string(personalID))
	}
	tenantID := string(personalID)
	if err := p.clearMemberships(ctx, tenantID); err != nil {
		return err
	}
	if err := p.clearPolicy(ctx, tenantID, string(id)); err != nil {
		return err
	}
	if err := p.auditRequired(ctx, personalAudit(personalDeleteAction, id, held.Name, newPersonalActionID())); err != nil {
		return err
	}
	if err := p.roster.Delete(ctx, personalID); err != nil {
		return fmt.Errorf("writing the personal tenant's tombstone: %w", err)
	}
	return nil
}

// ServiceSecretRevoked is a no-op: a service account has no personal tenant.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error {
	return nil
}

func (p *product) lifecycleTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	if _, err := p.roster.EnterRegistered(ctx, id); err != nil {
		if errors.Is(err, tenant.ErrInvalidTenant) {
			return tenant.Tenant{}, false, nil
		}
		return tenant.Tenant{}, false, fmt.Errorf("reading the personal tenant's row: %w", err)
	}
	held, found, err := p.roster.Get(ctx, id)
	if err != nil {
		return tenant.Tenant{}, false, fmt.Errorf("reading the personal tenant's row: %w", err)
	}
	return held, found, nil
}

func (p *product) clearMemberships(ctx context.Context, tenantID string) error {
	query := authn.MemberQuery{Page: authn.Page{Limit: personalMemberPage}}
	for {
		members, err := p.memberships.Members(ctx, tenantID, query)
		if err != nil {
			return fmt.Errorf("listing the personal tenant's members: %w", err)
		}
		for _, membership := range members {
			if err := p.membershipCleanup.RemoveMembershipForDeletion(ctx, membership.Account, tenantID); err != nil {
				return fmt.Errorf("removing a membership from the personal tenant: %w", err)
			}
		}
		if len(members) < query.EffectiveLimit() {
			return nil
		}
		query.After = members[len(members)-1].Account.String()
	}
}

func (p *product) clearPolicy(ctx context.Context, tenantID, account string) error {
	if err := p.manager.ClearAccountBindings(ctx, tenantID, account); err != nil {
		return fmt.Errorf("clearing the deleted account's bindings in the personal tenant: %w", err)
	}
	if err := p.manager.DeleteRole(ctx, tenantID, personalTenantAdminRole); err != nil && !errors.Is(err, authz.ErrBuiltinRole) {
		return fmt.Errorf("deleting the personal tenant's tenant-admin role: %w", err)
	}
	return nil
}
