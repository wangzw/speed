package main

// The in-transaction half of the host's personal-tenant policy: the account
// module's required provisioning seam.
//
// AccountLifecycle runs after the account's transaction has committed, so a
// hook that fails leaves a committed account without the personal tenant it is
// meant to own. ProvisionAccount is the same work inside that transaction: the
// tenant row, the creator's membership, the tenant-admin role and its grants,
// and the binding are written through the *gorm.DB the account module hands in,
// so a failure rolls the account back and a committed account is never left
// half-provisioned. The lifecycle hook still runs afterwards and completes
// anything the seam did not; every step of both is idempotent.
//
// The seam writes nothing for a service account: a non-interactive API client
// is given tenant access by an administrator and owns no organization of its
// own. It also writes nothing while the host's org.personal switch is off,
// which is the same stance AccountCreated takes; the switch is the host's
// policy, and the seam is how that policy reaches the account transaction.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The product is the account provisioning seam the descriptor provides.
var _ provisioning.AccountProvisioning = (*personalProduct)(nil)

// ProvisionAccount writes what a user account owns for the whole of its life,
// inside the account's transaction. It runs after the account row, its
// identifiers and its credential are written and before the creation event is
// staged.
func (p *personalProduct) ProvisionAccount(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	if !p.config.Enabled || facts.Kind != string(authn.KindUser) {
		return nil
	}
	account, err := accountIDOf(facts)
	if err != nil {
		return err
	}
	personalID := personalTenantID(account)
	now := time.Now().UTC()

	// 1. The tenant row. An identifier another call already registered is the
	// idempotent replay of this write, not a failure: the rest of the steps
	// complete the row it left. An alias another tenant holds is this account's
	// username being taken by a business organization, and the account cannot
	// own a personal tenant addressed by it.
	// The host's personal tenant carries its derived identifier as its alias, the
	// same pair ensure writes; the account's username addresses it through the
	// membership, not through the request surface.
	after, err := p.txRoster.CreateTx(ctx, tx, tenant.Tenant{
		ID:           personalID,
		Alias:        string(personalID),
		Status:       tenant.StatusActive,
		OwnerAccount: facts.ID,
		Profile:      tenant.Profile{DisplayName: facts.DisplayName},
	})
	switch {
	case err == nil:
		provisioning.AfterCommitOf(ctx).Add(after)
	case errors.Is(err, tenant.ErrTenantExists):
		// The row is already there; the remaining steps are the replay.
	case errors.Is(err, tenant.ErrAliasConflict):
		return fmt.Errorf("%w: the personal tenant's alias is held by another tenant: %w",
			errIdentifierUnavailable, err)
	default:
		return fmt.Errorf("registering the personal tenant in the account's transaction: %w", err)
	}

	// 2. The creator's membership. The account module's plain Memberships
	// surface would open a transaction of its own; this writes through tx.
	if after, err = p.membershipWriter.PutMembershipTx(ctx, tx, authn.Membership{
		Account: account,
		Tenant:  string(personalID),
		Active:  true,
		Source:  personalMemberSource,
	}); err != nil {
		return fmt.Errorf("writing the personal membership in the account's transaction: %w", err)
	}
	provisioning.AfterCommitOf(ctx).Add(after)

	// 3. The tenant-admin role, its grants and the creator's binding.
	if after, err = p.txManager.PutRoleTx(ctx, tx, authz.RoleRecord{
		Tenant:         string(personalID),
		Role:           personalTenantAdminRole,
		DescriptionKey: personalTenantAdminDescriptionKey,
		CreatedAt:      now,
	}); err != nil {
		return fmt.Errorf("writing the personal tenant's tenant-admin role in the account's transaction: %w", err)
	}
	provisioning.AfterCommitOf(ctx).Add(after)

	_, after, err = p.txManager.SeedGrantsTx(ctx, tx, string(personalID), personalTenantAdminRole,
		p.personalAdminGrants())
	if err != nil {
		return fmt.Errorf("seeding the personal tenant's tenant-admin grants in the account's transaction: %w", err)
	}
	provisioning.AfterCommitOf(ctx).Add(after)

	if after, err = p.txManager.PutBindingTx(ctx, tx, authz.Binding{
		Tenant:    string(personalID),
		Account:   facts.ID,
		Role:      personalTenantAdminRole,
		GrantedAt: now,
	}); err != nil {
		return fmt.Errorf("binding the account to the personal tenant-admin role in the account's transaction: %w", err)
	}
	provisioning.AfterCommitOf(ctx).Add(after)
	return nil
}

