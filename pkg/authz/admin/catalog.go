package admin

import "context"

// handlePermissionList answers GET /api/v1/system/permissions: every
// permission the catalog declares, each as its own view. It is a read, so the
// action is role.read; the identity and the permission are decided by the route's
// guard wrapper before this handler is called.
func (p *product) handlePermissionList(ctx context.Context, _ struct{}) (permissionListBody, error) {
	declared := p.catalog.Declared()
	views := make([]AuthzAdminPermissionView, len(declared))
	for i, perm := range declared {
		views[i] = p.permissionViewOf(ctx, perm)
	}
	return permissionListBody{Permissions: views}, nil
}
