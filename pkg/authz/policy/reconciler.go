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
// It acts on TenantAdminRole alone: another role is ErrReservedRole before any
// row is read, because no other role's grant set is a host-owned target. A
// TenantAdminRole that is not builtin is ErrReservedRole as well: its grants are
// still the management surface's to change, and converging them here would take
// that away without the mark that says the host owns them. An absent role is
// ErrUnknownRole and an empty tenant ErrTenantMismatch.
//
// The additions and removals are one transaction. The role check takes the
// role's row lock, so a concurrent delete and this convergence serialise. The
// held set is read before anything is written: a pair listed in both grants and
// retired is kept, because the target set is what the caller declares current
// and the retired list is what this version withdraws.
//
// A pair outside grants and outside retired is left as it stands. It is not
// removed by set difference: during a rolling deployment another replica may
// still declare it, and deleting by difference would let the replicas erase each
// other's grants.
//
// Once the transaction commits, this replica reloads its snapshot and publishes
// authz.policy.changed. A call that writes nothing does neither, which is what
// makes a repeated reconciliation over an unchanged target set free.
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
