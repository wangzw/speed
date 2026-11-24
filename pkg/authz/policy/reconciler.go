package policy

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/persist"
	tenancy "github.com/vislake/speed/pkg/tenant"
)

// ReconcileGrants converges a builtin tenant-admin role's grant set to grants
// and removes from it only the grants named in retired that it currently holds.
//
// It acts on a builtin TenantAdminRole alone: another role, or a tenant-admin
// role that is not builtin, is ErrReservedRole before any row is read. An absent
// role is ErrUnknownRole and an empty tenant ErrTenantMismatch.
//
// The additions and removals are one transaction, and the role check takes the
// role's row lock. A pair outside grants and outside retired is left as it
// stands: deleting by set difference would let replicas erase each other's
// grants during a rolling deployment. Once the transaction commits, this replica
// reloads its snapshot and publishes authz.policy.changed; a call that writes
// nothing does neither.
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
		// The transaction rolled back every row this call counted.
		return 0, 0, err
	}
	if added == 0 && removed == 0 {
		return 0, 0, nil
	}
	return added, removed, p.afterWrite(ctx)
}
