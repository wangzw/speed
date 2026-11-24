package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

const tenancyModuleName = "host.tenancy"

const policyWriteGuardModuleName = tenancyModuleName + ".policy-write"

const settingsWriteGuardModuleName = tenancyModuleName + ".settings-write"

const tenantCreatorMembershipSource = "initial"

func init() {
	core.ProcessRegistry.Register(tenancyModule())
	core.ProcessRegistry.Register(policyWriteGuardModule())
	core.ProcessRegistry.Register(settingsWriteGuardModule())
}

// tenancyModule delivers the host's tenancy-policy seams, including the
// per-tenant lock the policy module's binding delete takes.
func tenancyModule() core.Module {
	return core.Module{
		Name: tenancyModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Seeder)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authz.MembershipChecker)(nil)},
			{Token: (*authz.AccountIdentityReader)(nil)},
			{Token: (*tenant.WriteGuard)(nil)},
			{Token: (*tenant.Provisioner)(nil)},
			{Token: (*authn.MembershipGuard)(nil)},
			{Token: (*authz.TenantLock)(nil)},
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newTenancyProduct(reg)
		},
	}
}

type tenancyProduct struct {
	roster      tenant.Catalog
	memberships authn.Memberships
	accounts    authn.Accounts
	manager     authz.Manager
	seeder      authz.Seeder
	catalog     authz.Catalog
	removalLock *tenantRemovalLock
	logger      *slog.Logger
}

var (
	_ authz.MembershipChecker     = (*tenancyProduct)(nil)
	_ authz.AccountIdentityReader = (*tenancyProduct)(nil)
	_ tenant.WriteGuard           = (*tenancyProduct)(nil)
	_ tenant.Provisioner          = (*tenancyProduct)(nil)
	_ authn.MembershipGuard       = (*tenancyProduct)(nil)
	_ authn.SharedMembershipLock  = (*tenancyProduct)(nil)
	_ authz.TenantLock            = (*tenancyProduct)(nil)
)

func newTenancyProduct(reg *core.Registry) (*tenancyProduct, error) {
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the tenant roster: %w", tenancyModuleName, err)
	}
	memberships, err := core.Resolve[authn.Memberships](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the memberships: %w", tenancyModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the accounts: %w", tenancyModuleName, err)
	}
	manager, err := core.Resolve[authz.Manager](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy manager: %w", tenancyModuleName, err)
	}
	seeder, err := core.Resolve[authz.Seeder](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the policy seeding surface: %w", tenancyModuleName, err)
	}
	catalog, err := core.Resolve[authz.Catalog](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the permission catalog: %w", tenancyModuleName, err)
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the database: %w", tenancyModuleName, err)
	}
	product := &tenancyProduct{
		roster:      roster,
		memberships: memberships,
		accounts:    accounts,
		manager:     manager,
		seeder:      seeder,
		catalog:     catalog,
		removalLock: newTenantRemovalLock(database),
		logger:      log.Default(),
	}
	if delivered, found, takeErr := optionalTenancy[log.Logger](reg, "the logger"); takeErr != nil {
		return nil, takeErr
	} else if found {
		product.logger = delivered.Named(tenancyModuleName)
	}
	return product, nil
}

// IsMember implements authz.MembershipChecker: it reads the account membership
// store, and a subject no account identifier can name is not a member. A
// subject of the static credential table is a member: the deployment configured
// that operator itself.
func (p *tenancyProduct) IsMember(ctx context.Context, tenantID, account string) (bool, error) {
	id, err := authn.ParseAccountID(account)
	if err != nil {
		return false, nil
	}
	if id.IsStatic() {
		return true, nil
	}
	return p.memberships.IsMember(ctx, id, tenantID)
}

