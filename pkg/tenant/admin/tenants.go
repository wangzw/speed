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

func (p *product) handleTenantCreate(ctx context.Context, in createBody) (tenantCreatedBody, error) {
	if !tenant.IsName(in.Name) {
		return tenantCreatedBody{}, badRequest()
	}
	id := tenant.NewOrganizationID()
	if !tenant.IsIdentifier(id) {
		return tenantCreatedBody{}, p.failureOf("registering a tenant",
			fmt.Errorf("the generated identifier is outside the roster's domain"))
	}
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
		OwnerAccount: caller.Actor().String(),
	}
	if err := p.provisioner.Provision(ctx, created, caller.Actor().String()); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantCreate, in.Name, actionID))
		return tenantCreatedBody{}, p.failureOf("registering a tenant", err)
	}
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

type statusBody struct {
	Status string `json:"status"`
}

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
