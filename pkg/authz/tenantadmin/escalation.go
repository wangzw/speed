package tenantadmin

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// errNoIdentity is what an anti-escalation read reports when the request
// carries no identity. The route's guard wrapper refuses such a request before
// any handler runs, so this is unreachable through the mounted surface; a
// handler called directly is answered as an outage rather than granted an empty
// held set.
var errNoIdentity = errors.New("authz.tenantadmin: the request carries no identity")

// The anti-escalation rules, each one a named check that runs before the host
// write guard, the mandatory audit and the manager. They are the tenant
// surface's own: the manager enforces what it enforces for every caller, and
// these narrow it further for a tenant administrator.

// refuseRoleRowWrite refuses a role create, rewrite or delete that names a
// reserved role. platform-admin exists only in the platform domain, and
// tenant-admin is the domain's builtin administrator whose grant set only the
// host's seeding and reconciling surfaces change; neither role row belongs to
// this surface. The check is on the name alone, unconditionally: it does not
// depend on the role existing, on the caller's permissions or on the tenant
// being protected.
func refuseRoleRowWrite(role authz.Role) error {
	switch role {
	case authz.PlatformAdminRole, authz.TenantAdminRole:
		return protected()
	}
	return nil
}

// refuseRoleGrantsWrite refuses a grant write that names a reserved role. It is
// the same protection refuseRoleRowWrite gives the role rows, applied to the
// grants under them: a tenant administrator may not add to or remove from
// either role's grant set.
func refuseRoleGrantsWrite(role authz.Role) error {
	return refuseRoleRowWrite(role)
}

// refuseBindingRole refuses a binding that names the platform administrator
// role. tenant-admin is deliberately allowed: a tenant administrator may bind
// another account as an administrator, subject to the subset, membership and
// last-administrator rules.
func refuseBindingRole(role authz.Role) error {
	if role == authz.PlatformAdminRole {
		return protected()
	}
	return nil
}

// heldPermissions reads the caller's effective permissions in the tenant domain
// through SubjectPermissions. The subject is the request's effective account —
// the one an authorization decision uses — and not the path or body. A read
// that fails is a 503 and is never read as an empty set: an empty set is a
// conclusion, a fault is not.
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
// the tenant. A checker that answers false is a 400; a checker that cannot
// answer is a 503 and is never read as a member.
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
// caller does not hold. The role's grants are read from the manager and mapped
// back to permission names through the catalog; a grant the catalog cannot map
// is one the caller's held set cannot contain, so it refuses too. A read that
// fails is a 503.
//
// The rule is what makes binding tenant-admin allowed but not free: the role
// holds the whole governed set, so only a caller that holds the whole governed
// set — the tenant's own administrator — may bind it.
func (p *product) requireRoleSubset(ctx context.Context, tenant string, role authz.Role, held map[authz.Permission]bool) error {
	grants, err := p.manager.Grants(ctx, tenant, role)
	if err != nil {
		return p.failureOf("reading the role's grants", err)
	}
	for _, grant := range grants {
		permission, ok := p.permissionOf(grant.Resource, grant.Action)
		if !ok || !held[permission] {
			return protected()
		}
	}
	return nil
}

// permissionOf maps a grant's (resource, action) pair back to the permission it
// translates to. The translation is one-to-one across declared permissions, so
// the first declared permission carrying the pair is the only one.
func (p *product) permissionOf(resource, action string) (authz.Permission, bool) {
	for _, permission := range p.catalog.Declared() {
		object, act, ok := p.catalog.ObjectAction(permission)
		if ok && object == resource && act == action {
			return permission, true
		}
	}
	return "", false
}

// removingOwnBinding reports whether the account a tenant-admin binding delete
// names is the caller the request is taken to be from. The effective account is
// the subject an authorization decision uses, and it is the one an impersonated
// request is decided for.
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
