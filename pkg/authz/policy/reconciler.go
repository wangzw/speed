package policy

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/persist"
	tenancy "github.com/vislake/speed/pkg/tenant"
)

func (p *product) ReconcileGrants(ctx context.Context, tenant string, role authz.Role,
	grants, retired []authz.Grant,
) (added, removed int, err error) {
	if tenantErr := requireTenant(tenant); tenantErr != nil {
		return 0, 0, tenantErr
	}
	if role != authz.TenantAdminRole {
		return 0, 0, fmt.Errorf("%w: grant convergence acts on %q only, not %q",
			authz.ErrReservedRole, authz.TenantAdminRole, role)
	}
	err = p.write(ctx, func(tx *gorm.DB) error {
		builtin, found, stateErr := roleState(ctx, tx, tenant, role)
		if stateErr != nil {
			return stateErr
		}
		if !found {
			return fmt.Errorf("%w: the role %q of tenant %q was not created",
				authz.ErrUnknownRole, role, tenant)
		}
		if !builtin {
			return fmt.Errorf("%w: the role %q of tenant %q is not builtin, so its grant set is not converged here",
				authz.ErrReservedRole, role, tenant)
		}
		held, heldErr := grantsHeld(ctx, tx, tenant, role)
		if heldErr != nil {
			return heldErr
		}
		target := make(map[authz.Grant]struct{}, len(grants))
		adapter := persist.New(ctx, tx, tenancy.ID(tenant))
		for _, g := range grants {
			target[g] = struct{}{}
			if _, ok := held[g]; ok {
				continue
			}
			rule := []string{string(role), tenant, g.Resource, g.Action}
			if addErr := adapter.AddPolicy("p", "p", rule); addErr != nil {
				return fmt.Errorf("authz/policy: converging the grants of tenant %q: %w", tenant, addErr)
			}
			held[g] = struct{}{}
			added++
		}
		for _, g := range retired {
			if _, keep := target[g]; keep {
				continue
			}
			if _, ok := held[g]; !ok {
				continue
			}
			rule := []string{string(role), tenant, g.Resource, g.Action}
			if removeErr := adapter.RemovePolicy("p", "p", rule); removeErr != nil {
				return fmt.Errorf("authz/policy: retiring the grants of tenant %q: %w", tenant, removeErr)
			}
			delete(held, g)
			removed++
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if added == 0 && removed == 0 {
		return 0, 0, nil
	}
	return added, removed, p.afterWrite(ctx)
}
