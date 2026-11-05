package tenantadmin

import (
	"context"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authz"
)

// handleRoleList answers GET /api/v1/tenants/:tenant/roles: every role of the
// tenant, with its description, its grant count, its active member count and
// whether the caller may manage its grants. It is a read, so the action is
// authz.tenant.role.read; the identity and the permission are decided by the
// route's guard wrapper.
//
// The counts are read in one pass each: the grant counts from one grouped read,
// and the member counts from one tenant-wide binding read plus one batch
// identity read. The caller's held set is read because can_manage is a
// property of the caller, not of the role; a held set that cannot be read is a
// 503, never a role list that quietly says the caller may manage nothing.
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
	views := make([]AuthzTenantAdminRoleView, len(roles))
	for i, r := range roles {
		views[i] = p.roleViewOf(ctx, r, grantCounts[r.Role], memberCounts[r.Role], canManage)
	}
	return roleListBody{Roles: views}, nil
}

// handleRolePut answers PUT /api/v1/tenants/:tenant/roles/:role: it creates or
// rewrites a role without the builtin mark. It is a write, so the action is
// authz.tenant.role.manage.
//
// A reserved role name is refused unconditionally before the host write guard,
// the audit and the manager: a tenant administrator cannot create the platform
// administrator role, and cannot create or rewrite the tenant administrator's
// role row either — that row's grant set is the host's to seed and converge.
func (p *product) handleRolePut(ctx context.Context, in roleWriteInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := refuseRoleRowWrite(role); err != nil {
		return struct{}{}, err
	}
	// The description is the request's to set: when the member is present, even
	// as an explicit empty string, it takes priority, and description_key is used
	// only when description is absent. The role name is not writable: it comes
	// from the path, which is the binding's key.
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > authz.RoleDescriptionMax {
		return struct{}{}, badRequest()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRolePut, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
		detailRole:     string(role),
		detailChange:   roleChange,
	})); err != nil {
		return struct{}{}, err
	}
	record := authz.RoleRecord{
		Tenant:         in.Tenant,
		Role:           role,
		DescriptionKey: in.DescriptionKey,
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
// removes a role, which revokes its grants and bindings with it. It is a write,
// so the action is authz.tenant.role.manage.
//
// A reserved role name is refused unconditionally: the tenant administrator's
// role row is the host's, and the platform administrator's role does not belong
// to a tenant domain.
func (p *product) handleRoleDelete(ctx context.Context, in roleInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := refuseRoleRowWrite(role); err != nil {
		return struct{}{}, err
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRoleDelete, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
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
