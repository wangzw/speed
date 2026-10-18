package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

// handleBindingList answers GET /api/v1/authz/admin/tenants/:tenant/bindings: every
// role binding of the tenant. It is a read, so the action is role.read.
func (p *product) handleBindingList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleRead) {
		return
	}
	tenant := c.Param("tenant")
	bindings, err := p.manager.Bindings(c.Request.Context(), tenant)
	if err != nil {
		p.answer(c, "listing the bindings", err)
		return
	}
	p.writeBindings(c, bindings)
}

// handleBindingOf answers GET
// /api/v1/authz/admin/tenants/:tenant/bindings/:account: every binding the account
// holds within the tenant. It is a read, so the action is role.read.
func (p *product) handleBindingOf(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleRead) {
		return
	}
	tenant, account := c.Param("tenant"), c.Param("account")
	bindings, err := p.manager.BindingsOf(c.Request.Context(), tenant, account)
	if err != nil {
		p.answer(c, "listing the account's bindings", err)
		return
	}
	p.writeBindings(c, bindings)
}

// writeBindings answers a binding read with its views.
func (p *product) writeBindings(c *gin.Context, bindings []authz.Binding) {
	views := make([]bindingView, len(bindings))
	for i, b := range bindings {
		views[i] = bindingViewOf(b)
	}
	c.JSON(http.StatusOK, gin.H{"bindings": views})
}

// handleBindingPut answers PUT
// /api/v1/authz/admin/tenants/:tenant/bindings/:account/:role: it binds a role to an
// account within the tenant. It is a write, so the action is role.manage. The
// mandatory audit row lands before the manager writes.
//
// An empty account is refused on shape before the manager is called: a binding
// addresses an account, and the empty account names the zero subject, so a
// request that carries none is a 400 rather than a write the manager would
// refuse without a code the module maps.
func (p *product) handleBindingPut(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, account := c.Param("tenant"), c.Param("account")
	if account == "" {
		p.bad(c)
		return
	}
	role := authz.Role(c.Param("role"))
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionBindingPut, account, map[string]string{
		detailActionID: actionID,
		detailTenant:   tenant,
		detailAccount:  account,
		detailRole:     string(role),
	})) {
		return
	}
	binding := authz.Binding{Tenant: tenant, Account: account, Role: role}
	if err := p.manager.PutBinding(c.Request.Context(), binding); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionBindingPut, account, actionID))
		p.answer(c, "writing a binding", err)
		return
	}
	p.noContent(c)
}

// handleBindingDelete answers DELETE
// /api/v1/authz/admin/tenants/:tenant/bindings/:account/:role: it removes an account's
// binding to a role within the tenant. It is a write, so the action is
// role.manage. The mandatory audit row lands before the manager writes.
//
// An empty account is refused on shape before the manager is called, the same
// as the binding write: the route addresses a binding by its account, and an
// empty account names no row.
func (p *product) handleBindingDelete(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, authz.PermissionRoleManage) {
		return
	}
	tenant, account := c.Param("tenant"), c.Param("account")
	if account == "" {
		p.bad(c)
		return
	}
	role := authz.Role(c.Param("role"))
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionBindingDelete, account, map[string]string{
		detailActionID: actionID,
		detailTenant:   tenant,
		detailAccount:  account,
		detailRole:     string(role),
	})) {
		return
	}
	if err := p.manager.DeleteBinding(c.Request.Context(), tenant, account, role); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionBindingDelete, account, actionID))
		p.answer(c, "deleting a binding", err)
		return
	}
	p.noContent(c)
}
