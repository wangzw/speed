package personal

// The idempotent completion of a personal tenant. The account-creation
// transaction is the guarantee that the tenant exists; the lifecycle hook runs
// this after the transaction commits to complete anything the seam left
// unfinished. It never creates a tenant for an account whose username cannot be
// read: the alias is always the account's username.
//
// The ensure writes the same five pieces the transaction seam writes, through
// the context-taking surfaces, each idempotent. A membership row that exists is
// left as it stands, and a tenant row that is not enabled is refused before
// anything is written.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// personalResult is the ensure's outcome.
type personalResult struct {
	Tenant  tenant.Tenant
	Created bool
}

// auditRequired writes the mandatory row of an action.
func (p *product) auditRequired(ctx context.Context, e audit.Entry) error {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			"action", e.Action, "tenant", e.Object, "cause", err.Error())
		return fmt.Errorf("writing the %s audit row: %w", e.Action, err)
	}
	return nil
}

// auditBestEffort writes the failed row that closes a failed attempt.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": a failed-action audit row could not be written",
			"action", e.Action, "tenant", e.Object, "cause", err.Error())
	}
}

// ensure checks and completes the five pieces of a personal tenant:
//
//  1. the tenant row (created, or read back when it already exists),
//  2. the account's membership in it,
//  3. the non-builtin tenant-admin role,
//  4. the tenant-admin role's grants, converged to the allowlist,
//  5. the account's binding to that role.
//
// Each step is idempotent, and a failure leaves the earlier steps in place for
// the next call to complete. A membership row that exists is left as it stands,
// and a tenant row that is not enabled is answered with a refusal before
// anything is written.
func (p *product) ensure(ctx context.Context, account authn.AccountID) (personalResult, error) {
	personalID := TenantID(account)
	if !tenant.IsIdentifier(string(personalID)) {
		return personalResult{}, fmt.Errorf("%w: the derived personal tenant identifier is outside "+
			"the roster's domain", ErrIdentifierUnavailable)
	}

	_, own, err := p.memberships.Membership(ctx, string(personalID), account)
	if err != nil {
		return personalResult{}, fmt.Errorf("reading the account's personal membership: %w", err)
	}

	var record tenant.Tenant
	created := false
	actionID := ""
	createBegan := false
	createRefused := false
	if own {
		record, err = p.registeredTenant(ctx, personalID)
	} else {
		// The mandatory row lands before the create: an audit module that cannot
		// take it stops the registration, so no tenant is ever created without the
		// record of it.
		actionID = newPersonalActionID()
		createBegan = true
		if auditErr := p.auditRequired(ctx, personalAudit(personalCreateAction, account, personalID, actionID)); auditErr != nil {
			return personalResult{}, auditErr
		}
		alias, aliasErr := p.usernameOf(ctx, account)
		if aliasErr != nil {
			if createBegan {
				p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
			}
			return personalResult{}, aliasErr
		}
		record, created, createRefused, err = p.createTenant(ctx, personalID, alias)
	}
	if err != nil {
		if createBegan {
			p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
		}
		return personalResult{}, err
	}
	// fail closes whatever is left of a create attempt with the failed row that
	// links to its permitted one. A read-back attempt has no permitted row to
	// close.
	fail := func(err error) (personalResult, error) {
		if createBegan {
			p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
		}
		return personalResult{}, err
	}
	if record.Status != tenant.StatusActive {
		// A disabled tenant is the operator's decision. Nothing below runs.
		return fail(ErrTenantUnavailable)
	}

	if !own {
		if err = p.memberships.Put(ctx, authn.Membership{
			Account: account,
			Tenant:  string(personalID),
			Active:  true,
			Source:  personalMemberSource,
		}); err != nil {
			return fail(fmt.Errorf("writing the membership: %w", err))
		}
	}

	roles, err := p.manager.Roles(ctx, string(personalID))
	if err != nil {
		return fail(fmt.Errorf("reading the personal tenant's roles: %w", err))
	}
	adminPresent := false
	for _, role := range roles {
		if role.Role == personalTenantAdminRole {
			adminPresent = true
			break
		}
	}
	if !adminPresent {
		if err = p.manager.PutRole(ctx, authz.RoleRecord{
			Tenant:         string(personalID),
			Role:           personalTenantAdminRole,
			DescriptionKey: personalTenantAdminDescriptionKey,
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			return fail(fmt.Errorf("writing the tenant-admin role: %w", err))
		}
	}

	if err = p.convergeTenantAdminGrants(ctx, string(personalID)); err != nil {
		return fail(err)
	}

	bindings, err := p.manager.BindingsOf(ctx, string(personalID), account.String())
	if err != nil {
		return fail(fmt.Errorf("reading the account's bindings: %w", err))
	}
	bound := false
	for _, binding := range bindings {
		if binding.Role == personalTenantAdminRole {
			bound = true
			break
		}
	}
	if !bound {
		if err := p.manager.PutBinding(ctx, authz.Binding{
			Tenant:    string(personalID),
			Account:   account.String(),
			Role:      personalTenantAdminRole,
			GrantedAt: time.Now().UTC(),
		}); err != nil {
			return fail(fmt.Errorf("binding the account to the tenant-admin role: %w", err))
		}
	}

	// A tenant built under the old model still carries its owner role. The
	// tenant-admin role and the account's binding are in place above, so removing
	// the old role finishes the migration.
	if err := p.manager.DeleteRole(ctx, string(personalID), legacyPersonalOwnerRole); err != nil &&
		!errors.Is(err, authz.ErrBuiltinRole) {
		return fail(fmt.Errorf("removing the personal tenant's legacy owner role: %w", err))
	}

	if createRefused {
		// Another call won the race the deterministic identifier creates: this
		// attempt's create was refused, and the row it read back is the answer.
		p.auditBestEffort(ctx, personalFailedAudit(personalCreateAction, personalID, actionID))
	}
	return personalResult{Tenant: record, Created: created}, nil
}

// usernameOf reads the account's username, which names its personal tenant's
// alias. An assembly without the account read surface, an account the store no
// longer holds and an account with no username identifier are all refusals: the
// alias is always the username, never a derived identifier.
func (p *product) usernameOf(ctx context.Context, account authn.AccountID) (string, error) {
	if p.accounts == nil {
		return "", fmt.Errorf("%w: the assembly has no account read surface", ErrUsernameMissing)
	}
	identifiers, err := p.accounts.Identifiers(ctx, account)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return "", fmt.Errorf("%w: account %q", ErrUsernameMissing, account)
		}
		return "", fmt.Errorf("reading the account's username: %w", err)
	}
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierUsername && identifier.Value != "" {
			return identifier.Value, nil
		}
	}
	return "", fmt.Errorf("%w: account %q", ErrUsernameMissing, account)
}

