package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// memberViewOf builds one membership as the management routes show it.
func memberViewOf(mem authn.Membership) AuthnAdminMember {
	return AuthnAdminMember{
		Account:   string(mem.Account),
		Tenant:    mem.Tenant,
		Active:    mem.Active,
		SSOExempt: mem.SSOExempt,
		Source:    mem.Source,
	}
}

// handleMemberList answers GET /admin/tenants/:tenant/members. The tenant in the
// path is the decision's tenant; there is no look-up of the tenant itself,
// because this capability does not hold a tenant roster. The action is
// membership.manage.
func (m *product) handleMemberList(ctx context.Context, in memberListInput) (AuthnAdminMemberList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminMemberList{}, err
	}
	if err = m.admit(id); err != nil {
		return AuthnAdminMemberList{}, err
	}
	if in.Limit < 0 {
		return AuthnAdminMemberList{}, m.bad()
	}
	page := authn.Page{Limit: in.Limit, After: in.After}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant}); err != nil {
		return AuthnAdminMemberList{}, err
	}
	members, err := m.memberships.Members(ctx, in.Tenant, page)
	if err != nil {
		return AuthnAdminMemberList{}, m.unavailable("listing members", err)
	}
	views := make([]AuthnAdminMember, len(members))
	for i, mem := range members {
		views[i] = memberViewOf(mem)
	}
	return AuthnAdminMemberList{Members: views}, nil
}

// handleMemberPut answers PUT /admin/tenants/:tenant/members/:account: it adds,
// suspends or resumes a membership. The action is membership.manage.
func (m *product) handleMemberPut(ctx context.Context, in memberWriteInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.Account)
	source := in.Source
	if source == "" {
		source = authn.ReasonAdmin
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipSet, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: in.Tenant})); err != nil {
		return nil, err
	}
	mem := authn.Membership{
		Account: accountID, Tenant: in.Tenant, Active: in.Active,
		Source: source, SSOExempt: in.SSOExempt,
	}
	if err := m.memberships.Put(ctx, mem); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipSet, string(accountID), actionID))
		return m.replyFailure("writing a membership", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleMemberRemove answers DELETE /admin/tenants/:tenant/members/:account. The
// action is membership.manage.
func (m *product) handleMemberRemove(ctx context.Context, in memberAddressInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.Account)
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipRemove, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: in.Tenant})); err != nil {
		return nil, err
	}
	if err := m.memberships.Remove(ctx, accountID, in.Tenant); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipRemove, string(accountID), actionID))
		return m.replyFailure("removing a membership", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleMemberSSOExempt answers PUT
// /admin/tenants/:tenant/members/:account/sso-exempt: it sets or clears the
// emergency exemption from the tenant's enforced SSO. The action is
// membership.manage.
func (m *product) handleMemberSSOExempt(ctx context.Context, in memberExemptInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.Account)
	mem, found, err := m.memberships.Membership(ctx, in.Tenant, accountID)
	if err != nil {
		return nil, m.unavailable("reading a membership", err)
	}
	if !found {
		return speedhttp.EmptyReply(404), nil
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipSet, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: in.Tenant, detailChange: "sso-exempt"})); err != nil {
		return nil, err
	}
	mem.SSOExempt = in.SSOExempt
	if err := m.memberships.Put(ctx, mem); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipSet, string(accountID), actionID))
		return m.replyFailure("writing the SSO exemption", err)
	}
	return speedhttp.EmptyReply(204), nil
}
