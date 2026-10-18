package admin

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// sessionView is one session as the management routes show it: no credential and
// nothing a caller could use to take the session over. Current marks the row
// whose session is the caller's own, the same flag the self-service list
// carries.
type sessionView struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Assurance       uint8     `json:"assurance"`
	AuthTime        time.Time `json:"auth_time"`
	ImpersonationID string    `json:"impersonation_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Revoked         bool      `json:"revoked"`
	Current         bool      `json:"current"`
}

func sessionViewOf(s authn.SessionInfo, current authn.SessionID) sessionView {
	return sessionView{
		ID:              string(s.ID),
		Kind:            string(s.Kind),
		Assurance:       uint8(s.Assurance),
		AuthTime:        s.AuthTime.UTC(),
		ImpersonationID: s.ImpersonationID,
		CreatedAt:       s.CreatedAt.UTC(),
		LastSeenAt:      s.LastSeenAt.UTC(),
		ExpiresAt:       s.ExpiresAt.UTC(),
		Revoked:         s.Revoked,
		Current:         s.ID == current,
	}
}

// handleAccountSessions answers GET /admin/accounts/:id/sessions. The action is
// session.read.
func (m *product) handleAccountSessions(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionSessionRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	sessions, err := m.sessions.List(c.Request.Context(), accountID)
	if err != nil {
		m.cause(c, "listing sessions", err)
		return
	}
	views := make([]sessionView, len(sessions))
	for i, s := range sessions {
		views[i] = sessionViewOf(s, id.Session())
	}
	c.JSON(200, gin.H{"sessions": views})
}

// handleRevokeAccountSessions answers POST
// /admin/accounts/:id/revoke-sessions: it revokes the account's unrevoked
// browser and token sessions, writing one audit row per session before the
// action. The set is read under the statement's own condition, so a session past
// its deadline is named like any other and no revocation happens without a row
// to account for it. The action is session.revoke.
func (m *product) handleRevokeAccountSessions(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionSessionRevoke, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	ctx := c.Request.Context()
	// The audit row of a revocation names the session it revoked, and the row is
	// written before the action, so the set is read first — under the condition
	// the statement itself uses, so the rows and the revocation name one set. An
	// account with nothing in that set has nothing revoked and nothing written.
	targets, err := m.sessions.RevocableSessions(ctx, accountID)
	if err != nil {
		m.cause(c, "listing an account's revocable sessions", err)
		return
	}
	actionID := newActionID()
	for _, target := range targets {
		if !m.auditRequired(c, mandatory(actionSessionRevoke, string(target),
			map[string]string{detailActionID: actionID})) {
			return
		}
	}
	revoked, err := m.sessions.RevokeAccount(ctx, accountID, "", authn.ReasonAdmin)
	if err != nil {
		for _, target := range targets {
			m.auditBestEffort(ctx, failedAudit(actionSessionRevoke, string(target), actionID))
		}
		m.answer(c, "revoking an account's sessions", err)
		return
	}
	c.JSON(200, gin.H{"revoked": len(revoked)})
}

// handleSessionRevoke answers DELETE /admin/sessions/:id: it reads the session
// to learn the account it belongs to, then revokes it. The action is
// session.revoke and the object the Decision carries is the session, the
// TargetAccount the account it belongs to.
func (m *product) handleSessionRevoke(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	sessionID := authn.SessionID(c.Param("id"))
	info, err := m.sessions.Get(c.Request.Context(), sessionID)
	if err != nil {
		m.answer(c, "reading a session", err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionSessionRevoke, Actor: id,
		Target: string(sessionID), TargetAccount: string(info.Account),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionSessionRevoke, string(sessionID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	if err := m.sessions.Revoke(c.Request.Context(), sessionID, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionSessionRevoke, string(sessionID), actionID))
		m.answer(c, "revoking a session", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}
