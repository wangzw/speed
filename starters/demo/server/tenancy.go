package main

// The host's tenancy policy seams. The tenant capability and the authorization
// capability each leave a decision to the host that writes a tenant's policy:
//
//   - which tenants an operator may not disable or delete (tenant.WriteGuard),
//   - how a tenant is opened together with its first administrator
//     (tenant.Provisioner),
//   - whether an account is an active member of a tenant before a
//     platform-administrator binding is written (authz.MembershipChecker).
//
// The demo host answers all three from one module because they are one policy:
// system and every personal tenant are protected; a tenant opens with a
// tenant-admin bound to its creator; platform administration is confined to
// accounts that belong to system. The module delivers the three capabilities
// and holds no state of its own.

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
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// tenancyModuleName is the name the host registers this module under.
const tenancyModuleName = "host.tenancy"

// init registers the module. It is named on serve, which stands the
// management surface up, and on seed, which writes the platform administrator
// binding that asks the membership checker.
func init() { core.ProcessRegistry.Register(tenancyModule()) }

// tenancyModule delivers the host's three tenancy-policy seams.
//
// The roster, the memberships and the policy manager, seeding surface and
// catalog are required: the membership checker reads the memberships, and the
// provisioner writes a role, its grants and a binding through the policy
// seams. The logger is optional.
func tenancyModule() core.Module {
	return core.Module{
		Name: tenancyModuleName,
		Requires: []core.Requirement{
			{Token: (*tenant.Catalog)(nil)},
			{Token: (*authn.Memberships)(nil)},
			{Token: (*authz.Manager)(nil)},
			{Token: (*authz.Seeder)(nil)},
			{Token: (*authz.Catalog)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authz.MembershipChecker)(nil)},
			{Token: (*tenant.WriteGuard)(nil)},
			{Token: (*tenant.Provisioner)(nil)},
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newTenancyProduct(reg)
		},
	}
}

// tenancyProduct implements the three seams over the capabilities the
// descriptor requires.
type tenancyProduct struct {
	roster      tenant.Catalog
	memberships authn.Memberships
	manager     authz.Manager
	seeder      authz.Seeder
	catalog     authz.Catalog
	logger      *slog.Logger
}

// The three interfaces are one product.
var (
	_ authz.MembershipChecker = (*tenancyProduct)(nil)
	_ tenant.WriteGuard       = (*tenancyProduct)(nil)
	_ tenant.Provisioner      = (*tenancyProduct)(nil)
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
	product := &tenancyProduct{
		roster:      roster,
		memberships: memberships,
		manager:     manager,
		seeder:      seeder,
		catalog:     catalog,
		logger:      log.Default(),
	}
	if delivered, found, takeErr := optionalPersonal[log.Logger](reg, "the logger"); takeErr != nil {
		return nil, takeErr
	} else if found {
		product.logger = delivered.Named(tenancyModuleName)
	}
	return product, nil
}

// IsMember implements authz.MembershipChecker: it reads the account
// membership store, and a subject no account identifier can name is not a
// member rather than an unavailable check.
func (p *tenancyProduct) IsMember(ctx context.Context, tenantID, account string) (bool, error) {
	id, err := authn.ParseAccountID(account)
	if err != nil {
		return false, nil
	}
	return p.memberships.IsMember(ctx, id, tenantID)
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
		strings.HasPrefix(string(id), personalIDPrefix)
}

// Provision implements tenant.Provisioner: it writes the tenant domain's
// tenant-admin role, every declared permission on it, the creator's membership
// and the creator's binding, registers the roster row, and marks the role
// builtin once the row is written so the management surface cannot rewrite or
// delete it. The membership is written with the binding because the creator is
// the tenant's first administrator: a binding without it names an administrator
// who cannot enter the tenant. A refusal of the roster row removes what this
// call wrote, so no tenant is left half-built — except ErrTenantExists, which is
// another replica registering the identifier first: that domain is the winner's,
// and the losing call leaves it untouched. A builtin tenant-admin role found
// before the roster row is the same race seen one step earlier: when the table
// carries the identifier, the call answers ErrTenantExists rather than a
// builtin-role failure.
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
	grants := declaredGrants(p.catalog)
	if _, err := p.seeder.SeedGrants(ctx, tenantID, role, grants); err != nil {
		p.removeProvision(ctx, tenantID, creatorID)
		return fmt.Errorf("%s: granting the declared permissions to the tenant-admin role: %w", tenancyModuleName, err)
	}
	if err := p.memberships.Put(ctx, authn.Membership{
		Account: creatorID,
		Tenant:  tenantID,
		Active:  true,
		Source:  authn.ReasonAdmin,
	}); err != nil {
		p.removeProvision(ctx, tenantID, creatorID)
		return fmt.Errorf("%s: writing the creator's membership in the tenant: %w", tenancyModuleName, err)
	}
	if err := p.manager.PutBinding(ctx, authz.Binding{
		Tenant:    tenantID,
		Account:   creator,
		Role:      role,
		GrantedAt: time.Now().UTC(),
	}); err != nil {
		p.removeProvision(ctx, tenantID, creatorID)
		return fmt.Errorf("%s: binding the creator to the tenant-admin role: %w", tenancyModuleName, err)
	}
	if err := p.roster.Create(ctx, t); err != nil {
		// ErrTenantExists means another replica registered the identifier
		// between this call's read and its write. The domain belongs to the
		// tenant that call opened; removing the rows this call wrote would
		// tear down the winner's role and bindings, so the loser leaves the
		// domain as it stands.
		if !errors.Is(err, tenant.ErrTenantExists) {
			p.removeProvision(ctx, tenantID, creatorID)
		}
		return fmt.Errorf("%s: registering the tenant %q: %w", tenancyModuleName, t.ID, err)
	}
	// The mark freezes the role against the management surface's rewrite and
	// delete paths, as the seed freezes the administrator roles. It comes after
	// the roster row so the role is complete — its grants and the creator's
	// binding are written — before it is frozen, and so a failure before the row
	// can still remove it.
	if err := p.manager.MarkRoleBuiltin(ctx, tenantID, role); err != nil {
		return fmt.Errorf("%s: marking the tenant-admin role builtin: %w", tenancyModuleName, err)
	}
	return nil
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

// removeProvision removes what a failed provision wrote. It is best effort: a
// failure is logged, because the write's own failure is the answer the caller
// receives, and there is no second failure to report beside it.
func (p *tenancyProduct) removeProvision(ctx context.Context, tenantID string, creator authn.AccountID) {
	if err := p.memberships.Remove(ctx, creator, tenantID); err != nil {
		p.logger.Error(tenancyModuleName+": a failed provision left the creator's membership behind",
			"tenant", tenantID, "cause", err.Error())
	}
	if err := p.manager.DeleteRole(ctx, tenantID, authz.TenantAdminRole); err != nil &&
		!errors.Is(err, authz.ErrUnknownRole) {
		p.logger.Error(tenancyModuleName+": a failed provision left its policy rows behind",
			"tenant", tenantID, "cause", err.Error())
	}
}

// declaredGrants is the (resource, action) pair every declared permission the
// process translation carries. A declared permission with no translation row
// is one no route can be held to, so there is nothing to grant for it.
func declaredGrants(catalog authz.Catalog) []authz.Grant {
	declared := catalog.Declared()
	grants := make([]authz.Grant, 0, len(declared))
	for _, permission := range declared {
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