// Identities implements authz.AccountIdentityReader: the display name and login
// username come from the account store and the activity from the account's
// state in this tenant, where a disabled account or a suspended membership is
// inactive. An account this host cannot resolve is left out of the map rather
// than reported inactive. A static credential subject is active with no display
// name or username.
func (p *tenancyProduct) Identities(ctx context.Context, tenantID string,
	accounts []string,
) (map[string]authz.AccountIdentity, error) {
	identities := make(map[string]authz.AccountIdentity, len(accounts))
	names := make([]string, 0, len(accounts))
	ids := make([]authn.AccountID, 0, len(accounts))
	for _, account := range accounts {
		id, err := authn.ParseAccountID(account)
		if err != nil {
			continue
		}
		if id.IsStatic() {
			identities[account] = authz.AccountIdentity{Active: true}
			continue
		}
		names = append(names, account)
		ids = append(ids, id)
	}
	// One identifier read answers the username of every account on the page.
	var identifiers map[authn.AccountID][]authn.Identifier
	if len(ids) > 0 {
		var err error
		if identifiers, err = p.accounts.IdentifiersMany(ctx, ids); err != nil {
			return nil, fmt.Errorf("%s: reading the login identifiers of the named accounts: %w",
				tenancyModuleName, err)
		}
	}
	for i, id := range ids {
		account := names[i]
		held, err := p.accounts.Get(ctx, id)
		if errors.Is(err, authn.ErrAccountNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: reading the account %q: %w", tenancyModuleName, account, err)
		}
		membership, found, err := p.memberships.Membership(ctx, tenantID, id)
		if err != nil {
			return nil, fmt.Errorf("%s: reading the membership of %q in %q: %w",
				tenancyModuleName, account, tenantID, err)
		}
		identities[account] = authz.AccountIdentity{
			DisplayName: held.DisplayName,
			Username:    usernameOf(identifiers[id]),
			Active:      found && membership.Active && held.Status == authn.StatusActive,
		}
	}
	return identities, nil
}

// usernameOf returns the login username among an account's identifiers; an
// email or phone is never a stand-in for one.
func usernameOf(identifiers []authn.Identifier) string {
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierUsername && identifier.Value != "" {
			return identifier.Value
		}
	}
	return ""
}

// CheckMembershipRemoval implements authn.MembershipGuard: a personal tenant
// refuses every removal, and an ordinary tenant refuses the one that would leave
// it with no tenant-administrator binding. It counts bindings rather than active
// memberships, because a suspended member's binding still exists.
func (p *tenancyProduct) CheckMembershipRemoval(ctx context.Context, id authn.AccountID, tenantID string) error {
	if tenantpersonal.IsPersonalTenantID(tenantID) {
		return fmt.Errorf("%w: the personal tenant %q is a single-member space",
			authn.ErrLastTenantAdmin, tenantID)
	}
	if tenantID == "" {
		return nil
	}
	bindings, err := p.manager.Bindings(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("%s: reading the bindings of %q: %w", tenancyModuleName, tenantID, err)
	}
	admins := 0
	holdsAdmin := false
	for _, binding := range bindings {
		if binding.Role != authz.TenantAdminRole {
			continue
		}
		admins++
		if binding.Account == id.String() {
			holdsAdmin = true
		}
	}
	if holdsAdmin && admins == 1 {
		return fmt.Errorf("%w: account %q is the last %q of tenant %q",
			authn.ErrLastTenantAdmin, id, authz.TenantAdminRole, tenantID)
	}
	return nil
}

// Denied implements tenant.WriteGuard: the system tenant and every personal
// tenant reject both protected writes.
func (p *tenancyProduct) Denied(_ context.Context, id tenant.ID, op tenant.WriteOp) (bool, error) {
	if !protectedTenant(id) {
		return false, nil
	}
	switch op {
	case tenant.WriteDisable, tenant.WriteDelete:
		return true, nil
	default:
		return false, nil
	}
}

func protectedTenant(id tenant.ID) bool {
	return string(id) == string(authz.PlatformDomain) ||
		tenantpersonal.IsPersonalTenantID(string(id))
}

