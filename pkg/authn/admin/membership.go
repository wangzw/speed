package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// memberView is one membership as the management routes show it.
type memberView struct {
	Account   string `json:"account"`
	Tenant    string `json:"tenant"`
	Active    bool   `json:"active"`
	SSOExempt bool   `json:"sso_exempt"`
	Source    string `json:"source"`
}

func memberViewOf(mem authn.Membership) memberView {
	return memberView{
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
func (m *product) handleMemberList(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	var page authn.Page
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			m.bad(c)
			return
		}
		page.Limit = n
	}
	page.After = c.Query("after")
	if !m.asks(c, authn.Decision{Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant}) {
		return
	}
	members, err := m.memberships.Members(c.Request.Context(), tenant, page)
	if err != nil {
		m.cause(c, "listing members", err)
		return
	}
	views := make([]memberView, len(members))
	for i, mem := range members {
		views[i] = memberViewOf(mem)
	}
	c.JSON(200, gin.H{"members": views})
}

// memberBody is the request of a membership write.
type memberBody struct {
	Active    bool   `json:"active"`
	Source    string `json:"source"`
	SSOExempt bool   `json:"sso_exempt"`
}

// handleMemberPut answers PUT /admin/tenants/:tenant/members/:account: it adds,
// suspends or resumes a membership. The action is membership.manage.
func (m *product) handleMemberPut(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	accountID := authn.AccountID(c.Param("account"))
	var body memberBody
	if !mount.Decode(c, &body) {
		return
	}
	source := body.Source
	if source == "" {
		source = authn.ReasonAdmin
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionMembershipSet, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: tenant})) {
		return
	}
	mem := authn.Membership{
		Account: accountID, Tenant: tenant, Active: body.Active,
		Source: source, SSOExempt: body.SSOExempt,
	}
	if err := m.memberships.Put(c.Request.Context(), mem); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionMembershipSet, string(accountID), actionID))
		m.answer(c, "writing a membership", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// handleMemberRemove answers DELETE /admin/tenants/:tenant/members/:account. The
// action is membership.manage.
func (m *product) handleMemberRemove(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	accountID := authn.AccountID(c.Param("account"))
	if !m.asks(c, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionMembershipRemove, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: tenant})) {
		return
	}
	if err := m.memberships.Remove(c.Request.Context(), accountID, tenant); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionMembershipRemove, string(accountID), actionID))
		m.answer(c, "removing a membership", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// ssoExemptBody is the request of a change of the SSO exemption flag.
type ssoExemptBody struct {
	SSOExempt bool `json:"sso_exempt"`
}

// handleMemberSSOExempt answers PUT
// /admin/tenants/:tenant/members/:account/sso-exempt: it sets or clears the
// emergency exemption from the tenant's enforced SSO. The action is
// membership.manage.
func (m *product) handleMemberSSOExempt(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	accountID := authn.AccountID(c.Param("account"))
	var body ssoExemptBody
	if !mount.Decode(c, &body) {
		return
	}
	mem, found, err := m.memberships.Membership(c.Request.Context(), tenant, accountID)
	if err != nil {
		m.cause(c, "reading a membership", err)
		return
	}
	if !found {
		m.notFound(c)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionMembershipSet, string(accountID),
		map[string]string{detailActionID: actionID, detailTenant: tenant, detailChange: "sso-exempt"})) {
		return
	}
	mem.SSOExempt = body.SSOExempt
	if err := m.memberships.Put(c.Request.Context(), mem); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionMembershipSet, string(accountID), actionID))
		m.answer(c, "writing the SSO exemption", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}
