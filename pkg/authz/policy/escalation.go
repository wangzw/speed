package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// RoleSubsetHeld implements authz.Manager: it reports whether every permission a
// role carries is within the caller's held set. A grant the catalog cannot map
// to a declared permission is one the held set cannot contain, so it refuses. A
// non-nil error means the grants could not be read, never a pass or a refusal.
func (p *product) RoleSubsetHeld(ctx context.Context, tenant string, role authz.Role,
	held map[authz.Permission]bool,
) (bool, error) {
	grants, err := p.Grants(ctx, tenant, role)
	if err != nil {
		return false, err
	}
	return roleGrantsHeld(grants, p.catalog, held), nil
}

// roleGrantsHeld reports whether every grant is one the caller holds.
func roleGrantsHeld(grants []authz.GrantRecord, catalog authz.Catalog, held map[authz.Permission]bool) bool {
	for _, grant := range grants {
		permission, ok := catalog.PermissionOf(grant.Resource, grant.Action)
		if !ok || !held[permission] {
			return false
		}
	}
	return true
}
