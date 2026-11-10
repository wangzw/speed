package admin

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// handleTenantRequestTransfer answers
// POST /api/v1/system/tenants/:tenant/owner/requests: platform staff ask the
// organization's owner to hand ownership to an active member. The request is
// recorded; ownership does not change until the target confirms.
//
// The tenant is read first, so an identifier no row carries is a bare 404, a
// tombstone a 409 and a personal organization a 403 protected, all before the
// audit. The mandatory audit row lands before the request; a refusal closes it
// with a linked failed row.
func (p *product) handleTenantRequestTransfer(ctx context.Context, in requestTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	if checkErr := p.refuseProtectedOrganization(ctx, got.ID); checkErr != nil {
		return nil, checkErr
	}

	name, nameErr := p.nameOf(ctx, got.ID)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if checkErr := p.auditRequired(ctx, mandatory(actionTenantOwnerTransferRequest, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfacePlatform,
		detailTarget:   in.Account,
	})); checkErr != nil {
		return nil, checkErr
	}
	request, err := p.organization.RequestTransfer(ctx, got.ID, in.Account, "")
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransferRequest, name, actionID))
		return nil, p.failureOf("requesting a tenant's owner transfer", err)
	}
	view, err := p.transferViewOf(ctx, request)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(http.StatusCreated, transferRequestBody{Request: view}), nil
}

// handleTenantPendingTransfer answers
// GET /api/v1/system/tenants/:tenant/owner/requests/pending: platform staff read
// the organization's pending request, or null. A read writes no audit row.
func (p *product) handleTenantPendingTransfer(ctx context.Context, in pendingTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	request, found, err := p.organization.PendingTransfer(ctx, got.ID)
	if err != nil {
		return p.replyFailure("reading the pending owner-transfer request", err)
	}
	return p.pendingReply(ctx, request, found)
}

// handleTenantCancelTransfer answers
// DELETE /api/v1/system/tenants/:tenant/owner/requests/:request: platform staff
// cancel a pending request. Ownership does not change.
func (p *product) handleTenantCancelTransfer(ctx context.Context, in cancelTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !tenant.IsIdentifier(in.ID) || !validRequestID(in.Request) {
		return nil, badRequest()
	}
	got, ok, err := p.catalog.Get(ctx, tenant.ID(in.ID))
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	name, nameErr := p.nameOf(ctx, got.ID)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransferCancel, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfacePlatform,
		detailRequest:  in.Request,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.CancelTransfer(ctx, got.ID, in.Request, ""); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransferCancel, name, actionID))
		return nil, p.failureOf("cancelling an owner-transfer request", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleTenantRequestTransfer answers POST /api/v1/tenants/:tenant/owner/requests:
// the organization's own owner asks to hand ownership to an active member. The
// tenant surface's guard has already held the path tenant to the resolved tenant,
// refused the platform domain and decided that the caller is the row's owner.
//
// The expected owner is the caller, not the owner this handler's read returns, so
// an ownership handed on between the guard's read and this one refuses the stale
// request with ErrOwnerChanged.
func (p *product) handleOwnerRequestTransfer(ctx context.Context, in ownerRequestTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	if checkErr := p.refuseProtectedOrganization(ctx, got.ID); checkErr != nil {
		return nil, checkErr
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
	if checkErr := p.auditRequired(ctx, mandatory(actionTenantOwnerTransferRequest, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTenant,
		detailTarget:   in.Account,
	})); checkErr != nil {
		return nil, checkErr
	}
	request, err := p.organization.RequestTransfer(ctx, tenantID, in.Account, id.Actor().String())
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransferRequest, name, actionID))
		return nil, p.failureOf("requesting the owner transfer", err)
	}
	view, err := p.transferViewOf(ctx, request)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(http.StatusCreated, transferRequestBody{Request: view}), nil
}

// handleTenantPendingTransfer answers
// GET /api/v1/tenants/:tenant/owner/requests/pending on the tenant surface.
func (p *product) handleOwnerPendingTransfer(ctx context.Context, _ ownerPendingTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	request, found, err := p.organization.PendingTransfer(ctx, tenantID)
	if err != nil {
		return p.replyFailure("reading the pending owner-transfer request", err)
	}
	return p.pendingReply(ctx, request, found)
}

