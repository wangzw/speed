package tenantadmin

import (
	"context"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authz"
)

// handleRoleList answers GET /api/v1/tenants/:tenant/roles: every role of the
// tenant. A held set that cannot be read is a 503, never a role list that says
// the caller may manage nothing.
func (p *product) handleRoleList(ctx context.Context, in tenantInput) (roleListBody, error) {
	roles, err := p.manager.Roles(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, p.failureOf("listing the roles", err)
	}
	grantCounts, err := p.manager.GrantCounts(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, p.failureOf("counting the roles' grants", err)
	}
	memberCounts, err := p.roleMemberCounts(ctx, in.Tenant, roles)
	if err != nil {
		return roleListBody{}, err
	}
	held, err := p.heldPermissions(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, err
	}
	canManage := held[authz.PermissionTenantRoleManage]
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return roleListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	views := make([]AuthzTenantAdminRoleView, len(roles))
	for i, r := range roles {
		views[i] = p.roleViewOf(ctx, r, grantCounts[r.Role], memberCounts[r.Role], canManage, tenantName)
	}
	return roleListBody{Roles: views}, nil
}

// handleRolePut answers PUT /api/v1/tenants/:tenant/roles/:role: it creates or
// rewrites a role without the builtin mark. A reserved role name is refused
// unconditionally before the host write guard, the audit and the manager.
func (p *product) handleRolePut(ctx context.Context, in roleWriteInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := refuseRoleRowWrite(role); err != nil {
		return struct{}{}, err
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > authz.RoleDescriptionMax {
		return struct{}{}, badRequest()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRolePut, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailRole:     string(role),
		detailChange:   roleChange,
	})); err != nil {
		return struct{}{}, err
	}
	record := authz.RoleRecord{
		Tenant: in.Tenant,
		Role:   role,
	}
	if in.Description != nil {
		record.Description = *in.Description
	}
	if err := p.manager.PutRole(ctx, record); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRolePut, string(role), actionID))
		return struct{}{}, p.failureOf("writing a role", err)
	}
	return struct{}{}, nil
}

// handleRoleDelete answers DELETE /api/v1/tenants/:tenant/roles/:role: it
// removes a role, which revokes its grants and bindings with it. A reserved role
// name is refused unconditionally.
func (p *product) handleRoleDelete(ctx context.Context, in roleInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := refuseRoleRowWrite(role); err != nil {
		return struct{}{}, err
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRoleDelete, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	if err := p.manager.DeleteRole(ctx, in.Tenant, role); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRoleDelete, string(role), actionID))
		return struct{}{}, p.failureOf("deleting a role", err)
	}
	return struct{}{}, nil
}
