package main

// The host's tenancy policy seams. The tenant capability and the authorization
// capability each leave a decision to the host that writes a tenant's policy:
//
//   - which tenants an operator may not disable or delete (tenant.WriteGuard),
//   - how a tenant is opened together with its first administrator
//     (tenant.Provisioner),
//   - whether an account is an active member of a tenant before a
//     platform-administrator binding is written (authz.MembershipChecker),
//   - what a role-usage read should show for an account: its display name and
//     whether it is still active (authz.AccountIdentityReader),
//   - which removals of a membership a tenant refuses (authn.MembershipGuard),
//   - the per-tenant lock that judges a tenant's administrator floor
//     (authz.TenantLock),
//   - which domains the management surface may not change (authz.WriteGuard),
//   - which domains' settings may not be changed through the tenant surface
//     (authz.WriteSettings).
//
// The demo host answers the first six from one module because they are one
// policy: system and every personal tenant are protected; a tenant opens with a
// tenant-admin bound to its creator; platform administration is confined to
// accounts that belong to system; a personal tenant refuses every membership
// removal while an ordinary tenant keeps at least one tenant-admin; and one
// per-tenant lock carries the floor both of those guards judge. The identity
// answer is the account store and the membership store read together, because
// neither alone answers both halves of what a role-usage row shows. The last two
// are modules of their own: tenant.WriteGuard, authz.WriteGuard and
// authz.WriteSettings each name their method Denied, and one Go type cannot
// carry two methods named Denied. The tenancy product holds one piece of state,
// the removal lock, because the policy module's binding delete and both guards
// must wait on the same object.

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

// tenancyModuleName is the name the host registers this module under.
const tenancyModuleName = "host.tenancy"

// policyWriteGuardModuleName is the name of the module that delivers the
// management surface's write guard. It is a module of its own because a Go type
// cannot carry two methods named Denied, and tenant.WriteGuard and
// authz.WriteGuard each declare one.
const policyWriteGuardModuleName = tenancyModuleName + ".policy-write"

// settingsWriteGuardModuleName is the name of the module that delivers the
// settings surface's write guard. It is a module of its own for the same reason:
// authz.WriteSettings and authz.WriteGuard each declare a Denied method, and one
// Go type cannot carry both.
const settingsWriteGuardModuleName = tenancyModuleName + ".settings-write"

// tenantCreatorMembershipSource is the membership source every tenant creation
// writes for its creator. The creator is the tenant's initial user, so the
// source says so rather than reading as a later administrator's addition; the
// product reserves "admin" for the additions made after the tenant exists, and
// "personal" for the personal tenant's own creation.
const tenantCreatorMembershipSource = "initial"

// init registers the three modules. The tenancy module is named on serve, which
// stands the management surface up, and on seed, which writes the platform
// administrator binding that asks the membership checker. The two write-guard
// modules are named on serve, where the management surfaces ask them.
func init() {
	core.ProcessRegistry.Register(tenancyModule())
	core.ProcessRegistry.Register(policyWriteGuardModule())
	core.ProcessRegistry.Register(settingsWriteGuardModule())
}

// tenancyModule delivers the host's tenancy-policy seams, including the
// per-tenant lock the policy module's binding delete takes.
//
// The roster, the memberships and the policy manager, seeding surface and
// catalog are required: the membership checker reads the memberships, the
// provisioner writes a role, its grants and a binding through the policy
// seams, and the membership guard reads the bindings through the manager. The
// logger is optional.
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
			// The one lock every path that judges a tenant's administrator
			// floor takes: the policy module's binding delete and both the
			// policy module's and this module's membership guard. The policy
			// module takes it up optionally and otherwise uses its in-process
			// default, so a host that names no lock still serialises a single
			// instance.
			{Token: (*authz.TenantLock)(nil)},
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newTenancyProduct(reg)
		},
	}
}

// tenancyProduct implements the host's seams over the capabilities the
// descriptor requires.
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

// The interfaces are one product.
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

// IsMember implements authz.MembershipChecker: it reads the account
// membership store, and a subject no account identifier can name is not a
// member rather than an unavailable check.
//
// A subject of the static credential table is the one exception: it is an
// operator the deployment configured in its own table, not an account that
// registered itself, so it holds no membership row and the answer is true. The
// deployment is the party that decides which static principals exist, and it is
// the same party that writes the platform-administrator binding the check
// guards.
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

