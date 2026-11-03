package main

// The personal tenant follows its account's lifecycle: the host module implements
// authn.AccountLifecycle, so the account module's status change and deletion do
// the tenant-side work after their own transaction has committed.
//
// The two status hooks move the tenant through the roster's account-driven
// transition, which writes the disable source as the account's; an operator's
// disable is therefore never lifted by an account's recovery. The deletion
// removes the tenant's memberships, the deleted account's bindings and its
// tenant-admin role, records the deletion, and then writes the tombstone, in
// that order. Every hook is idempotent, and the tombstone is the completion
// marker: a repeated call finds the row already deleted and writes nothing.
//
// A host module implements the hook only when it is in the run. Without it, an
// account's state change leaves the tenant row and its policy in place, and the
// roster's terminal state has no writer: nothing here is a platform guarantee.

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// personalProduct implements authn.AccountLifecycle. The assertion keeps the
// descriptor's Provides honest: the registry checks the same thing at startup,
// and a method renamed here fails this line before it can fail a run.
var _ authn.AccountLifecycle = (*personalProduct)(nil)

// personalMemberPage is the page size the deletion reads a tenant's memberships
// in. The explicit value keeps the loop's termination independent of whatever
// page size the account module defaults to.
const personalMemberPage = 100

// AccountCreated implements authn.AccountLifecycle: the account gets its
// personal tenant, its membership and its tenant-admin binding as soon as the
// account exists, so no account is ever without a tenant. The route beside this
// module is the backfill for the cases this call fails or is missed; both run
// the same idempotent ensure.
//
// A service account has no personal tenant, and an account already deleted is
// no work: both answer nil. A retry that arrives after the tenant was deleted
// finds the tombstone and is a no-op. The route switch does not apply here in
// the sense of the status hooks: this hook is what opens the space, so a
// process with the policy off builds no personal tenant at all.
func (p *personalProduct) AccountCreated(ctx context.Context, id authn.AccountID) error {
	if !p.config.Enabled {
		return nil
	}
	if p.accounts != nil {
		account, err := p.accounts.Get(ctx, id)
		switch {
		case errors.Is(err, authn.ErrAccountNotFound):
			// The account was deleted before this retry arrived.
			return nil
		case err != nil:
			return fmt.Errorf("reading the account %s at creation: %w", id, err)
		}
		if account.Kind != authn.KindUser || account.Status == authn.StatusDeleted {
			return nil
		}
	}
	personalID := personalTenantID(id)
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
func (p *personalProduct) AccountDisabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusDisabled)
}

// AccountEnabled implements authn.AccountLifecycle: a disable the account made is
// lifted through the same roster path. A tenant an operator disabled stays
// disabled, because the roster's transition only reverses an account's own
// disable.
func (p *personalProduct) AccountEnabled(ctx context.Context, id authn.AccountID) error {
	return p.setAccountStatus(ctx, id, tenant.StatusActive)
}

// setAccountStatus is both status hooks. An account that never had a personal
// tenant has nothing to move, and a tombstone is terminal: both are no work, not
// a failure. A refusal the roster reports for a tombstone is the same race seen
// a moment later and is tolerated.
func (p *personalProduct) setAccountStatus(ctx context.Context, id authn.AccountID, target tenant.Status) error {
	personalID := personalTenantID(id)
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
// Each step is idempotent, and the tombstone is the completion marker: a call
// that finds the tenant already deleted writes nothing. A step that fails leaves
// the earlier steps in place, and the next call finishes the rest; the audit row
// may then appear twice, which at-least-once recording accepts.
func (p *personalProduct) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	personalID := personalTenantID(id)
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
// no personal tenant — the ensure refuses every one — so there is nothing to
// revoke.
func (p *personalProduct) ServiceSecretRevoked(context.Context, authn.AccountID, string) error {
	return nil
}

// lifecycleTenant reads the personal tenant's row across statuses. EnterRegistered
// forces one table read where this replica's snapshot does not carry the row, so
// a snapshot that predates it cannot turn "a row exists" into "no row" and
// silence a hook. The context it returns is not used: the hooks act by the
// tenant's identifier and do not run inside a tenant-scoped request.
//
// An identifier no row carries is found=false, which is the answer the hooks act
// on; every other failure is returned as it stands.
func (p *personalProduct) lifecycleTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
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
// transaction; this step collects the rows an administrator added. The removal
// goes through the account module's account-deletion path rather than the
// explicit removal: the host's membership guard refuses every removal in a
// personal tenant, and the deleted account's winding-up is not an explicit
// removal from a domain that must stay governed.
func (p *personalProduct) clearMemberships(ctx context.Context, tenantID string) error {
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
// constrain: the account's own binding is often the tenant's only one when the
// policy module's AccountDeleted has not run first, and the cleanup must not
// depend on that hook's order. The rule protects a domain still in service; this
// one is about to become a tombstone. The role follows in its own transaction,
// and a role marked builtin cannot be deleted; that refusal is tolerated,
// because no request resolves to the domain again.
func (p *personalProduct) clearPolicy(ctx context.Context, tenantID, account string) error {
	if err := p.manager.ClearAccountBindings(ctx, tenantID, account); err != nil {
		return fmt.Errorf("clearing the deleted account's bindings in the personal tenant: %w", err)
	}
	if err := p.manager.DeleteRole(ctx, tenantID, personalTenantAdminRole); err != nil && !errors.Is(err, authz.ErrBuiltinRole) {
		return fmt.Errorf("deleting the personal tenant's tenant-admin role: %w", err)
	}
	return nil
}
