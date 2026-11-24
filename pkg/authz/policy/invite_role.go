package policy

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
)

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

func (p *product) BindRoleTx(ctx context.Context, tx *gorm.DB, tenant, account, role string) (func(context.Context) error, error) {
	return p.PutBindingTx(ctx, tx, authz.Binding{
		Tenant:    tenant,
		Account:   account,
		Role:      authz.Role(role),
		GrantedAt: time.Now().UTC(),
	})
}

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

func (p *product) roleExists(ctx context.Context, tenant, role string) (bool, error) {
	var rows []roleRow
	err := p.crossing(ctx).Where("tenant_id = ? AND role = ?", tenant, role).Limit(1).Find(&rows).Error
	if err != nil {
		return false, fmt.Errorf("authz/policy: reading the role %q of tenant %q: %w", role, tenant, err)
	}
	return len(rows) > 0, nil
}
