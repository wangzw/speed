package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/tenant"
)

// handleTenantList answers GET /api/v1/tenant/admin/tenants: every tenant the roster
// holds, in the roster's own order — by identifier. It is a read, so the action
// is tenant.read. The roster is the set of tenants one deployment serves, and it
// is not paged.
//
// Listing the roster is a platform read, not a cross-tenant one: the roster's
// table is not a tenant-scoped table and its reads are outside the filter, so
// enumerating every tenant is a read the process makes in the platform domain,
// and it opens no cross-tenant context and requires no cross-tenant permission.
func (p *product) handleTenantList(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTenantRead) {
		return
	}
	tenants, err := p.catalog.List(c.Request.Context())
	if err != nil {
		p.answer(c, "listing the tenants", err)
		return
	}
	views := make([]tenantView, len(tenants))
	for i, t := range tenants {
		views[i] = tenantViewOf(t)
	}
	c.JSON(http.StatusOK, gin.H{"tenants": views})
}

// handleTenantGet answers GET /api/v1/tenant/admin/tenants/:id: one tenant the roster
// holds. It is a read, so the action is tenant.read.
//
// A read goes straight to the roster, with no shape check of its own: whether an
// identifier names a tenant is the roster's to answer, and an identifier no row
// carries is a 404. The route is addressed by the identifier, the natural key
// the roster keys a row by, so no opaque row identifier ever appears in a path.
func (p *product) handleTenantGet(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTenantRead) {
		return
	}
	got, ok, err := p.catalog.Get(c.Request.Context(), tenant.ID(c.Param("id")))
	if err != nil {
		p.answer(c, "reading a tenant", err)
		return
	}
	if !ok {
		p.notFound(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenant": tenantViewOf(got)})
}

// createBody is the request of a registration: the tenant's identifier and the
// alias the request surface will address it by. The status is not here: a
// registration starts the tenant active.
type createBody struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

// handleTenantCreate answers POST /api/v1/tenant/admin/tenants: it registers a tenant
// whose identifier the roster does not hold yet. It is a write, so the action is
// tenant.manage.
//
// The registration goes through Catalog.Create, which never overwrites: an
// identifier the roster's table already holds is refused with
// tenant.ErrTenantExists and answered a 409, and nothing is written. Create and
// Put divide here — Put rewrites the row its identifier names, alias and status
// together, which is a seed's action, while Create only inserts, which is how a
// route that opens a tenant is written. Both fields are checked against the
// identifier domain before the roster is called; a registration starts the
// tenant active, and the mandatory audit row lands before Create runs.
func (p *product) handleTenantCreate(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTenantManage) {
		return
	}
	var body createBody
	if !p.body(c, &body) {
		return
	}
	if !tenant.IsIdentifier(body.ID) || !tenant.IsIdentifier(body.Alias) {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTenantCreate, body.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   body.ID,
	})) {
		return
	}
	created := tenant.Tenant{ID: tenant.ID(body.ID), Alias: body.Alias, Status: tenant.StatusActive}
	if err := p.catalog.Create(c.Request.Context(), created); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTenantCreate, body.ID, actionID))
		p.answer(c, "registering a tenant", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"tenant": tenantViewOf(created)})
}

// aliasBody is the request of a rename: the new alias.
type aliasBody struct {
	Alias string `json:"alias"`
}

// handleTenantSetAlias answers PATCH /api/v1/tenant/admin/tenants/:id: it changes one
// tenant's alias and nothing else. It is a write, so the action is tenant.manage.
//
// The write goes through Catalog.SetAlias, which writes the alias column alone.
// It never goes through Put: Put takes the whole row's new values and writes the
// status back with the alias, so a rename would carry whatever status this
// process last read, and a tenant another replica disabled in that window — the
// gap between the snapshot and the table — would be served again. Both fields
// are checked against the identifier domain, and a tenant the roster does not
// hold is a 404 before the audit row is written. The mandatory audit row lands
// before SetAlias runs.
func (p *product) handleTenantSetAlias(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTenantManage) {
		return
	}
	id := c.Param("id")
	var body aliasBody
	if !p.body(c, &body) {
		return
	}
	if !tenant.IsIdentifier(id) || !tenant.IsIdentifier(body.Alias) {
		p.bad(c)
		return
	}
	_, ok, err := p.catalog.Get(c.Request.Context(), tenant.ID(id))
	if err != nil {
		p.answer(c, "reading a tenant", err)
		return
	}
	if !ok {
		p.notFound(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTenantUpdate, id, map[string]string{
		detailActionID: actionID,
		detailTenant:   id,
	})) {
		return
	}
	if err := p.catalog.SetAlias(c.Request.Context(), tenant.ID(id), body.Alias); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTenantUpdate, id, actionID))
		p.answer(c, "renaming a tenant", err)
		return
	}
	p.noContent(c)
}

// statusBody is the request of a status change: the target state, one of the
// closed set active and disabled.
type statusBody struct {
	Status string `json:"status"`
}

// handleTenantSetStatus answers POST /api/v1/tenant/admin/tenants/:id/status: it changes
// one tenant's state. It is a write, so the action is tenant.manage.
//
// The state is a closed set: active or disabled, anything else a 400 before the
// roster is called. Disabling goes through Catalog.SetStatus and only through
// it — the roster has no delete — and an identifier no row carries comes back
// with tenant.ErrInvalidTenant, answered a 404. The mandatory audit row lands
// before SetStatus runs.
func (p *product) handleTenantSetStatus(c *gin.Context) {
	if _, ok := p.caller(c); !ok {
		return
	}
	if !p.require(c, PermissionTenantManage) {
		return
	}
	id := c.Param("id")
	var body statusBody
	if !p.body(c, &body) {
		return
	}
	status, ok := statusOf(body.Status)
	if !ok {
		p.bad(c)
		return
	}
	if !tenant.IsIdentifier(id) {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionTenantStatus, id, map[string]string{
		detailActionID: actionID,
		detailTenant:   id,
	})) {
		return
	}
	if err := p.catalog.SetStatus(c.Request.Context(), tenant.ID(id), status); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionTenantStatus, id, actionID))
		p.answer(c, "changing a tenant's status", err)
		return
	}
	p.noContent(c)
}

// statusOf reads the closed set of tenant states off a request value, by the
// names tenant.Status spells them: active and disabled. Any other value is a
// shape this module refuses.
func statusOf(s string) (tenant.Status, bool) {
	switch s {
	case tenant.StatusActive.String():
		return tenant.StatusActive, true
	case tenant.StatusDisabled.String():
		return tenant.StatusDisabled, true
	default:
		return tenant.StatusDisabled, false
	}
}
