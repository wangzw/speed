package tenantadmin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// handleGrantList answers GET /api/v1/tenants/:tenant/roles/:role/grants: every
// permission grant the role holds. It is a read, so the action is
// authz.tenant.role.read.
func (p *product) handleGrantList(ctx context.Context, in roleInput) (grantListBody, error) {
	grants, err := p.manager.Grants(ctx, in.Tenant, authz.Role(in.Role))
	if err != nil {
		return grantListBody{}, p.failureOf("listing the grants", err)
	}
	views := make([]AuthzTenantAdminGrantView, len(grants))
	for i, g := range grants {
		views[i] = grantViewOf(g)
	}
	return grantListBody{Grants: views}, nil
}

// handleGrantPut answers PUT
// /api/v1/tenants/:tenant/roles/:role/grants/:permission: it grants a governed
// permission to a role. It is a write, so the action is authz.tenant.role.manage.
//
// The checks run in this order, before the host write guard, the audit and the
// manager: the role must not be reserved, the permission must be in the
// governed set, the translation must have a row for it, and the caller must
// hold it. A permission outside the governed set is a 400 because the request
// named a code this surface cannot grant; a permission the caller does not
// hold is a refusal with the protected code because the target does not permit
// the write. A caller whose held set cannot be read is a 503.
func (p *product) handleGrantPut(ctx context.Context, in grantInput) (struct{}, error) {
	role := authz.Role(in.Role)
	permission := authz.Permission(in.Permission)
	if err := refuseRoleGrantsWrite(role); err != nil {
		return struct{}{}, err
	}
	if !p.catalog.Governed(permission) {
		return struct{}{}, badRequest()
	}
	resource, action, ok := p.catalog.ObjectAction(permission)
	if !ok {
		return struct{}{}, badRequest()
	}
	held, err := p.heldPermissions(ctx, in.Tenant)
	if err != nil {
		return struct{}{}, err
	}
	if !held[permission] {
		return struct{}{}, protected()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteGrant); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionGrantPut, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     in.Tenant,
		detailRole:       string(role),
		detailPermission: string(permission),
	})); err != nil {
		return struct{}{}, err
	}
	record := authz.GrantRecord{Tenant: in.Tenant, Role: role, Resource: resource, Action: action}
	if err := p.manager.PutGrant(ctx, record); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionGrantPut, string(permission), actionID))
		return struct{}{}, p.failureOf("writing a grant", err)
	}
	return struct{}{}, nil
}

// handleGrantDelete answers DELETE
// /api/v1/tenants/:tenant/roles/:role/grants/:permission: it revokes a
// permission from a role. It is a write, so the action is
// authz.tenant.role.manage.
//
// The governed-set rule belongs to the grant write and not to the revoke: a
// permission that has left the set can still be removed. The role must not be
// reserved, and the translation must have a row for the permission before the
// manager is called.
func (p *product) handleGrantDelete(ctx context.Context, in grantInput) (struct{}, error) {
	role := authz.Role(in.Role)
	permission := authz.Permission(in.Permission)
	if err := refuseRoleGrantsWrite(role); err != nil {
		return struct{}{}, err
	}
	resource, action, ok := p.catalog.ObjectAction(permission)
	if !ok {
		return struct{}{}, badRequest()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteGrant); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionGrantDelete, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     in.Tenant,
		detailRole:       string(role),
		detailPermission: string(permission),
	})); err != nil {
		return struct{}{}, err
	}
	record := authz.GrantRecord{Tenant: in.Tenant, Role: role, Resource: resource, Action: action}
	if err := p.manager.DeleteGrant(ctx, record); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionGrantDelete, string(permission), actionID))
		return struct{}{}, p.failureOf("deleting a grant", err)
	}
	return struct{}{}, nil
}
