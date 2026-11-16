package invite

import (
	"context"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// handleIssue answers POST /api/v1/tenants/:tenant/invitations: it issues an
// invitation and returns its view together with the notice's outcome, and with
// the one-time token only when the notice did not reach the address. The action
// is membership.manage, decided in the tenant in the path. The invitation's
// identifier is minted before the mandatory audit row, so that row names the
// invitation the action creates, and the row a failed action writes names the
// same identifier.
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
	// An omitted deadline, or a null one, takes the configured lifetime. An
	// explicit zero instant is a malformed value rather than an omission, so it
	// is refused like any other out-of-domain deadline.
	deadline := time.Time{}
	if in.ExpiresAt != nil {
		if in.ExpiresAt.IsZero() {
			return AuthnInviteIssued{}, p.badRequest()
		}
		deadline = *in.ExpiresAt
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnInviteIssued{}, p.cause("reading the tenant's name", nameErr)
	}
	invitationID, idErr := newID()
	if idErr != nil {
		return AuthnInviteIssued{}, p.answer("minting an invitation identifier", idErr)
	}
	actionID := newActionID()
	detail := map[string]string{detailActionID: actionID, detailTenant: tenantName}
	if in.Role != "" {
		detail[detailRole] = in.Role
	}
	if err = p.auditRequired(ctx, mandatory(actionIssue, invitationID, detail)); err != nil {
		return AuthnInviteIssued{}, err
	}
	issued, err := p.issue(ctx, IssueInvite{
		Tenant: in.Tenant, Email: in.Email, ExpiresAt: deadline, Role: in.Role,
	}, invitationID)
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionIssue, invitationID, actionID))
		return AuthnInviteIssued{}, p.answer("issuing an invitation", err)
	}
	// The notice is best effort: the invitation is issued and its token is the
	// one-time link whatever the delivery outcome, so the answer stays 201. The
	// view is built after the write too, so an inviter whose name cannot be read
	// leaves the name out instead of discarding the token.
	delivery := p.deliverInvite(ctx, issued, actionID)
	return p.issuedViewOf(ctx, issued, delivery, tenantName), nil
}

// handleList answers GET /api/v1/tenants/:tenant/invitations: one page of the
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
	// One instant judges both the page's groups and its views' states, so a row
	// crossing its deadline during the request does not read as two states.
	now := p.at()
	invitations, next, err := p.list(ctx, in.Tenant, authn.Page{Limit: in.Limit, After: in.After}, filter, now)
	if err != nil {
		return AuthnInvitePage{}, p.answer("listing invitations", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnInvitePage{}, p.cause("reading the tenant's name", nameErr)
	}
	// A page keeps every invitation even when an inviter cannot be named: the
	// names are omitted from the views that need them.
	views := p.invitationViewsOf(ctx, invitations, tenantName, now)
	return AuthnInvitePage{Invitations: views, NextCursor: next}, nil
}

// handleRevoke answers DELETE /api/v1/tenants/:tenant/invitations/:id: it
// revokes a pending invitation that has not expired. The action is
// membership.manage, and the invitation's identifier is the decision's target.
func (p *product) handleRevoke(ctx context.Context, in revokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if admitErr := p.admit(); admitErr != nil {
		return nil, admitErr
	}
	if signupErr := p.gateSignup(ctx, id); signupErr != nil {
		return nil, signupErr
	}
	if decisionErr := p.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant, Target: in.ID,
	}); decisionErr != nil {
		return nil, decisionErr
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

// handleResend answers POST /api/v1/tenants/:tenant/invitations/:id/resend: it
// sends a pending invitation again on the same row and returns its view with the
// notice's outcome. The action is membership.manage, and the invitation's
// identifier is the decision's target. The admission order is the issue
// handler's: sign-in, the signup gate, the decision, then the mandatory audit
// row. The send budgets are charged by the domain call itself, after the audit:
// the issuing account's before the terminal check, so a terminal row still costs
// the operator, and the address's and the deployment's after it, counted only
// when the resend's write lands.
func (p *product) handleResend(ctx context.Context, in resendInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if admitErr := p.admit(); admitErr != nil {
		return nil, admitErr
	}
	if signupErr := p.gateSignup(ctx, id); signupErr != nil {
		return nil, signupErr
	}
	if decisionErr := p.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant, Target: in.ID,
	}); decisionErr != nil {
		return nil, decisionErr
	}
	if in.ID == "" {
		return nil, p.badRequest()
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, p.cause("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if auditErr := p.auditRequired(ctx, mandatory(actionResend, in.ID,
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); auditErr != nil {
		return nil, auditErr
	}
	issued, err := p.Resend(ctx, in.Tenant, in.ID)
	if err != nil {
		p.auditBestEffort(ctx, failedAudit(actionResend, in.ID, actionID))
		return p.answerReply("resending an invitation", err)
	}
	// As on an issue, the resend and its notice are already settled: a name that
	// cannot be read is omitted, and the answer keeps the new token because the
	// old link is void.
	delivery := p.deliverInvite(ctx, issued, actionID)
	return speedhttp.JSONReply(http.StatusOK, p.issuedViewOf(ctx, issued, delivery, tenantName)), nil
}
