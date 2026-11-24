package admin

import "context"

// handlePermissionList answers GET /api/v1/system/permissions: every permission
// the catalog declares. The set is returned whole, with grantable a property of
// the caller. A held set that cannot be read is a 503.
func (p *product) handlePermissionList(ctx context.Context, _ struct{}) (permissionListBody, error) {
	declared := p.catalog.Declared()
	held, err := p.heldPermissions(ctx)
	if err != nil {
		return permissionListBody{}, err
	}
	views := make([]AuthzAdminPermissionView, len(declared))
	for i, perm := range declared {
		views[i] = p.permissionViewOf(ctx, perm, held[perm])
	}
	return permissionListBody{Permissions: views}, nil
}
