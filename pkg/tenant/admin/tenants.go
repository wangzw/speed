package admin

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// handleTenantList answers GET /api/v1/tenant/admin/tenants: every tenant the
// roster holds, in the roster's own order — by identifier. It is a read, so the
// action is tenant.read. The roster is the set of tenants one deployment serves,
// and it is not paged.
//
// Listing the roster is a platform read, not a cross-tenant one: the roster's
// table is not a tenant-scoped table and its reads are outside the filter, so
// enumerating every tenant is a read the process makes in the platform domain,
// and it opens no cross-tenant context and requires no cross-tenant permission.
// The identity and the permission are decided by the route's guard wrapper
// before this handler is called.
func (p *product) handleTenantList(ctx context.Context, _ struct{}) (tenantListBody, error) {
	tenants, err := p.catalog.List(ctx)
	if err != nil {
		return tenantListBody{}, p.failureOf("listing the tenants", err)
	}
	views := make([]tenantView, len(tenants))
	for i, t := range tenants {
		views[i] = tenantViewOf(t)
	}
	return tenantListBody{Tenants: views}, nil
}

// handleTenantGet answers GET /api/v1/tenant/admin/tenants/:id: one tenant the
// roster holds. It is a read, so the action is tenant.read.
//
// A read goes straight to the roster, with no shape check of its own: whether an
// identifier names a tenant is the roster's to answer, and an identifier no row
// carries is a 404. The route is addressed by the identifier, the natural key
// the roster keys a row by, so no opaque row identifier ever appears in a path.
func (p *product) handleTenantGet(ctx context.Context, in getTenantInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	return speedhttp.JSONReply(http.StatusOK, tenantReadBody{Tenant: tenantViewOf(got)}), nil
}

// handleTenantCreate answers POST /api/v1/tenant/admin/tenants: it registers a
// tenant whose identifier the roster does not hold yet. It is a write, so the
// action is tenant.manage.
//
// The registration goes through Catalog.Create, which never overwrites: an
// identifier the roster's table already holds is refused with
// tenant.ErrTenantExists and answered a 409, and nothing is written. Create and
// Put divide here — Put rewrites the row its identifier names, alias and status
// together, which is a seed's action, while Create only inserts, which is how a
// route that opens a tenant is written. Both fields are checked against the
// identifier domain before the roster is called; a registration starts the
// tenant active, and the mandatory audit row lands before Create runs.
func (p *product) handleTenantCreate(ctx context.Context, in createBody) (tenantCreatedBody, error) {
	if !tenant.IsIdentifier(in.ID) || !tenant.IsIdentifier(in.Alias) {
		return tenantCreatedBody{}, badRequest()
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantCreate, in.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.ID,
	})); err != nil {
		return tenantCreatedBody{}, err
	}
	created := tenant.Tenant{ID: tenant.ID(in.ID), Alias: in.Alias, Status: tenant.StatusActive}
	if err := p.catalog.Create(ctx, created); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantCreate, in.ID, actionID))
		return tenantCreatedBody{}, p.failureOf("registering a tenant", err)
	}
	return tenantCreatedBody{Tenant: tenantViewOf(created)}, nil
}

// aliasBody is the rename request's body as a case builds it. The route's
// declared input pairs the same alias with the path's identifier.
type aliasBody struct {
	Alias string `json:"alias"`
}

// handleTenantSetAlias answers PATCH /api/v1/tenant/admin/tenants/:id: it changes
// one tenant's alias and nothing else. It is a write, so the action is
// tenant.manage.
//
// The write goes through Catalog.SetAlias, which writes the alias column alone.
// It never goes through Put: Put takes the whole row's new values and writes the
// status back with the alias, so a rename would carry whatever status this
// process last read, and a tenant another replica disabled in that window — the
// gap between the snapshot and the table — would be served again. Both fields
// are checked against the identifier domain, and a tenant the roster does not
// hold is a 404 before the audit row is written. The mandatory audit row lands
// before SetAlias runs.
func (p *product) handleTenantSetAlias(ctx context.Context, in setAliasInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !tenant.IsIdentifier(in.ID) || !tenant.IsIdentifier(in.Alias) {
		return nil, badRequest()
	}
	_, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantUpdate, in.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.ID,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetAlias(ctx, tenant.ID(in.ID), in.Alias); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantUpdate, in.ID, actionID))
		return p.replyFailure("renaming a tenant", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// statusBody is the status change's body as a case builds it. The route's
// declared input pairs the same state with the path's identifier.
type statusBody struct {
	Status string `json:"status"`
}

// handleTenantSetStatus answers POST /api/v1/tenant/admin/tenants/:id/status: it
// changes one tenant's state. It is a write, so the action is tenant.manage.
//
// The state is a closed set: active or disabled, anything else a 400 before the
// roster is called. Disabling goes through Catalog.SetStatus and only through it
// — the roster has no delete — and an identifier no row carries comes back with
// tenant.ErrInvalidTenant, answered a 404. The mandatory audit row lands before
// SetStatus runs.
func (p *product) handleTenantSetStatus(ctx context.Context, in setStatusInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	status, ok := statusOf(in.Status)
	if !ok {
		return nil, badRequest()
	}
	if !tenant.IsIdentifier(in.ID) {
		return nil, badRequest()
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantStatus, in.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.ID,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetStatus(ctx, tenant.ID(in.ID), status); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantStatus, in.ID, actionID))
		return p.replyFailure("changing a tenant's status", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
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
