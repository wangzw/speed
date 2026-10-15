package invite

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// issueBody is the request of an issue. ExpiresAt is optional; left out, the
// configured lifetime applies.
type issueBody struct {
	Email     string     `json:"email"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// handleIssue answers POST /admin/tenants/:tenant/invitations: it issues an
// invitation and returns its view together with the one-time token. The action
// is membership.manage, decided in the tenant in the path.
func (p *product) handleIssue(c *gin.Context) {
	id, ok := p.caller(c)
	if !ok || !p.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	if tenant == "" {
		p.bad(c)
		return
	}
	if !p.asks(c, authn.Decision{Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant}) {
		return
	}
	// The decision comes first, and only then is the body parsed and shaped:
	// an unauthorized caller learns nothing about the body it sent.
	var body issueBody
	if !mount.Decode(c, &body) {
		return
	}
	if body.Email == "" {
		p.bad(c)
		return
	}
	in := IssueInvite{Tenant: tenant, Email: body.Email}
	if body.ExpiresAt != nil {
		in.ExpiresAt = *body.ExpiresAt
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionIssue, tenant,
		map[string]string{detailActionID: actionID, detailTenant: tenant})) {
		return
	}
	issued, err := p.Issue(c.Request.Context(), in)
	if err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionIssue, tenant, actionID))
		p.answer(c, "issuing an invitation", err)
		return
	}
	c.JSON(201, issuedView{Invitation: invitationViewOf(issued.Invitation), Token: issued.Token})
}

// handleList answers GET /admin/tenants/:tenant/invitations: one page of the
// tenant's invitations. The action is membership.manage, and a read writes no
// audit row.
func (p *product) handleList(c *gin.Context) {
	id, ok := p.caller(c)
	if !ok || !p.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	if !p.asks(c, authn.Decision{Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant}) {
		return
	}
	// The page's shape is judged after the decision, so a refused caller
	// learns nothing about what a well-formed query looks like.
	var page authn.Page
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			p.bad(c)
			return
		}
		page.Limit = n
	}
	page.After = c.Query("after")
	invitations, next, err := p.List(c.Request.Context(), tenant, page)
	if err != nil {
		p.answer(c, "listing invitations", err)
		return
	}
	views := make([]invitationView, len(invitations))
	for i, inv := range invitations {
		views[i] = invitationViewOf(inv)
	}
	c.JSON(200, gin.H{"invitations": views, "next_cursor": next})
}

// handleRevoke answers DELETE /admin/tenants/:tenant/invitations/:id: it
// revokes a pending invitation. The action is membership.manage, and the
// invitation's identifier is the decision's target.
func (p *product) handleRevoke(c *gin.Context) {
	id, ok := p.caller(c)
	if !ok || !p.admit(c, id) {
		return
	}
	tenant := c.Param("tenant")
	invitation := c.Param("id")
	if !p.asks(c, authn.Decision{
		Action: authn.ActionMembershipManage, Actor: id, Tenant: tenant, Target: invitation,
	}) {
		return
	}
	if invitation == "" {
		p.bad(c)
		return
	}
	actionID := newActionID()
	if !p.auditRequired(c, mandatory(actionRevoke, invitation,
		map[string]string{detailActionID: actionID, detailTenant: tenant})) {
		return
	}
	if err := p.Revoke(c.Request.Context(), tenant, invitation); err != nil {
		p.auditBestEffort(c.Request.Context(), failedAudit(actionRevoke, invitation, actionID))
		p.answer(c, "revoking an invitation", err)
		return
	}
	p.noContent(c)
}
