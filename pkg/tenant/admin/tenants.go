package admin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// handleTenantList answers GET /api/v1/system/tenants: every tenant the
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

// handleTenantGet answers GET /api/v1/system/tenants/:tenant: one tenant the
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

// handleTenantCreate answers POST /api/v1/system/tenants: it registers a
// tenant whose identifier the roster does not hold yet. It is a write, so the
// action is tenant.manage.
//
// The registration goes through tenant.Provisioner, which creates the tenant's
// tenant-admin role, its grants and the creator's binding and writes the roster
// row last; this route never calls Catalog.Create itself, because a tenant and
// its first administrator have to come into being together. A refusal of the
// roster's Create is reported by the provisioner with its sentinel intact: an
// identifier the roster's table already holds is answered a 409, and an alias
// another tenant holds is a 409. Both fields are checked against the identifier
// domain before the provisioner is called; a registration starts the tenant
// active, the mandatory audit row lands before the provisioner runs, and a
// failure of the provisioner is closed with a linked failed row.
func (p *product) handleTenantCreate(ctx context.Context, in createBody) (tenantCreatedBody, error) {
	if !tenant.IsIdentifier(in.ID) || !tenant.IsIdentifier(in.Alias) {
		return tenantCreatedBody{}, badRequest()
	}
	// The profile is optional here and judged by the same root-package rules the
	// profile write uses. The value is normalized before the provisioner sees
	// it, so the roster stores what a surface will show.
	profile := tenant.Profile{
		DisplayName:  in.DisplayName,
		Description:  in.Description,
		ContactEmail: in.ContactEmail,
		Website:      in.Website,
	}.Normalize()
	if err := profile.Validate(); err != nil {
		return tenantCreatedBody{}, badRequest()
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return tenantCreatedBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantCreate, in.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.ID,
	})); err != nil {
		return tenantCreatedBody{}, err
	}
	created := tenant.Tenant{
		ID: tenant.ID(in.ID), Alias: in.Alias, Status: tenant.StatusActive, Profile: profile,
	}
	if err := p.provisioner.Provision(ctx, created, id.Actor().String()); err != nil {
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

// handleTenantSetAlias answers PATCH /api/v1/system/tenants/:tenant: it changes
// one tenant's alias and nothing else. It is a write, so the action is
// tenant.manage.
//
// The write goes through Catalog.SetAlias, which writes the alias column alone.
// It never goes through Put: Put takes the whole row's new values and writes the
// status back with the alias, so a rename would carry whatever status this
// process last read, and a tenant another replica disabled in that window — the
// gap between the snapshot and the table — would be served again. Both fields
// are checked against the identifier domain, and a tenant the roster does not
// hold is a 404 before the audit row is written. A tombstone reaches SetAlias
// and is refused with tenant.ErrTenantDeleted, answered a 409: the row and the
// alias stay, and a rename does not revive it. The mandatory audit row lands
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

// handleTenantSetProfile answers PATCH /api/v1/system/tenants/:tenant/profile:
// it writes one tenant's organization profile and nothing else. It is a write,
// so the action is tenant.manage.
//
// The shape is checked before the roster is read, so an invalid profile costs
// no read and writes no audit row. The existence check precedes the audit, so a
// tenant no row carries is a bare 404 with no row written. The mandatory audit
// row lands before SetProfile and names the platform surface; a roster refusal
// closes it with a linked failed row. The answer is the tenant with the profile
// as written, built from the row this request read rather than a second read
// that could fail after the committed write.
func (p *product) handleTenantSetProfile(ctx context.Context, in setProfileInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !tenant.IsIdentifier(in.ID) {
		return nil, badRequest()
	}
	profile := tenant.Profile{
		DisplayName:  in.DisplayName,
		Description:  in.Description,
		ContactEmail: in.ContactEmail,
		Website:      in.Website,
	}.Normalize()
	if err := profile.Validate(); err != nil {
		return nil, badRequest()
	}

	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}

	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantProfileUpdate, in.ID, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.ID,
		detailSurface:  surfacePlatform,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetProfile(ctx, tenant.ID(in.ID), profile); err != nil {
		p.auditBestEffort(ctx, failedProfileAudit(actionTenantProfileUpdate, in.ID, actionID))
		return p.replyFailure("writing a tenant's profile", err)
	}
	got.Profile = profile
	return speedhttp.JSONReply(http.StatusOK, tenantReadBody{Tenant: tenantViewOf(got)}), nil
}

// statusBody is the status change's body as a case builds it. The route's
// declared input pairs the same state with the path's identifier.
type statusBody struct {
	Status string `json:"status"`
}

// handleTenantSetStatus answers POST /api/v1/system/tenants/:tenant/status: it
// changes one tenant's state. It is a write, so the action is tenant.manage.
//
// The state is a closed set: active or disabled, anything else a 400 before the
// guard and the roster are called. Deletion is not in this surface's writable
// set — it is the roster's terminal marker, reached through Catalog.Delete by
// the host that winds an account up — so this route never writes the deleted
// state. A disable is one of the operator writes the host may protect, so when
// the target is disabled the route asks tenant.WriteGuard past the shape checks
// and before the audit: a denied write is a 403 with the protected type, and a
// guard that cannot answer is a 503; neither writes an audit row or reaches the
// roster. An enable is not a write the guard protects, so it asks nothing. An
// identifier no row carries comes back with tenant.ErrInvalidTenant, answered a
// 404, and a tombstone comes back with tenant.ErrTenantDeleted, answered a 409:
// deletion is terminal and no status change revives it. The mandatory audit row
// lands before SetStatus runs.
func (p *product) handleTenantSetStatus(ctx context.Context, in setStatusInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	status, ok := statusOf(in.Status)
	if !ok {
		return nil, badRequest()
	}
	if !tenant.IsIdentifier(in.ID) {
		return nil, badRequest()
	}
	if status == tenant.StatusDisabled {
		denied, err := p.writeGuard.Denied(ctx, tenant.ID(in.ID), tenant.WriteDisable)
		if err != nil {
			p.logger.Error(ModuleName+": the write guard could not answer, so the status change was not carried out",
				slog.String("tenant", in.ID), slog.String("cause", err.Error()))
			return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
		}
		if denied {
			return nil, speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
		}
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
