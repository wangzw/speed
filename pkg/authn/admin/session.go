package admin

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// sessionView is one session as the management routes show it: no credential and
// nothing a caller could use to take the session over.
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
}

func sessionViewOf(s authn.SessionInfo) sessionView {
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
		views[i] = sessionViewOf(s)
	}
	c.JSON(200, gin.H{"sessions": views})
}

// handleRevokeAccountSessions answers POST
// /admin/accounts/:id/revoke-sessions: it revokes every browser and token
// session of the account. The action is session.revoke.
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
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionSessionRevoke, string(accountID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	n, err := m.sessions.RevokeAccount(c.Request.Context(), accountID, "", authn.ReasonAdmin)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionSessionRevoke, string(accountID), actionID))
		m.answer(c, "revoking an account's sessions", err)
		return
	}
	c.JSON(200, gin.H{"revoked": n})
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
