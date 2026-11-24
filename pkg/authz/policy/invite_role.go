package policy

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

// Grantable implements authn.InviteRolePolicy: it answers whether account may
// grant role in tenant, with the rules a binding write applies. The platform
// administrator may not be placed in a tenant domain; the caller must hold the
// tenant's role-management permission, and every permission the role carries
// must be within the caller's held set; the host's write guard must admit the
// binding.
//
// The permission is judged before the role's existence, so the answers cannot
// reveal which role names a tenant has. An empty account is a host command and
// skips the held-set comparison. A held set that cannot be read is an error,
// never an empty set.
func (p *product) Grantable(ctx context.Context, tenant, account, role string) error {
	if role == "" {
		return nil
	}
	if err := requireTenant(tenant); err != nil {
		return err
	}
	if authz.Role(role) == authz.PlatformAdminRole {
		return fmt.Errorf("%w: the role %q exists only in the platform domain", authn.ErrRoleProtected, role)
	}
	if account == "" {
		if err := p.requireRoleExists(ctx, tenant, role); err != nil {
			return err
		}
		return p.requireWriteGuard(ctx, tenant)
	}
	// An invitation that an account issues has to be acceptable, and acceptance
	// replays the inviter's standing through the membership checker. Without it
	// the invitation could be issued and never accepted, so the issue itself
	// fails closed.
	if p.membership == nil {
		return fmt.Errorf("authz/policy: judging an account's standing in tenant %q needs the "+
			"membership checker", tenant)
	}
	held, err := p.heldSet(ctx, tenant, account)
	if err != nil {
		return err
	}
	if !held[authz.PermissionTenantRoleManage] {
		return fmt.Errorf("%w: the account holds no %s in tenant %q",
			authn.ErrForbidden, authz.PermissionTenantRoleManage, tenant)
	}
	if err := p.requireRoleExists(ctx, tenant, role); err != nil {
		return err
	}
	if err := p.requireGrantSubset(ctx, tenant, role, held); err != nil {
		return err
	}
	return p.requireWriteGuard(ctx, tenant)
}

// StillGrantable implements authn.InviteRolePolicy: it replays the issue-time
// judgement against the inviter, because an invitation can outlive the standing
// it was issued under. The inviter must still be an active member, still hold
// the role-management permission and every permission the role carries, and the
// role must still exist and not be the platform administrator.
//
// An empty inviter checks only that the role exists. A judgement that cannot be
// read is an error, never a refusal.
func (p *product) StillGrantable(ctx context.Context, tenant, role, inviter string) error {
	if role == "" {
		return nil
	}
	if err := requireTenant(tenant); err != nil {
		return err
	}
	if inviter == "" {
		return p.requireRoleExists(ctx, tenant, role)
	}
	if authz.Role(role) == authz.PlatformAdminRole {
		return fmt.Errorf("%w: the role %q exists only in the platform domain", authn.ErrRoleProtected, role)
	}
	if p.membership == nil {
		return fmt.Errorf("authz/policy: judging the inviter's standing in tenant %q needs the "+
			"membership checker", tenant)
	}
	member, err := p.membership.IsMember(ctx, tenant, inviter)
	if err != nil {
		return fmt.Errorf("authz/policy: reading the membership of account %q in tenant %q: %w",
			inviter, tenant, err)
	}
	if !member {
		return fmt.Errorf("%w: the inviter is not an active member of tenant %q",
			authn.ErrForbidden, tenant)
	}
	held, err := p.heldSet(ctx, tenant, inviter)
	if err != nil {
		return err
	}
	if !held[authz.PermissionTenantRoleManage] {
		return fmt.Errorf("%w: the inviter holds no %s in tenant %q",
			authn.ErrForbidden, authz.PermissionTenantRoleManage, tenant)
	}
	if err := p.requireRoleExists(ctx, tenant, role); err != nil {
		return err
	}
	return p.requireGrantSubset(ctx, tenant, role, held)
}

// BindRoleTx implements provisioning.RoleBinder: it writes one binding in the
// transaction the acceptance hands it, with PutBindingTx's rules.
func (p *product) BindRoleTx(ctx context.Context, tx *gorm.DB, tenant, account, role string) (func(context.Context) error, error) {
	return p.PutBindingTx(ctx, tx, authz.Binding{
		Tenant:    tenant,
		Account:   account,
		Role:      authz.Role(role),
		GrantedAt: time.Now().UTC(),
	})
}

// heldSet reads an account's effective permissions in one tenant as a set.
func (p *product) heldSet(ctx context.Context, tenant, account string) (map[authz.Permission]bool, error) {
	held, err := p.Held(ctx, authz.Domain(tenant), account)
	if err != nil {
		return nil, fmt.Errorf("authz/policy: reading the permissions of account %q in tenant %q: %w",
			account, tenant, err)
	}
	set := make(map[authz.Permission]bool, len(held))
	for _, permission := range held {
		set[permission] = true
	}
	return set, nil
}

// requireGrantSubset refuses a role carrying a permission the held set does not
// contain.
func (p *product) requireGrantSubset(ctx context.Context, tenant, role string, held map[authz.Permission]bool) error {
	within, err := p.RoleSubsetHeld(ctx, tenant, authz.Role(role), held)
	if err != nil {
		return err
	}
	if !within {
		return fmt.Errorf("%w: the role %q of tenant %q carries a permission the account does not hold",
			authn.ErrRoleProtected, role, tenant)
	}
	return nil
}

// requireRoleExists refuses a role the tenant does not have.
func (p *product) requireRoleExists(ctx context.Context, tenant, role string) error {
	found, err := p.roleExists(ctx, tenant, role)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: the tenant %q has no role %q", authn.ErrRoleUnknown, tenant, role)
	}
	return nil
}

// requireWriteGuard asks the host whether the tenant protects its bindings.
func (p *product) requireWriteGuard(ctx context.Context, tenant string) error {
	if p.writeGuard == nil {
		return nil
	}
	denied, err := p.writeGuard.Denied(ctx, tenant, authz.WriteBinding)
	if err != nil {
		return fmt.Errorf("authz/policy: asking the write guard about tenant %q: %w", tenant, err)
	}
	if denied {
		return fmt.Errorf("%w: the tenant %q protects bindings", authn.ErrRoleProtected, tenant)
	}
	return nil
}

// roleExists reports whether the tenant has a role row with this name.
func (p *product) roleExists(ctx context.Context, tenant, role string) (bool, error) {
	var rows []roleRow
	err := p.crossing(ctx).Where("tenant_id = ? AND role = ?", tenant, role).Limit(1).Find(&rows).Error
	if err != nil {
		return false, fmt.Errorf("authz/policy: reading the role %q of tenant %q: %w", role, tenant, err)
	}
	return len(rows) > 0, nil
}
