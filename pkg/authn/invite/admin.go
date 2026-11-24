package invite

import (
	"context"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

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
	delivery := p.deliverInvite(ctx, issued, actionID)
	return p.issuedViewOf(ctx, issued, delivery, tenantName), nil
}

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
	if in.Limit < 0 {
		return AuthnInvitePage{}, p.badRequest()
	}
	filter, err := parseListFilter(in.Status)
	if err != nil {
		return AuthnInvitePage{}, p.badRequest()
	}
	// One instant judges both the page's groups and its views' states.
	now := p.at()
	invitations, next, err := p.list(ctx, in.Tenant, authn.Page{Limit: in.Limit, After: in.After}, filter, now)
	if err != nil {
		return AuthnInvitePage{}, p.answer("listing invitations", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnInvitePage{}, p.cause("reading the tenant's name", nameErr)
	}
	views := p.invitationViewsOf(ctx, invitations, tenantName, now)
	return AuthnInvitePage{Invitations: views, NextCursor: next}, nil
}

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
	delivery := p.deliverInvite(ctx, issued, actionID)
	return speedhttp.JSONReply(http.StatusOK, p.issuedViewOf(ctx, issued, delivery, tenantName)), nil
}
