package tenantadmin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// handleGrantList answers GET /api/v1/tenants/:tenant/roles/:role/grants: every
// permission grant the role holds.
func (p *product) handleGrantList(ctx context.Context, in roleInput) (grantListBody, error) {
	grants, err := p.manager.Grants(ctx, in.Tenant, authz.Role(in.Role))
	if err != nil {
		return grantListBody{}, p.failureOf("listing the grants", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return grantListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	views := make([]AuthzTenantAdminGrantView, len(grants))
	for i, g := range grants {
		views[i] = p.grantViewOf(g, tenantName)
	}
	return grantListBody{Grants: views}, nil
}

// handleGrantPut answers PUT
// /api/v1/tenants/:tenant/roles/:role/grants/:permission: it grants a governed
// permission to a role. Before the host write guard, the audit and the manager
// it checks that the role is not reserved, the permission is governed, the
// translation has a row for it, and the caller holds it. A held set that cannot
// be read is a 503.
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
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionGrantPut, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     tenantName,
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
// permission from a role. The governed-set rule belongs to the grant write and
// not to the revoke: a permission that has left the set can still be removed.
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
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionGrantDelete, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     tenantName,
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
