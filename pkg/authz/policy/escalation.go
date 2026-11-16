package policy

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// RoleSubsetHeld implements authz.Manager. It reports whether every permission
// a role carries is within the caller's held set, the anti-escalation rule the
// tenant management surface's binding write and the invitation's role judgement
// share: a caller may hand out only what it holds itself.
//
// The rule is expressed over the policy data rather than over a request: the
// role's grants are read through this module, each grant is mapped back to a
// permission through the catalog, and the held set answers. A grant the catalog
// cannot map to a declared permission is one the caller's set cannot contain, so
// it refuses.
//
// The second result is false for a refusal and true for a role within the held
// set. A non-nil error means the grants could not be read: it is the judgement
// being unavailable, and a caller must treat it as neither a pass nor a refusal.
func (p *product) RoleSubsetHeld(ctx context.Context, tenant string, role authz.Role,
	held map[authz.Permission]bool,
) (bool, error) {
	grants, err := p.Grants(ctx, tenant, role)
	if err != nil {
		return false, err
	}
	return roleGrantsHeld(grants, p.catalog, held), nil
}

// roleGrantsHeld reports whether every grant is one the caller holds. It is the
// pure half of RoleSubsetHeld, shared with this module's own role judgement.
func roleGrantsHeld(grants []authz.GrantRecord, catalog authz.Catalog, held map[authz.Permission]bool) bool {
	for _, grant := range grants {
		permission, ok := catalog.PermissionOf(grant.Resource, grant.Action)
		if !ok || !held[permission] {
			return false
		}
	}
	return true
}
