package tenantadmin

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// errNoIdentity is what an anti-escalation read reports when the request carries
// no identity.
var errNoIdentity = errors.New("authz.tenantadmin: the request carries no identity")

// The anti-escalation rules run before the host write guard, the mandatory audit
// and the manager.

// refuseRoleRowWrite refuses a role create, rewrite or delete that names a
// reserved role: platform-admin exists only in the platform domain, and
// tenant-admin's grant set only the host's seeding and reconciling surfaces
// change. The check is on the name alone, unconditionally.
func refuseRoleRowWrite(role authz.Role) error {
	switch role {
	case authz.PlatformAdminRole, authz.TenantAdminRole:
		return protected()
	}
	return nil
}

// refuseRoleGrantsWrite refuses a grant write that names a reserved role.
func refuseRoleGrantsWrite(role authz.Role) error {
	return refuseRoleRowWrite(role)
}

// refuseBindingRole refuses a binding that names the platform administrator
// role. tenant-admin is allowed, subject to the subset, membership and
// last-administrator rules.
func refuseBindingRole(role authz.Role) error {
	if role == authz.PlatformAdminRole {
		return protected()
	}
	return nil
}

// heldPermissions reads the caller's effective permissions in the tenant domain.
// A read that fails is a 503, never an empty set.
func (p *product) heldPermissions(ctx context.Context, tenant string) (map[authz.Permission]bool, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, p.unavailable("reading the caller's permissions", errNoIdentity)
	}
	held, err := p.subject.Held(ctx, authz.Domain(tenant), string(id.Account()))
	if err != nil {
		return nil, p.unavailable("reading the caller's permissions", err)
	}
	set := make(map[authz.Permission]bool, len(held))
	for _, permission := range held {
		set[permission] = true
	}
	return set, nil
}

// requireMembership refuses a binding whose account is not an active member of
// the tenant. A checker that cannot answer is a 503, never a member.
func (p *product) requireMembership(ctx context.Context, tenant, account string) error {
	member, err := p.membership.IsMember(ctx, tenant, account)
	if err != nil {
		return p.unavailable("checking the binding account's membership", err)
	}
	if !member {
		return badRequest()
	}
	return nil
}

// requireRoleSubset refuses a binding of a role that holds a permission the
// caller does not hold. A read that fails is a 503; a role outside the held set
// is the protected refusal. It is what makes binding tenant-admin allowed but
// not free: only a caller holding the whole governed set may bind it.
func (p *product) requireRoleSubset(ctx context.Context, tenant string, role authz.Role, held map[authz.Permission]bool) error {
	within, err := p.manager.RoleSubsetHeld(ctx, tenant, role, held)
	if err != nil {
		return p.failureOf("reading the role's grants", err)
	}
	if !within {
		return protected()
	}
	return nil
}

// removingOwnBinding reports whether the account a tenant-admin binding delete
// names is the effective account the request is made for.
func removingOwnBinding(ctx context.Context, role authz.Role, account string) bool {
	if role != authz.TenantAdminRole {
		return false
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return false
	}
	return string(id.Account()) == account
}
