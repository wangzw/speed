package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

func (p *product) RoleSubsetHeld(ctx context.Context, tenant string, role authz.Role,
	held map[authz.Permission]bool,
) (bool, error) {
	grants, err := p.Grants(ctx, tenant, role)
	if err != nil {
		return false, err
	}
	return roleGrantsHeld(grants, p.catalog, held), nil
}

func roleGrantsHeld(grants []authz.GrantRecord, catalog authz.Catalog, held map[authz.Permission]bool) bool {
	for _, grant := range grants {
		permission, ok := catalog.PermissionOf(grant.Resource, grant.Action)
		if !ok || !held[permission] {
			return false
		}
	}
	return true
}
