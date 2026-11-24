package profile

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

func (p *product) handleGet(ctx context.Context, _ profileInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	got, found, err := p.catalog.Get(ctx, id)
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !found {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	kind, kindErr := p.kindOf(ctx, got.ID)
	if kindErr != nil {
		return p.replyFailure("reading a tenant's kind", kindErr)
	}
	view := profileViewOf(got, kind)
	ownerDisplayName, ownerUsername, ownerErr := p.ownerNames(ctx, got.OwnerAccount)
	if ownerErr != nil {
		return nil, ownerErr
	}
	view.OwnerDisplayName = ownerDisplayName
	view.OwnerUsername = ownerUsername
	return speedhttp.JSONReply(http.StatusOK, profileBody{Profile: view}), nil
}

func (p *product) handleSet(ctx context.Context, in profileWriteInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	profile := tenant.Profile{
		DisplayName:  in.DisplayName,
		Description:  in.Description,
		ContactEmail: in.ContactEmail,
		Website:      in.Website,
	}.Normalize()
	if err := profile.Validate(); err != nil {
		return nil, badRequest()
	}

	id, ok := tenant.FromContext(ctx)
	if !ok {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}
	got, found, err := p.catalog.Get(ctx, id)
	if err != nil {
		return p.replyFailure("reading a tenant", err)
	}
	if !found {
		return speedhttp.EmptyReply(http.StatusNotFound), nil
	}

	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionTenantProfileUpdate, got.Name, map[string]string{
		detailActionID: actionID,
		detailTenant:   got.Name,
		detailSurface:  surfaceTenant,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetProfile(ctx, id, profile); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantProfileUpdate, got.Name, actionID))
		return p.replyFailure("writing a tenant's profile", err)
	}

	kind, kindErr := p.kindOf(ctx, got.ID)
	if kindErr != nil {
		return p.replyFailure("reading a tenant's kind", kindErr)
	}
	got.Profile = profile
	view := profileViewOf(got, kind)
	ownerDisplayName, ownerUsername, ownerErr := p.ownerNames(ctx, got.OwnerAccount)
	if ownerErr != nil {
		return nil, ownerErr
	}
	view.OwnerDisplayName = ownerDisplayName
	view.OwnerUsername = ownerUsername
	return speedhttp.JSONReply(http.StatusOK, profileBody{Profile: view}), nil
}
