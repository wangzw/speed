package invite

import (
	"context"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// handleIssue answers POST /admin/tenants/:tenant/invitations: it issues an
// invitation and returns its view together with the notice's outcome, and with
// the one-time token only when the notice did not reach the address. The action
// is membership.manage, decided in the tenant in the path.
//
// The entry point binds the declared input before the handler runs, so a body it
// cannot read is refused before the decision; the decision still comes before
// the action, and nothing of the body is read by the domain call when the
// decision refuses.
func (p *product) handleIssue(ctx context.Context, in issueInput) (AuthnInviteIssued, error) {
	id, err := p.caller(ctx)
	if err != nil {
		return AuthnInviteIssued{}, err
	}
	if err = p.admit(); err != nil {
		return AuthnInviteIssued{}, err
	}
	if err = p.gateSignup(ctx, id); err != nil {
		return AuthnInviteIssued{}, err
	}
	if in.Tenant == "" {
		return AuthnInviteIssued{}, p.badRequest()
	}
	if err = p.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
	}); err != nil {
		return AuthnInviteIssued{}, err
	}
	if in.Email == "" {
		return AuthnInviteIssued{}, p.badRequest()
	}
	deadline := time.Time{}
	if in.ExpiresAt != nil {
		deadline = *in.ExpiresAt
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnInviteIssued{}, p.cause("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err = p.auditRequired(ctx, mandatory(actionIssue, tenantName,
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return AuthnInviteIssued{}, err
	}
	issued, err := p.Issue(ctx, IssueInvite{Tenant: in.Tenant, Email: in.Email, ExpiresAt: deadline})
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionIssue, tenantName, actionID))
		return AuthnInviteIssued{}, p.answer("issuing an invitation", err)
	}
	// The notice is best effort: the invitation is issued and its token is the
	// one-time link whatever the delivery outcome, so the answer stays 201.
	delivery := p.deliverInvite(ctx, issued, actionID)
	return issuedViewOf(issued, delivery, tenantName), nil
}

// handleList answers GET /admin/tenants/:tenant/invitations: one page of the
// tenant's invitations. The action is membership.manage, and a read writes no
// audit row.
func (p *product) handleList(ctx context.Context, in listInput) (AuthnInvitePage, error) {
	id, err := p.caller(ctx)
	if err != nil {
		return AuthnInvitePage{}, err
	}
	if err = p.admit(); err != nil {
		return AuthnInvitePage{}, err
	}
	if err = p.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
	}); err != nil {
		return AuthnInvitePage{}, err
	}
	// A negative cap is refused here rather than by a binding rule: the entry
	// point parses the query value, and the route's answer to a value outside
	// the domain is the 400 it has always been.
	if in.Limit < 0 {
		return AuthnInvitePage{}, p.badRequest()
	}
	filter, err := parseListFilter(in.Status)
	if err != nil {
		return AuthnInvitePage{}, p.badRequest()
	}
	invitations, next, err := p.list(ctx, in.Tenant, authn.Page{Limit: in.Limit, After: in.After}, filter)
	if err != nil {
		return AuthnInvitePage{}, p.answer("listing invitations", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnInvitePage{}, p.cause("reading the tenant's name", nameErr)
	}
	views := make([]AuthnInviteInvitation, len(invitations))
	for i, inv := range invitations {
		views[i] = invitationViewOf(inv, tenantName)
	}
	return AuthnInvitePage{Invitations: views, NextCursor: next}, nil
}

// handleRevoke answers DELETE /admin/tenants/:tenant/invitations/:id: it
// revokes a pending invitation. The action is membership.manage, and the
// invitation's identifier is the decision's target.
func (p *product) handleRevoke(ctx context.Context, in revokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.admit(); err != nil {
		return nil, err
	}
	if err := p.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := p.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant, Target: in.ID,
	}); err != nil {
		return nil, err
	}
	if in.ID == "" {
		return nil, p.badRequest()
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, p.cause("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRevoke, in.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	if err := p.Revoke(ctx, in.Tenant, in.ID); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRevoke, in.ID, actionID))
		return p.answerReply("revoking an invitation", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}
