package tenantadmin

import "context"

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
