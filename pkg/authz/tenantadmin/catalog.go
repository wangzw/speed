package tenantadmin

import "context"

// handlePermissionList answers GET /api/v1/tenants/:tenant/permissions: every
// permission of the governed set, each as its own view. It is a read, so the
// action is authz.tenant.role.read; the identity and the permission are decided
// by the route's guard wrapper before this handler is called.
//
// The set is Catalog.TenantGoverned() and never Catalog.Declared(): a permission
// outside the governed set is one a tenant administrator may neither hold nor
// grant, so it is neither shown here nor writable through the grant routes.
//
// The set is returned whole: grantable is a property of the caller, and a
// permission the caller cannot grant is still one the caller may need to see.
// A held set that cannot be read is a 503, never a catalog whose every
// permission quietly says it cannot be granted.
func (p *product) handlePermissionList(ctx context.Context, in tenantInput) (permissionListBody, error) {
	governed := p.catalog.TenantGoverned()
	held, err := p.heldPermissions(ctx, in.Tenant)
	if err != nil {
		return permissionListBody{}, err
	}
	views := make([]AuthzTenantAdminPermissionView, len(governed))
	for i, perm := range governed {
		views[i] = p.permissionViewOf(ctx, perm, held[perm])
	}
	return permissionListBody{Permissions: views}, nil
}
