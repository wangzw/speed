package admin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// handleTenantList answers GET /api/v1/system/tenants: every tenant the
// roster holds, in the roster's own order — by name. It is a read, so the
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
		kind, kindErr := p.kindOf(ctx, t.ID)
		if kindErr != nil {
			return tenantListBody{}, p.failureOf("reading a tenant's kind", kindErr)
		}
		views[i] = tenantViewOf(t, kind)
	}
	return tenantListBody{Tenants: views}, nil
}

// handleTenantGet answers GET /api/v1/system/tenants/:tenant: one tenant the
// roster holds. It is a read, so the action is tenant.read.
//
// A read goes straight to the roster, with no shape check of its own: whether an
// name names a tenant is the name layer's to answer, and a name no row
// carries is a 404. The route is addressed by the name a client
// reads; the layer resolves it to the internal identifier before this handler
// runs.
func (p *product) handleTenantGet(ctx context.Context, in getTenantInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	kind, kindErr := p.kindOf(ctx, got.ID)
	if kindErr != nil {
		return p.replyFailure("reading a tenant's kind", kindErr)
	}
	return speedhttp.JSONReply(http.StatusOK, tenantReadBody{Tenant: tenantViewOf(got, kind)}), nil
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
// identifier the roster's table already holds is answered a 409, and a name
// another tenant holds is a 409. Both fields are checked against the identifier
// domain before the provisioner is called; a registration starts the tenant
// active, the mandatory audit row lands before the provisioner runs, and a
// failure of the provisioner is closed with a linked failed row.
func (p *product) handleTenantCreate(ctx context.Context, in createBody) (tenantCreatedBody, error) {
	if !tenant.IsName(in.Name) {
		return tenantCreatedBody{}, badRequest()
	}
	// The identifier is the server's to mint: a client names the tenant by its
	// name, and an identifier in the body is refused rather than honoured.
	id := tenant.NewOrganizationID()
	if !tenant.IsIdentifier(id) {
		return tenantCreatedBody{}, p.failureOf("registering a tenant",
			fmt.Errorf("the generated identifier is outside the roster's domain"))
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
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return tenantCreatedBody{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantCreate, in.Name, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Name,
	})); err != nil {
		return tenantCreatedBody{}, err
	}
	created := tenant.Tenant{
		ID: tenant.ID(id), Name: in.Name, Status: tenant.StatusActive, Profile: profile,
		// The creator is the organization's first owner unless a transfer
		// hands it on; the provisioner binds the same account as tenant-admin.
		OwnerAccount: caller.Actor().String(),
	}
	if err := p.provisioner.Provision(ctx, created, caller.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantCreate, in.Name, actionID))
		return tenantCreatedBody{}, p.failureOf("registering a tenant", err)
	}
	// The answer is the row as the roster holds it, not the value this call
	// assembled: the creation time is the roster's to record, so a view built
	// from the input would carry the zero instant.
	written, found, err := p.catalog.Get(ctx, tenant.ID(id))
	if err != nil {
		return tenantCreatedBody{}, p.failureOf("reading a tenant back after registering it", err)
	}
	if !found {
		return tenantCreatedBody{}, p.failureOf("reading a tenant back after registering it",
			fmt.Errorf("the provisioner reported success but the roster holds no tenant %q", id))
	}
	kind, kindErr := p.kindOf(ctx, written.ID)
	if kindErr != nil {
		return tenantCreatedBody{}, p.failureOf("reading a tenant's kind", kindErr)
	}
	return tenantCreatedBody{Tenant: tenantViewOf(written, kind)}, nil
}

