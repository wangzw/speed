package tenantadmin

import "context"

// handlePermissionList answers GET /api/v1/tenants/:tenant/permissions: every
// permission of the governed set, from Catalog.TenantGoverned() and never
// Catalog.Declared(). The set is returned whole, with grantable a property of
// the caller. A held set that cannot be read is a 503.
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