// registeredTenant reads the personal tenant's row back.
func (p *product) registeredTenant(ctx context.Context, id tenant.ID) (tenant.Tenant, error) {
	if _, enterErr := p.roster.EnterRegistered(ctx, id); enterErr != nil {
		return tenant.Tenant{}, fmt.Errorf("catching the roster's snapshot up with the personal "+
			"tenant: %w", enterErr)
	}
	held, found, err := p.roster.Get(ctx, id)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("reading the personal tenant back: %w", err)
	}
	if !found {
		return tenant.Tenant{}, fmt.Errorf("the roster's snapshot still does not carry the "+
			"personal tenant %q after catching up", id)
	}
	return held, nil
}

// createTenant registers the personal tenant. A row the roster's table already
// holds is read back instead. The alias is the account's username.
func (p *product) createTenant(ctx context.Context, id tenant.ID, alias string) (tenant.Tenant, bool, bool, error) {
	record := tenant.Tenant{ID: id, Alias: alias, Status: tenant.StatusActive}
	err := p.roster.Create(ctx, record)
	switch {
	case err == nil:
		return record, true, false, nil
	case errors.Is(err, tenant.ErrTenantExists):
		held, readErr := p.registeredTenant(ctx, id)
		return held, false, true, readErr
	case errors.Is(err, tenant.ErrAliasConflict):
		return tenant.Tenant{}, false, true, fmt.Errorf("%w: %w", ErrIdentifierUnavailable, err)
	default:
		return tenant.Tenant{}, false, true, fmt.Errorf("registering the personal tenant: %w", err)
	}
}

// convergeTenantAdminGrants brings the tenant-admin role's grants to the
// allowlist: a grant outside it is revoked, and a missing one is added.
func (p *product) convergeTenantAdminGrants(ctx context.Context, tenantID string) error {
	held, err := p.manager.Grants(ctx, tenantID, personalTenantAdminRole)
	if err != nil {
		return fmt.Errorf("reading the tenant-admin role's grants: %w", err)
	}
	allowed := make(map[authz.Grant]struct{}, len(personalTenantAdminPermissions(p.catalog)))
	grants := make([]authz.Grant, 0, len(personalTenantAdminPermissions(p.catalog)))
	for _, permission := range personalTenantAdminPermissions(p.catalog) {
		object, action, ok := p.catalog.ObjectAction(permission)
		if !ok {
			continue
		}
		grant := authz.Grant{Resource: object, Action: action}
		allowed[grant] = struct{}{}
		grants = append(grants, grant)
	}
	for _, grant := range held {
		if _, keep := allowed[authz.Grant{Resource: grant.Resource, Action: grant.Action}]; keep {
			continue
		}
		if err := p.manager.DeleteGrant(ctx, authz.GrantRecord{
			Tenant:   tenantID,
			Role:     personalTenantAdminRole,
			Resource: grant.Resource,
			Action:   grant.Action,
		}); err != nil {
			return fmt.Errorf("revoking a grant outside the tenant-admin role's allowlist: %w", err)
		}
	}
	if _, err := p.seeder.SeedGrants(ctx, tenantID, personalTenantAdminRole, grants); err != nil {
		return fmt.Errorf("seeding the tenant-admin role's grants: %w", err)
	}
	return nil
}

// personalAudit builds the mandatory row of a tenant-side write: the tenant's
// identifier is the object, and the detail names the action's two ends and the
// identifier that links the rows of one attempt.
func personalAudit(action string, account authn.AccountID, id tenant.ID, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  string(id),
		Outcome: personalOutcomeSuccess,
		Detail: map[string]string{
			personalDetailActionID: actionID,
			personalDetailAccount:  account.String(),
			personalDetailTenant:   string(id),
		},
	}
}

// personalFailedAudit builds the row that closes a failed attempt.
func personalFailedAudit(action string, id tenant.ID, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  string(id),
		Outcome: personalOutcomeFailed,
		Detail:  map[string]string{personalDetailActionID: actionID},
	}
}

// newPersonalActionID mints the identifier that links the audit rows of one
// attempt.
func newPersonalActionID() string { return uuid.NewString() }
