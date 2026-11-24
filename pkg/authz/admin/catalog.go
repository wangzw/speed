package admin

import "context"

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
