package profile

import (
	"context"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/tenant"
)

// handleGet answers GET /api/v1/tenants/{tenant}/profile: one tenant's
// organization profile.
//
// The tenant is the identifier the request resolved to, never the path segment
// as written: the path-tenant layer has already held the segment to that
// tenant, and the alias it carries may differ from the identifier. An
// identifier the roster does not carry is a bare 404.
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
	return speedhttp.JSONReply(http.StatusOK, profileBody{Profile: profileViewOf(got, kind)}), nil
}

// handleSet answers PATCH /api/v1/tenants/{tenant}/profile: it writes one
// tenant's organization profile and nothing else.
//
// The shape is checked before the roster is read, so an invalid profile costs
// no read and writes no audit row. The existence check precedes the audit, so a
// tenant no row carries is a 404 with no row written. The mandatory audit row
// lands before SetProfile; a roster refusal closes it with a linked failed row.
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
	if err := p.auditRequired(ctx, mandatory(actionTenantProfileUpdate, got.Alias, map[string]string{
		detailActionID: actionID,
		detailTenant:   got.Alias,
		detailSurface:  surfaceTenant,
	})); err != nil {
		return nil, err
	}
	if err := p.catalog.SetProfile(ctx, id, profile); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionTenantProfileUpdate, got.Alias, actionID))
		return p.replyFailure("writing a tenant's profile", err)
	}

	// The read-only fields are the row's as this request read them; the profile
	// is what the write just stored. Building the answer from the two avoids a
	// second read that could fail after a committed write.
	kind, kindErr := p.kindOf(ctx, got.ID)
	if kindErr != nil {
		return p.replyFailure("reading a tenant's kind", kindErr)
	}
	got.Profile = profile
	return speedhttp.JSONReply(http.StatusOK, profileBody{Profile: profileViewOf(got, kind)}), nil
}
