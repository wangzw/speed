package admin

import "context"

// handlePermissionList answers GET /api/v1/system/permissions: every
// permission the catalog declares, each as its own view. It is a read, so the
// action is role.read; the identity and the permission are decided by the route's
// guard wrapper before this handler is called.
//
// The set is returned whole: grantable is a property of the caller, and a
// permission the caller cannot grant is still one the caller may need to see. A
// held set that cannot be read is a 503, never a catalog whose every permission
// quietly says it cannot be granted.
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