// Identities implements authz.AccountIdentityReader: the display name comes
// from the account store and the activity from the account's state in this
// tenant. An account is active only while its status is active and its
// membership is in force, because a disabled account or a suspended membership
// is not a member a role-usage count should show as present.
//
// An account string this host cannot resolve — one that is not an account
// identifier, one naming no live account, or a deleted account's tombstone — is
// left out of the map rather than answered with an inactive row: the caller
// shows an unavailable identity instead of claiming the account was disabled.
// A store that cannot be read is an error, which the caller treats as a 503.
//
// A subject of the static credential table is the one exception: the deployment
// configured that operator in its own table, so it is active with no account
// row and therefore no display name. This mirrors IsMember's answer for the
// same principal.
func (p *tenancyProduct) Identities(ctx context.Context, tenantID string,
	accounts []string,
) (map[string]authz.AccountIdentity, error) {
	identities := make(map[string]authz.AccountIdentity, len(accounts))
	for _, account := range accounts {
		id, err := authn.ParseAccountID(account)
		if err != nil {
			continue
		}
		if id.IsStatic() {
			identities[account] = authz.AccountIdentity{Active: true}
			continue
		}
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
			Active:      found && membership.Active && held.Status == authn.StatusActive,
		}
	}
	return identities, nil
}

// CheckMembershipRemoval implements authn.MembershipGuard. A personal tenant is
// a single-member space, so every removal of a membership in it is refused; an
// ordinary tenant refuses the removal that would leave it with no
// tenant-administrator binding, because nobody could govern it afterwards.
//
// The answer reads the bindings through the policy manager, which is the same
// data DeleteBinding's own floor reads. It counts bindings rather than active
// memberships: the rule is that a domain keeps an administrator binding, and a
// suspended member's binding still exists.
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

// Denied implements tenant.WriteGuard. The system tenant and every personal
// tenant reject both protected writes; every other tenant accepts them. The
// answer never fails: the two classes are read from the identifier alone.
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

// protectedTenant reports whether a tenant is one an operator may not disable
// or delete: the system tenant, and a personal tenant, which the host
// recognises by the prefix of the identifier it derives.
func protectedTenant(id tenant.ID) bool {
	return string(id) == string(authz.PlatformDomain) ||
		tenantpersonal.IsPersonalTenantID(string(id))
}

// policyWriteGuardModule delivers authz.WriteGuard. It is a module of its own
// because tenant.WriteGuard's Denied and authz.WriteGuard's Denied share a
// name, and one Go type cannot implement both.
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

// policyWriteGuard implements authz.WriteGuard: the management surface may not
// add, rewrite or remove a role, a grant or a binding in a domain whose
// identifier starts with the personal prefix. That domain's policy belongs to
// the host that built the space, and the tenant's own administrator holds an
// allowlist that excludes the management permissions, so nobody reaches the
// route for a legitimate reason.
//
// The answer reads the identifier alone and never fails.
type policyWriteGuard struct{}

var _ authz.WriteGuard = policyWriteGuard{}

// Denied implements authz.WriteGuard.
func (policyWriteGuard) Denied(_ context.Context, tenantID string, _ authz.WriteOp) (bool, error) {
	return tenantpersonal.IsPersonalTenantID(tenantID), nil
}

// settingsWriteGuardModule delivers authz.WriteSettings. It is a module of its
// own because authz.WriteSettings and authz.WriteGuard both declare a Denied
// method, and one Go type cannot implement both.
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

// settingsWriteGuard implements authz.WriteSettings: the tenant management
// surface may not write the settings of a domain whose identifier starts with
// the personal prefix. The personal domain's settings belong to the host that
// built the space, and its tenant administrator governs a business tenant
// rather than the personal organization.
//
// The review build adds one refusal of its own through
// settingsWriteGuardReviewDenial: the review fixture's read-only business
// tenant, whose absence from a default build is what keeps the review fixture
// out of the production policy.
//
// The answer reads the identifier alone and never fails. The platform-only
// namespaces are a separate rule the settings module holds itself, so this seam
// refuses no other domain. The other management surfaces do not know the review
// fixture's prefix, so such a tenant still opens organization administration;
// only its settings writes are refused.
type settingsWriteGuard struct{}

var _ authz.WriteSettings = settingsWriteGuard{}

// Denied implements authz.WriteSettings.
func (settingsWriteGuard) Denied(_ context.Context, tenantID string) (bool, error) {
	return tenantpersonal.IsPersonalTenantID(tenantID) || settingsWriteGuardReviewDenial(tenantID), nil
}

