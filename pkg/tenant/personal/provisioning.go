package personal

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

// ErrNameConflict reports a personal tenant whose name another tenant holds.
// It wraps tenant.ErrNameConflict and authn.ErrAccountExists, so a caller may
// match any of the three: the roster's error names the conflict, and the authn
// sentinel lets the account module and the login path tell a taken name from an
// unavailable dependency. The exported sentinel exists so a caller and a test
// can identify this module's refusal without depending on the roster's own
// error text.
var ErrNameConflict = errors.New("personal: the personal tenant's name is held by another tenant")

// ErrUsernameMissing refuses a user account that carries no username. The
// personal tenant's name is always the account's username, so an account
// without one is the write input's invariant broken rather than a case to
// provision around: the seam writes nothing and the account's transaction
// rolls back.
var ErrUsernameMissing = errors.New("personal: the user account has no username")

// The product is the account provisioning seam the descriptor provides.
var _ provisioning.AccountProvisioning = (*product)(nil)

// ProvisionAccount writes what a user account owns for the whole of its life,
// inside the account's transaction. It runs after the account row, its
// identifiers and its credential are written and before the creation event is
// staged.
//
// A service account writes nothing: a non-interactive API client is given
// tenant access by an administrator and owns no organization of its own.
func (p *product) ProvisionAccount(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	if facts.Kind != string(authn.KindUser) {
		return nil
	}
	account, err := accountIDOf(facts)
	if err != nil {
		return err
	}
	personalID := TenantID(account)
	// The name is the account's username: it is how the personal organization
	// is addressed on the request surface, and the username is already unique in
	// the namespace the roster's name column carries. A user account always has
	// a username, so an empty one is refused rather than replaced by a derived
	// name.
	name := facts.Username
	if name == "" {
		return fmt.Errorf("%w: account %q", ErrUsernameMissing, facts.ID)
	}
	now := time.Now().UTC()

	// 1. The tenant row. An identifier another call already registered is the
	// idempotent replay of this write, not a failure: the rest of the steps
	// complete the row it left. A name another tenant holds is the account's
	// username being taken by a business organization, and the account cannot
	// own a personal tenant addressed by it.
	after, err := p.txRoster.CreateTx(ctx, tx, tenant.Tenant{
		ID:           personalID,
		Name:         name,
		Status:       tenant.StatusActive,
		OwnerAccount: facts.ID,
		Profile:      tenant.Profile{DisplayName: facts.DisplayName},
	})
	switch {
	case err == nil:
		provisioning.AfterCommitOf(ctx).Add(after)
		// The creation's audit row is written after the commit, like the
		// deletion's: the tenant row is committed with the account, and the row
		// records that creation rather than gating it. A replay that finds the
		// row already there records nothing.
		provisioning.AfterCommitOf(ctx).Add(func(ctx context.Context) error {
			return p.audit.Record(ctx, personalAudit(personalCreateAction, account, name, newPersonalActionID()))
		})
	case errors.Is(err, tenant.ErrTenantExists):
		// The row is already there; the remaining steps are the replay.
	case errors.Is(err, tenant.ErrNameConflict):
		// The name is the account's username, so a business tenant holding it
		// takes the name in the shared namespace. The error also carries
		// authn.ErrAccountExists: the account module passes that through as a
		// domain refusal rather than an unavailable dependency, and a login can
		// then answer the name as taken instead of failing it as a fault.
		return fmt.Errorf("%w: %w: %w", ErrNameConflict, authn.ErrAccountExists, err)
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
// written as a tombstone so its name is never reused.
func (p *product) ProvisionAccountDeletion(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	if facts.Kind != string(authn.KindUser) {
		return nil
	}
	account, err := accountIDOf(facts)
	if err != nil {
		return err
	}
	personalID := TenantID(account)

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

	// A personal tenant that was never provisioned has no row to delete, and
	// DeleteTx reports the missing identifier as ErrInvalidTenant. The
	// identifier is derived from an account identifier, so it is inside the
	// roster's domain; the only cause here is the absent row, which is no work.
	// The row is read first for the name the audit row names; an absent row
	// leaves the name empty and no audit row is written.
	held, found, readErr := p.txRoster.GetTx(ctx, tx, personalID)
	if readErr != nil {
		return fmt.Errorf("reading the personal tenant for its name: %w", readErr)
	}
	name := ""
	if found {
		name = held.Name
	}
	after, err := p.txRoster.DeleteTx(ctx, tx, personalID)
	switch {
	case err == nil:
		provisioning.AfterCommitOf(ctx).Add(after)
		// The tombstone is committed with the account, so the audit row can no
		// longer be what stops it. It is written after the commit, at least
		// once, like the deletion hook's own row would have been.
		provisioning.AfterCommitOf(ctx).Add(func(ctx context.Context) error {
			return p.audit.Record(ctx, personalAudit(personalDeleteAction, account, name, newPersonalActionID()))
		})
	case errors.Is(err, tenant.ErrInvalidTenant):
		// No row: nothing to tombstone.
	default:
		return fmt.Errorf("writing the personal tenant's tombstone in the account's transaction: %w", err)
	}
	return nil
}

// accountIDOf is the account identifier the facts carry. A user account is
// created with one on every path, so an empty value is the account module's
// invariant broken rather than a case to provision around.
func accountIDOf(facts provisioning.AccountFacts) (authn.AccountID, error) {
	if facts.ID == "" {
		return "", fmt.Errorf("%s: the provisioning seam was handed no account identifier", ModuleName)
	}
	return authn.AccountID(facts.ID), nil
}

// personalAdminGrants is the tenant-admin role's allowlist as grant rows, the
// same derivation convergeTenantAdminGrants writes: the module-declared business
// resources, kept only where the platform lets a tenant administrator govern
// them, less the management actions a single-person space must not have.
func (p *product) personalAdminGrants() []authz.Grant {
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