// handleTenantCancelTransfer answers
// DELETE /api/v1/tenants/:tenant/owner/requests/:request on the tenant surface.
func (p *product) handleOwnerCancelTransfer(ctx context.Context, in ownerCancelTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !validRequestID(in.Request) {
		return nil, badRequest()
	}
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
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
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransferCancel, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTenant,
		detailRequest:  in.Request,
	})); err != nil {
		return nil, err
	}
	if err := p.organization.CancelTransfer(ctx, tenantID, in.Request, id.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransferCancel, name, actionID))
		return nil, p.failureOf("cancelling the owner-transfer request", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleMyTransferRequests answers GET /api/v1/auth/me/owner-transfer-requests:
// every pending request addressed to the caller, across every organization. The
// caller's identity is the whole credential; no tenant context is read.
func (p *product) handleMyTransferRequests(ctx context.Context, _ struct{}, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	requests, err := p.ownerTarget.PendingTransfersFor(ctx, id.Actor().String())
	if err != nil {
		return p.replyFailure("reading the caller's owner-transfer requests", err)
	}
	views := make([]MyTransferView, len(requests))
	for i, request := range requests {
		view, viewErr := p.myTransferViewOf(ctx, request)
		if viewErr != nil {
			return nil, viewErr
		}
		views[i] = view
	}
	return speedhttp.JSONReply(http.StatusOK, myTransferListBody{Requests: views}), nil
}

// handleMyTransferRequest answers
// GET /api/v1/auth/me/owner-transfer-requests/:request: one request addressed to
// the caller, in whatever state it is.
func (p *product) handleMyTransferRequest(ctx context.Context, in myTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !validRequestID(in.Request) {
		return nil, badRequest()
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	request, err := p.ownerTarget.TransferFor(ctx, in.Request, id.Actor().String())
	if err != nil {
		return p.replyFailure("reading an owner-transfer request", err)
	}
	view, err := p.myTransferViewOf(ctx, request)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(http.StatusOK, myTransferBody{Request: view}), nil
}

// handleMyTransferConfirm answers
// POST /api/v1/auth/me/owner-transfer-requests/:request/confirm: the target
// confirms the request and the transfer is written. The route's guard has
// already required an identity and a proved email address.
//
// The request is read first for its organization, so the audit row names the
// organization rather than only the identifier. The mandatory audit row lands
// before the confirmation; a refusal closes it with a linked failed row.
func (p *product) handleMyTransferConfirm(ctx context.Context, in myTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !validRequestID(in.Request) {
		return nil, badRequest()
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	request, err := p.ownerTarget.TransferFor(ctx, in.Request, id.Actor().String())
	if err != nil {
		return p.replyFailure("reading an owner-transfer request", err)
	}
	name, nameErr := p.nameOf(ctx, request.Tenant)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransfer, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTarget,
		detailRequest:  in.Request,
	})); err != nil {
		return nil, err
	}
	if err := p.ownerTarget.ConfirmTransfer(ctx, in.Request, id.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransfer, name, actionID))
		return nil, p.failureOf("confirming an owner-transfer request", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// handleMyTransferDecline answers
// POST /api/v1/auth/me/owner-transfer-requests/:request/decline. Ownership does
// not change. The route is not registration-gated: declining is harmless.
func (p *product) handleMyTransferDecline(ctx context.Context, in myTransferInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if !validRequestID(in.Request) {
		return nil, badRequest()
	}
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	request, err := p.ownerTarget.TransferFor(ctx, in.Request, id.Actor().String())
	if err != nil {
		return p.replyFailure("reading an owner-transfer request", err)
	}
	name, nameErr := p.nameOf(ctx, request.Tenant)
	if nameErr != nil {
		return p.replyFailure("reading a tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantOwnerTransferDecline, name, map[string]string{
		detailActionID: actionID,
		detailTenant:   name,
		detailSurface:  surfaceTarget,
		detailRequest:  in.Request,
	})); err != nil {
		return nil, err
	}
	if err := p.ownerTarget.DeclineTransfer(ctx, in.Request, id.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantOwnerTransferDecline, name, actionID))
		return nil, p.failureOf("declining an owner-transfer request", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// pendingReply builds the pending read's answer: a view, or the explicit null.
func (p *product) pendingReply(ctx context.Context, request tenant.TransferRequest, found bool) (speedhttp.Reply, error) {
	if !found {
		return speedhttp.JSONReply(http.StatusOK, pendingTransferBody{}), nil
	}
	view, err := p.transferViewOf(ctx, request)
	if err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(http.StatusOK, pendingTransferBody{Request: &view}), nil
}

// refuseProtectedOrganization refuses a personal organization, the same
// protection the deletion and disable paths apply. The system tenant is left to
// the ownership module, which answers ErrNoOwner as a conflict: it has no owner
// to transfer from rather than being off-limits as a whole.
func (p *product) refuseProtectedOrganization(ctx context.Context, id tenant.ID) error {
	kind, err := p.kindOf(ctx, id)
	if err != nil {
		return p.failureOf("reading a tenant's kind", err)
	}
	if kind == tenant.KindPersonal {
		return speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	}
	return nil
}