// ProvisionAccountDeletion ends what ProvisionAccount wrote, inside the
// transaction that erases the account. The account module has already deleted
// the account's own rows, its membership included; what remains is the policy
// the account held in its personal tenant and the tenant row itself, which is
// written as a tombstone so its alias is never reused.
func (p *personalProduct) ProvisionAccountDeletion(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	if facts.Kind != string(authn.KindUser) {
		return nil
	}
	account, err := accountIDOf(facts)
	if err != nil {
		return err
	}
	personalID := personalTenantID(account)

	if after, clearErr := p.txManager.ClearAccountBindingsTx(ctx, tx, string(personalID), facts.ID); clearErr != nil {
		return fmt.Errorf("clearing the deleted account's bindings in its personal tenant: %w", clearErr)
	} else {
		provisioning.AfterCommitOf(ctx).Add(after)
	}

	// The role is not builtin in a personal tenant — the seed skips that mark
	// there — but an operator may have set it. The tombstone below makes the
	// refusal harmless.
	if after, deleteErr := p.txManager.DeleteRoleTx(ctx, tx, string(personalID), personalTenantAdminRole); deleteErr != nil {
		if !errors.Is(deleteErr, authz.ErrBuiltinRole) {
			return fmt.Errorf("deleting the personal tenant's tenant-admin role: %w", deleteErr)
		}
	} else {
		provisioning.AfterCommitOf(ctx).Add(after)
	}

	// A personal tenant that was never provisioned — the switch was off when the
	// account was created — has no row to delete, and DeleteTx reports the
	// missing identifier as ErrInvalidTenant. The identifier is derived from an
	// account identifier, so it is inside the roster's domain; the only cause
	// here is the absent row, which is no work.
	after, err := p.txRoster.DeleteTx(ctx, tx, personalID)
	switch {
	case err == nil:
		provisioning.AfterCommitOf(ctx).Add(after)
	case errors.Is(err, tenant.ErrInvalidTenant):
		// No row: nothing to tombstone.
	default:
		return fmt.Errorf("writing the personal tenant's tombstone in the account's transaction: %w", err)
	}
	return nil
}

// accountIDOf is the account identifier the facts carry, and the username a
// user account must have for its personal tenant to be addressed by it. A user
// account is created with a username on every path, so an empty one is the
// account module's invariant broken rather than a case to provision around.
func accountIDOf(facts provisioning.AccountFacts) (authn.AccountID, error) {
	if facts.ID == "" {
		return "", fmt.Errorf("%s: the provisioning seam was handed no account identifier", personalModuleName)
	}
	if facts.Username == "" {
		return "", fmt.Errorf("%s: the user account %s has no username to name its personal tenant",
			personalModuleName, facts.ID)
	}
	return authn.AccountID(facts.ID), nil
}

// personalAdminGrants is the tenant-admin role's allowlist as grant rows, the
// same derivation convergeTenantAdminGrants writes: the host-declared business
// resources, kept only where the platform lets a tenant administrator govern
// them, less the management actions a single-person space must not have.
func (p *personalProduct) personalAdminGrants() []authz.Grant {
	permissions := personalTenantAdminPermissions(p.catalog)
	grants := make([]authz.Grant, 0, len(permissions))
	for _, permission := range permissions {
		object, action, ok := p.catalog.ObjectAction(permission)
		if !ok {
			// No translation row means no grant row can express it; the
			// lifecycle hook retries after a module adds it.
			continue
		}
		grants = append(grants, authz.Grant{Resource: object, Action: action})
	}
	return grants
}
