package admin

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func memberViewOf(mem authn.Membership, displayName, username string, roles []string, tenantName string) AuthnAdminMember {
	return AuthnAdminMember{
		Account:     string(mem.Account),
		DisplayName: displayName,
		Username:    username,
		Tenant:      tenantName,
		Active:      mem.Active,
		SSOExempt:   mem.SSOExempt,
		Source:      mem.Source,
		Roles:       roleNames(roles),
		JoinedAt:    mem.CreatedAt.UTC(),
	}
}

func roleNames(names []string) []string {
	out := make([]string, 0, len(names))
	out = append(out, names...)
	slices.Sort(out)
	return slices.Compact(out)
}

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
	var members []authn.Membership
	var next string
	var roles map[authn.AccountID][]string
	if in.Role == "" {
		if members, next, err = m.memberPage(ctx, in.Tenant, query); err != nil {
			return AuthnAdminMemberList{}, m.unavailable("listing members", err)
		}
		roles = m.rolesByAccount(ctx, in.Tenant)
	} else {
		if members, next, roles, err = m.memberPageByRole(ctx, in.Tenant, query, in.Role); err != nil {
			return AuthnAdminMemberList{}, m.unavailable("filtering members by role", err)
		}
	}
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnAdminMemberList{}, m.unavailable("reading the tenant's name", nameErr)
	}
	ids := make([]authn.AccountID, len(members))
	for i, mem := range members {
		ids[i] = mem.Account
	}
	var identifiers map[authn.AccountID][]authn.Identifier
	if len(ids) > 0 {
		if identifiers, err = m.accounts.IdentifiersMany(ctx, ids); err != nil {
			return AuthnAdminMemberList{}, m.unavailable("listing the members' identifiers", err)
		}
	}
	views := make([]AuthnAdminMember, len(members))
	for i, mem := range members {
		name, err := m.memberDisplayName(ctx, mem.Account)
		if err != nil {
			return AuthnAdminMemberList{}, err
		}
		views[i] = memberViewOf(mem, name, usernameOf(identifiers[mem.Account]), roles[mem.Account], tenantName)
	}
	return AuthnAdminMemberList{Members: views, NextCursor: next}, nil
}

func usernameOf(identifiers []authn.Identifier) string {
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierUsername && identifier.Value != "" {
			return identifier.Value
		}
	}
	return ""
}

// The store's read cannot exceed its own page ceiling: below the ceiling the
// page is read one row longer than it shows, at the ceiling it probes past the
// last account.
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

func (m *product) memberPageByRole(ctx context.Context, tenant string, query authn.MemberQuery, role string) ([]authn.Membership, string, map[authn.AccountID][]string, error) {
	if m.roleReader == nil {
		return nil, "", nil, errors.New("the assembly delivers no member role reader, so the role filter cannot run")
	}
	roles, err := m.roleReader.MemberRoles(ctx, tenant)
	if err != nil {
		return nil, "", nil, err
	}
	limit := query.EffectiveLimit()
	wanted := limit + 1
	matched := make([]authn.Membership, 0, wanted)
	after := query.After
	for len(matched) < wanted {
		page := query
		page.After = after
		page.Limit = limit
		read, err := m.memberships.Members(ctx, tenant, page)
		if err != nil {
			return nil, "", nil, err
		}
		if len(read) == 0 {
			break
		}
		for _, mem := range read {
			if slices.Contains(roles[mem.Account], role) {
				matched = append(matched, mem)
				if len(matched) == wanted {
					break
				}
			}
		}
		after = read[len(read)-1].Account.String()
		if len(read) < limit {
			break
		}
	}
	if len(matched) > limit {
		return matched[:limit], matched[limit-1].Account.String(), roles, nil
	}
	return matched, "", roles, nil
}

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
	if !in.Active && accountID == id.Account() {
		return nil, m.answer(ctx, "refusing a self-suspension", authn.ErrCannotSuspendSelf)
	}
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipSet, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	mem := authn.Membership{
		Account: accountID, Tenant: in.Tenant, Active: in.Active,
		Source: source, SSOExempt: in.SSOExempt,
	}
	if err := m.memberships.Put(ctx, mem); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipSet, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "writing a membership", err)
	}
	return speedhttp.EmptyReply(204), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipRemove, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailTenant: tenantName})); err != nil {
		return nil, err
	}
	if err := m.memberships.Remove(ctx, accountID, in.Tenant); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipRemove, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "removing a membership", err)
	}
	return speedhttp.EmptyReply(204), nil
}

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
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return nil, m.unavailable("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionMembershipSet, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailTenant: tenantName, detailChange: "sso-exempt"})); err != nil {
		return nil, err
	}
	mem.SSOExempt = in.SSOExempt
	if err := m.memberships.Put(ctx, mem); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMembershipSet, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "writing the SSO exemption", err)
	}
	return speedhttp.EmptyReply(204), nil
}
