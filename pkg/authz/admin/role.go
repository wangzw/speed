package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

// handleRoleList answers GET /authz/admin/tenants/:tenant/roles: every role of
// the tenant. The tenant in the path is the policy domain, taken as the
// identifier and not an alias; this module holds no tenant roster and does not
// look the tenant up. It is a read, so the action is role.read.
func (p *product) handleRoleList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleRead) {
		return
	}
	tenant := c.Param("tenant")
	roles, err := p.manager.Roles(c.Request.Context(), tenant)
	if err != nil {
		p.answer(c, "listing the roles", err)
		return
	}
	views := make([]roleView, len(roles))
	for i, r := range roles {
		views[i] = roleViewOf(r)
	}
	c.JSON(http.StatusOK, gin.H{"roles": views})
}

// roleBody is the request of a role write: the i18n key of the role's
// description. The builtin mark is not part of the request — a client cannot
// set it, and the field a client sends for it reaches nothing. The mark is
// what freezes a role for good, so it is set by the host that seeds the policy
// data through Manager.MarkRoleBuiltin, never by the management surface.
type roleBody struct {
	DescriptionKey string `json:"description_key"`
}

// handleRolePut answers PUT /authz/admin/tenants/:tenant/roles/:role: it creates
// or rewrites a role without the builtin mark. It is a write, so the action is
// role.manage. The mandatory audit row lands before the manager is called; a
// builtin role the manager refuses is a 409 with a failed row beside the
// permitted one.
func (p *product) handleRolePut(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, role := c.Param("tenant"), authz.Role(c.Param("role"))
	var body roleBody
	if !p.body(c, &body) {
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionRolePut, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenant,
		detailRole:     string(role),
		detailChange:   roleChange,
	})) {
		return
	}
	record := authz.RoleRecord{
		Tenant:         tenant,
		Role:           role,
		DescriptionKey: body.DescriptionKey,
	}
	if err := p.manager.PutRole(c.Request.Context(), record); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionRolePut, string(role), actionID))
		p.answer(c, "writing a role", err)
		return
	}
	p.noContent(c)
}

// handleRoleDelete answers DELETE /authz/admin/tenants/:tenant/roles/:role: it
// removes a role, which revokes its grants and bindings with it. It is a write,
// so the action is role.manage. A builtin role the manager refuses is a 409.
func (p *product) handleRoleDelete(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, role := c.Param("tenant"), authz.Role(c.Param("role"))
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionRoleDelete, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenant,
		detailRole:     string(role),
	})) {
		return
	}
	if err := p.manager.DeleteRole(c.Request.Context(), tenant, role); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionRoleDelete, string(role), actionID))
		p.answer(c, "deleting a role", err)
		return
	}
	p.noContent(c)
}

// roleChange names the kind of record a role write changes, for the audit row's
// change key.
const roleChange = "role"
