package personal

// The personal tenant follows its account's lifecycle: the module implements
// authn.AccountLifecycle, so the account module's status change and deletion do
// the tenant-side work after their own transaction has committed.
//
// The in-transaction seam is the authority; these hooks are the retry path and
// the compensation. The two status hooks move the tenant through the roster's
// account-driven transition, which writes the disable source as the account's;
// an operator's disable is therefore never lifted by an account's recovery. The
// deletion removes the tenant's memberships, the deleted account's bindings and
// its tenant-admin role, records the deletion, and then writes the tombstone, in
// that order. Every hook is idempotent, and the tombstone is the completion
// marker: a repeated call finds the row already deleted and writes nothing.

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

var _ authn.AccountLifecycle = (*product)(nil)

// personalMemberPage is the page size the deletion reads a tenant's memberships
// in.
const personalMemberPage = 100

// AccountCreated implements authn.AccountLifecycle: the account gets its
// personal tenant, its membership and its tenant-admin binding as soon as the
// account exists, so no account is ever without a tenant. The seam wrote them
// in the account's transaction; this call completes anything it did not, and
// the backfill route is the third path to the same idempotent ensure.
//
// A service account has no personal tenant, and an account already deleted is
// no work: both answer nil. A retry that arrives after the tenant was deleted
// finds the tombstone and is a no-op.
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

// AccountDisabled implements authn.AccountLifecycle: the tenant the account owns
// is disabled through the roster's account path, so the disable source says
// account state caused it.
func (p *product) AccountDisabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusDisabled)
}

// AccountEnabled implements authn.AccountLifecycle: a disable the account made is
// lifted through the same roster path. A tenant an operator disabled stays
// disabled, because the roster's transition only reverses an account's own
// disable.
func (p *product) AccountEnabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusActive)
}

// setAccountStatus is both status hooks.
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

// AccountDeleted implements authn.AccountLifecycle. The cleanup has a fixed
// order:
//
//  1. every membership of the tenant is removed,
//  2. the deleted account's bindings are cleared and then the tenant-admin role
//     is removed, which takes its remaining bindings with it,
//  3. the deletion's audit row is written,
//  4. the tenant's tombstone is written.
//
// Each step is idempotent, and the tombstone is the completion marker.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	personalID := TenantID(id)
	held, found, err := p.lifecycleTenant(ctx, personalID)
	if err != nil {
		return err
	}
	if !found || held.Status == tenant.StatusDeleted {
		return nil
	}
	tenantID := string(personalID)
	if err := p.clearMemberships(ctx, tenantID); err != nil {
		return err
	}
	if err := p.clearPolicy(ctx, tenantID, string(id)); err != nil {
		return err
	}
	if err := p.auditRequired(ctx, personalAudit(personalDeleteAction, id, personalID, newPersonalActionID())); err != nil {
		return err
	}
	if err := p.roster.Delete(ctx, personalID); err != nil {
		return fmt.Errorf("writing the personal tenant's tombstone: %w", err)
	}
	return nil
}

// ServiceSecretRevoked implements authn.AccountLifecycle. A service account has
// no personal tenant, so there is nothing to revoke.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error {
	return nil
}

// lifecycleTenant reads the personal tenant's row across statuses.
// EnterRegistered forces one table read where this replica's snapshot does not
// carry the row, so a snapshot that predates it cannot turn "a row exists" into
// "no row" and silence a hook.
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

// clearMemberships removes every membership of the tenant, one page at a time.
// The account's own row is already gone with its account in the account module's
// transaction; this step collects the rows an administrator added.
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

// clearPolicy clears the deleted account's bindings in the tenant and then
// removes its tenant-admin role. The bindings go through the host's
// account-deletion path, which the last-tenant-administrator rule does not
// constrain.
func (p *product) clearPolicy(ctx context.Context, tenantID, account string) error {
	if err := p.manager.ClearAccountBindings(ctx, tenantID, account); err != nil {
		return fmt.Errorf("clearing the deleted account's bindings in the personal tenant: %w", err)
	}
	if err := p.manager.DeleteRole(ctx, tenantID, personalTenantAdminRole); err != nil && !errors.Is(err, authz.ErrBuiltinRole) {
		return fmt.Errorf("deleting the personal tenant's tenant-admin role: %w", err)
	}
	return nil
}
