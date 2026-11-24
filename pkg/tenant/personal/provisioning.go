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

// ErrNameConflict reports a personal tenant whose name another tenant holds. It wraps
// tenant.ErrNameConflict and authn.ErrAccountExists.
var ErrNameConflict = errors.New("personal: the personal tenant's name is held by another tenant")

// ErrUsernameMissing refuses a user account that carries no username.
var ErrUsernameMissing = errors.New("personal: the user account has no username")

var _ provisioning.AccountProvisioning = (*product)(nil)

// ProvisionAccount writes what a user account owns inside the account's transaction;
// a service account writes nothing.
func (p *product) ProvisionAccount(ctx context.Context, tx *gorm.DB, facts provisioning.AccountFacts) error {
	if facts.Kind != string(authn.KindUser) {
		return nil
	}
	account, err := accountIDOf(facts)
	if err != nil {
		return err
	}
	personalID := TenantID(account)
	name := facts.Username
	if name == "" {
		return fmt.Errorf("%w: account %q", ErrUsernameMissing, facts.ID)
	}
	now := time.Now().UTC()

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
		provisioning.AfterCommitOf(ctx).Add(func(ctx context.Context) error {
			return p.audit.Record(ctx, personalAudit(personalCreateAction, account, name, newPersonalActionID()))
		})
	case errors.Is(err, tenant.ErrTenantExists):
	case errors.Is(err, tenant.ErrNameConflict):
		return fmt.Errorf("%w: %w: %w", ErrNameConflict, authn.ErrAccountExists, err)
	default:
		return fmt.Errorf("registering the personal tenant in the account's transaction: %w", err)
	}

	if after, err = p.membershipWriter.PutMembershipTx(ctx, tx, authn.Membership{
		Account: account,
		Tenant:  string(personalID),
		Active:  true,
		Source:  personalMemberSource,
	}); err != nil {
		return fmt.Errorf("writing the personal membership in the account's transaction: %w", err)
	}
	provisioning.AfterCommitOf(ctx).Add(after)

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

// ProvisionAccountDeletion ends what ProvisionAccount wrote, inside the transaction
// that erases the account.
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

	if after, deleteErr := p.txManager.DeleteRoleTx(ctx, tx, string(personalID), personalTenantAdminRole); deleteErr != nil {
		if !errors.Is(deleteErr, authz.ErrBuiltinRole) {
			return fmt.Errorf("deleting the personal tenant's tenant-admin role: %w", deleteErr)
		}
	} else {
		provisioning.AfterCommitOf(ctx).Add(after)
	}

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
		provisioning.AfterCommitOf(ctx).Add(func(ctx context.Context) error {
			return p.audit.Record(ctx, personalAudit(personalDeleteAction, account, name, newPersonalActionID()))
		})
	case errors.Is(err, tenant.ErrInvalidTenant):
	default:
		return fmt.Errorf("writing the personal tenant's tombstone in the account's transaction: %w", err)
	}
	return nil
}

func accountIDOf(facts provisioning.AccountFacts) (authn.AccountID, error) {
	if facts.ID == "" {
		return "", fmt.Errorf("%s: the provisioning seam was handed no account identifier", ModuleName)
	}
	return authn.AccountID(facts.ID), nil
}

func (p *product) personalAdminGrants() []authz.Grant {
	permissions := personalTenantAdminPermissions(p.catalog)
	grants := make([]authz.Grant, 0, len(permissions))
	for _, permission := range permissions {
		object, action, ok := p.catalog.ObjectAction(permission)
		if !ok {
			continue
		}
		grants = append(grants, authz.Grant{Resource: object, Action: action})
	}
	return grants
}
