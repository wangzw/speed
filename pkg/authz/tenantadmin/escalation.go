package tenantadmin

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

var errNoIdentity = errors.New("authz.tenantadmin: the request carries no identity")

// The anti-escalation rules run before the host write guard, the mandatory audit and the manager.

func refuseRoleRowWrite(role authz.Role) error {
	switch role {
	case authz.PlatformAdminRole, authz.TenantAdminRole:
		return protected()
	}
	return nil
}

func refuseRoleGrantsWrite(role authz.Role) error {
	return refuseRoleRowWrite(role)
}

func refuseBindingRole(role authz.Role) error {
	if role == authz.PlatformAdminRole {
		return protected()
	}
	return nil
}

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