// Provision implements tenant.Provisioner: it writes the tenant domain's
// tenant-admin role, the governed permissions on it, the roster row, marks the
// role builtin, and then writes the creator's membership and the creator's
// binding. The roster row precedes the administrator rows so a failure after it
// leaves a tenant the seed can repair, and the builtin mark precedes them so a
// failure at the mark leaves a tenant whose role is not yet frozen rather than a
// half-written administration.
//
// A failure before the roster row removes what this call wrote. A failure after
// it leaves the tenant in place and returns the error: the caller answers 503,
// never success. The seed reconciles every non-personal tenant in the roster —
// its tenant-admin role, grants and builtin mark — on every run, and reconciles
// each administrator's membership and binding for the tenants it declares.
//
// A refusal of the roster row for ErrTenantExists is another replica winning the
// identifier. The creator's membership and binding follow the roster row, so the
// losing call has written neither; it leaves the winner's role, grants and
// administrator rows in place and answers the already-exists refusal.
//
// A tenant the roster already holds is refused before any policy write: the
// identifier's domain belongs to that tenant, and writing this call's role and
// binding into it would change the policy of a tenant this call did not open.
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
		// A builtin tenant-admin role means this domain was provisioned before.
		// When the roster's table carries the identifier, another replica won
		// the race and froze the role; the answer is the already-exists refusal,
		// not a builtin-role failure.
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
		// ErrTenantExists means another replica registered the identifier
		// between this call's read and its write. The domain belongs to the
		// tenant that call opened; the creator's membership and binding follow
		// the roster row, so this call has written neither and leaves the
		// winner's role, grants and administrator rows in place.
		if !errors.Is(err, tenant.ErrTenantExists) {
			p.removeProvision(ctx, tenantID)
		}
		return fmt.Errorf("%s: registering the tenant %q: %w", tenancyModuleName, t.ID, err)
	}
	// The mark freezes the role against the management surface's rewrite and
	// delete paths, as the seed freezes the administrator roles. It comes after
	// the roster row so the tenant is registered before it is frozen, and before
	// the membership and binding so a failure here leaves a tenant with an
	// unfrozen role and no administrator rows for the seed to reconcile.
	//
	// A personal-prefixed identifier is not a business tenant: the host treats it
	// as a personal domain in every other seam, and the personal tenant's
	// tenant-admin is not builtin because the host revokes grants outside the
	// allowlist through the manager. Leave that role unfrozen for the same reason.
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

// markTenantAdminBuiltin freezes the tenant-admin role, retrying once. A single
// failure is often a transient store error; a second failure leaves the tenant
// with a non-builtin role and returns the error, so the caller answers 503
// instead of reporting a tenant it did not finish.
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

// rosterHolds reports whether the roster's table carries the identifier. It
// reads through EnterRegistered, which falls back to the table when this
// replica's snapshot predates another replica's row: the snapshot Get at the top
// of Provision is exactly what a racing creation may have missed.
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
// refused it. The membership and binding are written after the roster row, so at
// this point only the role and its grants are there. It is best effort: a
// failure is logged, because the write's own failure is the answer the caller
// receives, and there is no second failure to report beside it.
func (p *tenancyProduct) removeProvision(ctx context.Context, tenantID string) {
	if err := p.manager.DeleteRole(ctx, tenantID, authz.TenantAdminRole); err != nil &&
		!errors.Is(err, authz.ErrUnknownRole) {
		p.logger.Error(tenancyModuleName+": a failed provision left its policy rows behind",
			"tenant", tenantID, "cause", err.Error())
	}
}

// declaredGrants is the (resource, action) pair every declared permission the
// process translation carries. A declared permission with no translation row
// is one no route can be held to, so there is nothing to grant for it. The
// platform administrator's grant set is this one.
func declaredGrants(catalog authz.Catalog) []authz.Grant {
	return grantsFor(catalog, catalog.Declared())
}

// governedGrants is declaredGrants over the governed set: the permissions a
// tenant administrator may hold. Every tenant's tenant-admin role holds this
// set, the system tenant's included; platform staff power comes from
// platform-admin's full declared set instead.
func governedGrants(catalog authz.Catalog) []authz.Grant {
	return grantsFor(catalog, catalog.TenantGoverned())
}

// retiredTenantAdminGrants is the set this version withdraws from every
// tenant-admin role: the declared permissions that are not governed. Before the
// governed split, the role was seeded with the full declared set, so those
// platform-only permissions are the ones the convergence removes on an existing
// database. The list is explicit here rather than inferred by the reconciler,
// which removes only what its caller names.
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
// table holds, in resource then action order. A permission the process
// translation has no row for is one no route can be held to, so nothing is
// granted for it.
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

// optionalTenancy takes up a capability whose absence is a legal assembly.
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
