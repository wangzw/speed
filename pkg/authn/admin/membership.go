package admin

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// memberViewOf builds one membership as the management routes show it,
// carrying the account's display name resolved outside the membership store and
// the role names the policy side holds for the account.
func memberViewOf(mem authn.Membership, displayName string, roles []string) AuthnAdminMember {
	return AuthnAdminMember{
		Account:     string(mem.Account),
		DisplayName: displayName,
		Tenant:      mem.Tenant,
		Active:      mem.Active,
		SSOExempt:   mem.SSOExempt,
		Source:      mem.Source,
		Roles:       roleNames(roles),
		JoinedAt:    mem.CreatedAt.UTC(),
	}
}

// roleNames is the view's role array: ascending, without duplicates and never
// nil. The reader promises that order already; sorting here holds the view's
// contract even for an implementation that answers another one.
func roleNames(names []string) []string {
	out := make([]string, 0, len(names))
	out = append(out, names...)
	slices.Sort(out)
	return slices.Compact(out)
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
	if err = m.admit(id, tenantDomain); err != nil {
		return AuthnAdminMemberList{}, err
	}
	if in.Limit < 0 {
		return AuthnAdminMemberList{}, m.bad()
	}
	if !utf8.ValidString(in.Query) {
		return AuthnAdminMemberList{}, m.bad()
	}
	query := authn.MemberQuery{
		Page:  authn.Page{Limit: in.Limit, After: in.After},
		Query: in.Query,
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant}); err != nil {
		return AuthnAdminMemberList{}, err
	}
	members, next, err := m.memberPage(ctx, in.Tenant, query)
	if err != nil {
		return AuthnAdminMemberList{}, m.unavailable("listing members", err)
	}
	roles := m.rolesByAccount(ctx, in.Tenant)
	views := make([]AuthnAdminMember, len(members))
	for i, mem := range members {
		// The member read carries the account's display name so the workspace
		// administration face needs no platform account read. The account read
		// has no batch form, so the page resolves one row at a time; an account
		// deleted between the two reads has no name, the empty display name the
		// account model gives an account it never wrote.
		name, err := m.memberDisplayName(ctx, mem.Account)
		if err != nil {
			return AuthnAdminMemberList{}, err
		}
		views[i] = memberViewOf(mem, name, roles[mem.Account])
	}
	return AuthnAdminMemberList{Members: views, NextCursor: next}, nil
}

// memberPage reads one page of members and answers the cursor of the next page,
// which is empty when this one is the last. The membership store's read cannot
// ask for one row past its ceiling, so below the ceiling the page is read one
// row longer than it shows and the extra row is the probe; at the ceiling the
// full page is asked again for a single row past its last account.
func (m *product) memberPage(ctx context.Context, tenant string, query authn.MemberQuery) ([]authn.Membership, string, error) {
	limit := query.EffectiveLimit()
	var members []authn.Membership
	hasMore := false
	if limit < authn.MaxPageLimit {
		query.Limit = limit + 1
		read, err := m.memberships.Members(ctx, tenant, query)
		if err != nil {
			return nil, "", err
		}
		if len(read) > limit {
			hasMore = true
			read = read[:limit]
		}
		members = read
	} else {
		query.Limit = limit
		read, err := m.memberships.Members(ctx, tenant, query)
		if err != nil {
			return nil, "", err
		}
		members = read
		if len(read) == limit {
			probe := query
			probe.Limit = 1
			probe.After = read[limit-1].Account.String()
			beyond, err := m.memberships.Members(ctx, tenant, probe)
			if err != nil {
				return nil, "", err
			}
			hasMore = len(beyond) > 0
		}
	}
	if !hasMore || len(members) == 0 {
		return members, "", nil
	}
	return members, members[len(members)-1].Account.String(), nil
}

// rolesByAccount reads every member's roles in one bulk call. The seam is
// optional and the read is allowed to fail: a listing that cannot show roles is
// still a listing, so a failure is a Warn line and empty role arrays rather
// than a failed page.
func (m *product) rolesByAccount(ctx context.Context, tenant string) map[authn.AccountID][]string {
	if m.roleReader == nil || tenant == "" {
		return nil
	}
	roles, err := m.roleReader.MemberRoles(ctx, tenant)
	if err != nil {
		m.logger.Warn(ModuleName+": reading the members' roles failed; the listing is answered without them",
			slog.String("cause", err.Error()))
		return nil
	}
	return roles
}

// memberDisplayName reads the display name of one member account. A missing
// account is the empty name, not an outage: the membership row may outlive the
// account by the time between the page read and this one.
func (m *product) memberDisplayName(ctx context.Context, id authn.AccountID) (string, error) {
	account, err := m.accounts.Get(ctx, id)
	if errors.Is(err, authn.ErrAccountNotFound) {
		return "", nil
	}
	if err != nil {
		return "", m.unavailable("reading a member account", err)
	}
	return account.DisplayName, nil
}

// handleMemberPut answers PUT /admin/tenants/:tenant/members/:account: it adds,
// suspends or resumes a membership. The action is membership.manage.
func (m *product) handleMemberPut(ctx context.Context, in memberWriteInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.admit(id, tenantDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.Account)
	source := in.Source
	if source == "" {
		source = authn.ReasonAdmin
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: in.Tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	// An administrator cannot suspend their own membership: the tenant would
	// lose one of its active administrators and the write is the one way an
	// administrator could stop themselves from governing it. Another
	// administrator may suspend them, and resuming oneself is no refusal. The
	// check is the route's, not the membership store's, so the store's write
	// stays a plain idempotent write.
	if !in.Active && accountID == id.Account() {
		return nil, m.answer("refusing a self-suspension", authn.ErrCannotSuspendSelf)
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
	if err := m.admit(id, tenantDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.Account)
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
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
	if err = m.admit(id, tenantDomain); err != nil {
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
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
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