// policyWriteGuardModule delivers authz.WriteGuard; it is a module of its own
// because tenant.WriteGuard's and authz.WriteGuard's Denied share a name.
func policyWriteGuardModule() core.Module {
	return core.Module{
		Name: policyWriteGuardModuleName,
		Provides: []core.Provision{
			{Token: (*authz.WriteGuard)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return policyWriteGuard{}, nil
		},
	}
}

// policyWriteGuard refuses every policy write in a personal-tenant domain.
type policyWriteGuard struct{}

var _ authz.WriteGuard = policyWriteGuard{}

func (policyWriteGuard) Denied(_ context.Context, tenantID string, _ authz.WriteOp) (bool, error) {
	return tenantpersonal.IsPersonalTenantID(tenantID), nil
}

// settingsWriteGuardModule delivers authz.WriteSettings; it is a module of its
// own because authz.WriteSettings and authz.WriteGuard both declare Denied.
func settingsWriteGuardModule() core.Module {
	return core.Module{
		Name: settingsWriteGuardModuleName,
		Provides: []core.Provision{
			{Token: (*authz.WriteSettings)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) {
			return settingsWriteGuard{}, nil
		},
	}
}

// settingsWriteGuard refuses every setting write in a personal-tenant domain.
type settingsWriteGuard struct{}

var _ authz.WriteSettings = settingsWriteGuard{}

func (settingsWriteGuard) Denied(_ context.Context, tenantID string) (bool, error) {
	return tenantpersonal.IsPersonalTenantID(tenantID), nil
}

// Provision implements tenant.Provisioner: it writes the tenant-admin role, its
// governed grants, the roster row, the builtin mark, then the creator's
// membership and binding. A failure before the roster row removes what this call
// wrote; a failure after it leaves the tenant for the seed to reconcile, and a
// lost race on the identifier answers ErrTenantExists and leaves the winner's
// rows in place.
func (p *tenancyProduct) Provision(ctx context.Context, t tenant.Tenant, creator string) error {
	if _, found, err := p.roster.Get(ctx, t.ID); err != nil {
		return fmt.Errorf("%s: reading whether the tenant %q is registered: %w", tenancyModuleName, t.ID, err)
	} else if found {
		return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, t.ID)
	}
	creatorID, err := authn.ParseAccountID(creator)
	if err != nil {
		return fmt.Errorf("%s: the creator %q is not an account identifier: %w", tenancyModuleName, creator, err)
	}
	tenantID := string(t.ID)
	role := authz.Role(authz.TenantAdminRole)
	if err := p.manager.PutRole(ctx, authz.RoleRecord{
		Tenant:         tenantID,
		Role:           role,
		DescriptionKey: tenantAdminDescriptionKey,
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		// A builtin role means another replica won the race and froze it, so the
		// answer is the already-exists refusal.
		if errors.Is(err, authz.ErrBuiltinRole) {
			held, checkErr := p.rosterHolds(ctx, t.ID)
			if checkErr != nil {
				return fmt.Errorf("%s: reading whether the tenant %q is registered: %w", tenancyModuleName, t.ID, checkErr)
			}
			if held {
				return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, t.ID)
			}
		}
		return fmt.Errorf("%s: writing the tenant's tenant-admin role: %w", tenancyModuleName, err)
	}
	grants := governedGrants(p.catalog)
	if _, err := p.seeder.SeedGrants(ctx, tenantID, role, grants); err != nil {
		p.removeProvision(ctx, tenantID)
		return fmt.Errorf("%s: granting the governed permissions to the tenant-admin role: %w", tenancyModuleName, err)
	}
	if err := p.roster.Create(ctx, t); err != nil {
		if !errors.Is(err, tenant.ErrTenantExists) {
			p.removeProvision(ctx, tenantID)
		}
		return fmt.Errorf("%s: registering the tenant %q: %w", tenancyModuleName, t.ID, err)
	}
	// A personal-prefixed identifier's tenant-admin role stays unfrozen, because
	// the host revokes grants outside the allowlist through the manager.
	if !tenantpersonal.IsPersonalTenantID(tenantID) {
		if err := p.markTenantAdminBuiltin(ctx, tenantID, role); err != nil {
			return fmt.Errorf("%s: marking the tenant-admin role builtin: %w", tenancyModuleName, err)
		}
	}
	if err := p.memberships.Put(ctx, authn.Membership{
		Account: creatorID,
		Tenant:  tenantID,
		Active:  true,
		Source:  tenantCreatorMembershipSource,
	}); err != nil {
		return fmt.Errorf("%s: writing the creator's membership in the tenant: %w", tenancyModuleName, err)
	}
	if err := p.manager.PutBinding(ctx, authz.Binding{
		Tenant:    tenantID,
		Account:   creator,
		Role:      role,
		GrantedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("%s: binding the creator to the tenant-admin role: %w", tenancyModuleName, err)
	}
	return nil
}

// markTenantAdminBuiltin freezes the tenant-admin role, retrying once: a single
// failure is often transient, and a second one is returned.
func (p *tenancyProduct) markTenantAdminBuiltin(ctx context.Context, tenantID string, role authz.Role) error {
	first := p.manager.MarkRoleBuiltin(ctx, tenantID, role)
	if first == nil {
		return nil
	}
	second := p.manager.MarkRoleBuiltin(ctx, tenantID, role)
	if second == nil {
		return nil
	}
	return errors.Join(first, second)
}

// rosterHolds reports whether the roster's table carries the identifier, read
// through EnterRegistered: this replica's snapshot may predate another replica's
// row.
func (p *tenancyProduct) rosterHolds(ctx context.Context, id tenant.ID) (bool, error) {
	if _, err := p.roster.EnterRegistered(ctx, id); err != nil {
		if errors.Is(err, tenant.ErrInvalidTenant) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// removeProvision removes the policy rows a provision wrote before the roster
// refused it. It is best effort: a failure is logged because the write's own
// failure is the answer the caller receives.
func (p *tenancyProduct) removeProvision(ctx context.Context, tenantID string) {
	if err := p.manager.DeleteRole(ctx, tenantID, authz.TenantAdminRole); err != nil &&
		!errors.Is(err, authz.ErrUnknownRole) {
		p.logger.Error(tenancyModuleName+": a failed provision left its policy rows behind",
			"tenant", tenantID, "cause", err.Error())
	}
}

// declaredGrants is the grant set of every declared permission the process
// translation carries.
func declaredGrants(catalog authz.Catalog) []authz.Grant {
	return grantsFor(catalog, catalog.Declared())
}

// governedGrants is declaredGrants over the governed set, which every
// tenant-admin role holds; platform staff power comes from platform-admin's full
// declared set instead.
func governedGrants(catalog authz.Catalog) []authz.Grant {
	return grantsFor(catalog, catalog.TenantGoverned())
}

// retiredTenantAdminGrants is the declared permissions that are not governed:
// before the governed split the role held the full declared set, and the
// reconciler removes only what its caller names.
func retiredTenantAdminGrants(catalog authz.Catalog) []authz.Grant {
	governed := make(map[authz.Permission]bool, len(catalog.TenantGoverned()))
	for _, permission := range catalog.TenantGoverned() {
		governed[permission] = true
	}
	retired := make([]authz.Permission, 0)
	for _, permission := range catalog.Declared() {
		if !governed[permission] {
			retired = append(retired, permission)
		}
	}
	return grantsFor(catalog, retired)
}

// grantsFor translates permissions to the (resource, action) pairs the grant
// table holds, in resource then action order; a permission with no translation
// is skipped.
func grantsFor(catalog authz.Catalog, permissions []authz.Permission) []authz.Grant {
	grants := make([]authz.Grant, 0, len(permissions))
	for _, permission := range permissions {
		object, action, ok := catalog.ObjectAction(permission)
		if !ok {
			continue
		}
		grants = append(grants, authz.Grant{Resource: object, Action: action})
	}
	slices.SortFunc(grants, func(a, b authz.Grant) int {
		if a.Resource != b.Resource {
			return strings.Compare(a.Resource, b.Resource)
		}
		return strings.Compare(a.Action, b.Action)
	})
	return grants
}

func optionalTenancy[T any](reg *core.Registry, what string) (T, bool, error) {
	var zero T
	v, err := core.Resolve[T](reg)
	switch {
	case err == nil:
		return v, true, nil
	case errors.Is(err, core.ErrMissingProvider):
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%s: taking up %s: %w", tenancyModuleName, what, err)
	}
}