// handleTenantTransferOwner answers PUT /api/v1/system/tenants/:tenant/owner:
// it hands one tenant's ownership to an active member. It is a write, so the
// action is tenant.manage.
//
// The tenant is read first so an identifier no row carries is a bare 404 and a
// tombstone a 409, both before the audit. The mandatory audit row lands before
// the transfer; a refusal closes it with a linked failed row. The transfer
// itself is the host seam's: it grants the target tenant-admin if needed, writes
// the owner, and removes the previous owner's bindings and membership in one
// transaction. A target that is not an active member, a target whose account is
// not active, and a transfer that lost the row to a deletion, are 409. This is
// platform staff's route, so the transfer carries no ownership expectation.
func (p *product) handleTenantTransferOwner(ctx context.Context, in transferOwnerInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !tenant.IsIdentifier(in.ID) {
		return nil, badRequest()
	}
	if _, err := authn.ParseAccountID(in.Account); err != nil {
		return nil, badRequest()
	}
	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if got.Status == tenant.StatusDeleted {
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	}

	name, nameErr := p.nameOf(ctx, tenant.ID(in.ID))
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransfer, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfacePlatform,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.TransferOwner(ctx, tenant.ID(in.ID), in.Account, ""); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransfer, name, actionID))
		return nil, p.failureOf("transferring a tenant's owner", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleTenantDelete answers DELETE /api/v1/system/tenants/:tenant: it writes
// the tenant's tombstone and cleans its core relations. It is platform staff's
// route: the action is tenant.manage in the platform domain.
//
// The organization's owner does not reach this route; the owner's own deletion
// is the tenant-surface route (handleTenantOwnerDelete), which shares the write
// and the locks and carries the caller as the owner it expects. Platform staff
// carry no ownership expectation here.
//
// The tenant is read for the existence answers before the audit: an identifier
// no row carries is a bare 404 and a tombstone a 409. A protected tenant is
// refused by the host's write guard, as the status route refuses a disable. The
// mandatory audit row lands before the deletion; a refusal closes it with a
// linked failed row.
func (p *product) handleTenantDelete(ctx context.Context, in getTenantInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !tenant.IsIdentifier(in.ID) {
		return nil, badRequest()
	}
	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if got.Status == tenant.StatusDeleted {
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	}
	denied, err := p.writeGuard.Denied(ctx, tenant.ID(in.ID), tenant.WriteDelete)
	if err != nil {
		p.logger.Error(ModuleName+": the write guard could not answer, so the deletion was not carried out",
			slog.String("tenant", in.ID), slog.String("cause", err.Error()))
		return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if denied {
		return nil, speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	}

	name, nameErr := p.nameOf(ctx, tenant.ID(in.ID))
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantDelete, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfacePlatform,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.DeleteOrganization(ctx, tenant.ID(in.ID), ""); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantDelete, name, actionID))
		return nil, p.failureOf("deleting a tenant", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleTenantOwnerTransfer answers PUT /api/v1/tenants/:tenant/owner: the
// organization's own owner hands its ownership to an active member. The tenant
// surface's guard has already held the path tenant to the resolved tenant,
// refused the platform domain and decided that the caller is the row's owner;
// this handler reads the row again for its existence answers, lands the
// mandatory audit row and calls the same organization writer the platform
// surface calls.
//
// The expected owner is the caller, not the owner this handler's read returns: a
// transfer can commit between the guard's read and this one, and the second read
// would otherwise name the new owner as its own expectation. A transfer that
// wins the race therefore leaves the old owner's transfer a 409
// (tenant.ErrOwnerChanged) rather than handing on an organization the caller no
// longer governs.
//
// The tenant is the identifier the request resolved to, never the path segment
// as written, so a name addresses the same tenant.
func (p *product) handleTenantOwnerTransfer(ctx context.Context, in ownerTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if _, err := authn.ParseAccountID(in.Account); err != nil {
		return nil, badRequest()
	}
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	got, found, err := p.catalog.Get(ctx, tenantID)
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !found {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if got.Status == tenant.StatusDeleted {
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}

	name, nameErr := p.nameOf(ctx, tenantID)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransfer, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTenant,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.TransferOwner(ctx, tenantID, in.Account, id.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransfer, name, actionID))
		return nil, p.failureOf("transferring a tenant's owner", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleTenantOwnerDelete answers DELETE /api/v1/tenants/:tenant: the
// organization's own owner ends it. The tenant surface's guard has already held
// the path tenant to the resolved tenant, refused the platform domain and
// decided that the caller is the row's owner; this handler reads the row again
// for its existence answers and the write guard, lands the mandatory audit row
// naming the tenant surface, and calls the same organization writer the platform
// surface calls.
//
// The expected owner is the caller, not the owner this handler's read returns: a
// transfer can commit between the guard's read and this one, and the second read
// would otherwise name the new owner as its own expectation. A transfer that
// wins the race therefore leaves the old owner's deletion a 409
// (tenant.ErrOwnerChanged) rather than deleting an organization the caller no
// longer governs.
func (p *product) handleTenantOwnerDelete(ctx context.Context, in tenantDeleteInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	got, found, err := p.catalog.Get(ctx, tenantID)
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !found {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	if got.Status == tenant.StatusDeleted {
		return nil, speedhttp.Fault{Status: http.StatusConflict, Type: typePrefix + conflictCode}
	}
	denied, err := p.writeGuard.Denied(ctx, tenantID, tenant.WriteDelete)
	if err != nil {
		p.logger.Error(ModuleName+": the write guard could not answer, so the deletion was not carried out",
			slog.String("tenant", string(tenantID)), slog.String("cause", err.Error()))
		return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if denied {
		return nil, speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	name, nameErr := p.nameOf(ctx, tenantID)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantDelete, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTenant,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.DeleteOrganization(ctx, tenantID, id.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantDelete, name, actionID))
		return nil, p.failureOf("deleting a tenant", err)
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

	name, nameErr := p.nameOf(ctx, tenant.ID(in.ID))
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantProfileUpdate, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfacePlatform,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetProfile(ctx, tenant.ID(in.ID), profile); err != nil {
		p.auditBestEffort(ctx, failedProfileAudit(actionTenantProfileUpdate, name, actionID))
		return p.replyFailure("writing a tenant's profile", err)
	}
	got.Profile = profile
	kind, kindErr := p.kindOf(ctx, got.ID)
	if kindErr != nil {
		return p.replyFailure("reading a tenant's kind", kindErr)
	}
	return speedhttp.JSONReply(http.StatusOK, tenantReadBody{Tenant: tenantViewOf(got, kind)}), nil
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
	name, nameErr := p.nameOf(ctx, tenant.ID(in.ID))
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantStatus, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetStatus(ctx, tenant.ID(in.ID), status); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantStatus, name, actionID))
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
