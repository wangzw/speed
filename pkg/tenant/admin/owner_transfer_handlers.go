package admin

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

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

// The expected owner is the caller, not the owner this handler reads: ownership handed on since the guard's read must refuse the stale request with ErrOwnerChanged.
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

// The system tenant is left to the ownership module, which answers ErrNoOwner as a conflict.
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
