package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
)

// handleGrantList answers GET /authz/admin/tenants/:tenant/roles/:role/grants:
// every permission grant the role holds. It is a read, so the action is
// role.read.
func (p *product) handleGrantList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleRead) {
		return
	}
	tenant, role := c.Param("tenant"), authz.Role(c.Param("role"))
	grants, err := p.manager.Grants(c.Request.Context(), tenant, role)
	if err != nil {
		p.answer(c, "listing the grants", err)
		return
	}
	views := make([]grantView, len(grants))
	for i, g := range grants {
		views[i] = grantViewOf(g)
	}
	c.JSON(http.StatusOK, gin.H{"grants": views})
}

// handleGrantPut answers PUT
// /authz/admin/tenants/:tenant/roles/:role/grants/:permission: it grants a
// permission to a role. It is a write, so the action is role.manage.
//
// The permission named in the path is checked against the catalog and
// translated to the (resource, action) pair the engine matches on before the
// manager is called; a permission the catalog does not hold, or one the
// translation has no row for, is a 400 and the manager is not touched. The
// mandatory audit row lands before the manager writes.
func (p *product) handleGrantPut(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, role := c.Param("tenant"), authz.Role(c.Param("role"))
	permission := authz.Permission(c.Param("permission"))
	if !p.catalog.Has(permission) {
		p.bad(c)
		return
	}
	resource, action, ok := guard.ObjectAction(permission)
	if !ok {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionGrantPut, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     tenant,
		detailRole:       string(role),
		detailPermission: string(permission),
	})) {
		return
	}
	record := authz.GrantRecord{Tenant: tenant, Role: role, Resource: resource, Action: action}
	if err := p.manager.PutGrant(c.Request.Context(), record); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionGrantPut, string(permission), actionID))
		p.answer(c, "writing a grant", err)
		return
	}
	p.noContent(c)
}

// handleGrantDelete answers DELETE
// /authz/admin/tenants/:tenant/roles/:role/grants/:permission: it revokes a
// permission from a role. It is a write, so the action is role.manage.
//
// The permission named in the path is translated to the (resource, action) pair
// before the manager is called; a permission the translation has no row for is a
// 400. The mandatory audit row lands before the manager writes.
func (p *product) handleGrantDelete(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, role := c.Param("tenant"), authz.Role(c.Param("role"))
	permission := authz.Permission(c.Param("permission"))
	resource, action, ok := guard.ObjectAction(permission)
	if !ok {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionGrantDelete, string(permission), map[string]string{
		detailActionID:   actionID,
		detailTenant:     tenant,
		detailRole:       string(role),
		detailPermission: string(permission),
	})) {
		return
	}
	record := authz.GrantRecord{Tenant: tenant, Role: role, Resource: resource, Action: action}
	if err := p.manager.DeleteGrant(c.Request.Context(), record); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionGrantDelete, string(permission), actionID))
		p.answer(c, "deleting a grant", err)
		return
	}
	p.noContent(c)
}
