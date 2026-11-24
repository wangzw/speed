package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// handleGrantList answers GET /api/v1/system/tenants/:tenant/roles/:role/grants:
// every permission grant the role holds.
func (p *product) handleGrantList(ctx context.Context, in roleInput) (grantListBody, error) {
	grants, err := p.manager.Grants(ctx, in.Tenant, authz.Role(in.Role))
	if err != nil {
		return grantListBody{}, p.failureOf("listing the grants", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return grantListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	views := make([]AuthzAdminGrantView, len(grants))
	for i, g := range grants {
		views[i] = p.grantViewOf(g, tenantName)
	}
	return grantListBody{Grants: views}, nil
}

// handleGrantPut answers PUT
// /api/v1/system/tenants/:tenant/roles/:role/grants/:permission: it grants a
// permission to a role. A permission the catalog does not hold, or one the
// translation has no row for, is a 400 before the manager is touched.
func (p *product) handleGrantPut(ctx context.Context, in grantInput) (struct{}, error) {
	role := authz.Role(in.Role)
	permission := authz.Permission(in.Permission)
	if !p.catalog.Has(permission) {
		return struct{}{}, badRequest()
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
// /api/v1/system/tenants/:tenant/roles/:role/grants/:permission: it revokes a
// permission from a role. A permission the translation has no row for is a 400.
func (p *product) handleGrantDelete(ctx context.Context, in grantInput) (struct{}, error) {
	role := authz.Role(in.Role)
	permission := authz.Permission(in.Permission)
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
