package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// handleRoleList answers GET /api/v1/system/tenants/:tenant/roles: every role of
// the tenant. The tenant in the path is the policy domain, taken as the
// identifier and not an alias; this module holds no tenant roster and does not
// look the tenant up. It is a read, so the action is role.read; the identity and
// the permission are decided by the route's guard wrapper before this handler is
// called.
func (p *product) handleRoleList(ctx context.Context, in tenantInput) (roleListBody, error) {
	roles, err := p.manager.Roles(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, p.failureOf("listing the roles", err)
	}
	views := make([]AuthzAdminRoleView, len(roles))
	for i, r := range roles {
		views[i] = roleViewOf(r)
	}
	return roleListBody{Roles: views}, nil
}

// handleRolePut answers PUT /api/v1/system/tenants/:tenant/roles/:role: it creates
// or rewrites a role without the builtin mark. It is a write, so the action is
// role.manage. The mandatory audit row lands before the manager is called; a
// builtin role the manager refuses is a 409 with a failed row beside the
// permitted one.
func (p *product) handleRolePut(ctx context.Context, in roleWriteInput) (struct{}, error) {
	role := authz.Role(in.Role)
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
	if err := p.manager.PutRole(ctx, record); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRolePut, string(role), actionID))
		return struct{}{}, p.failureOf("writing a role", err)
	}
	return struct{}{}, nil
}

// handleRoleDelete answers DELETE /api/v1/system/tenants/:tenant/roles/:role: it
// removes a role, which revokes its grants and bindings with it. It is a write,
// so the action is role.manage. A builtin role the manager refuses is a 409.
func (p *product) handleRoleDelete(ctx context.Context, in roleInput) (struct{}, error) {
	role := authz.Role(in.Role)
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

// roleChange names the kind of record a role write changes, for the audit row's
// change key.
const roleChange = "role"
