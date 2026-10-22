package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// sessionViewOf builds one session as the management routes show it: no
// credential and nothing a caller could use to take the session over. Current
// marks the row whose session is the caller's own, the same flag the
// self-service list carries.
func sessionViewOf(s authn.SessionInfo, current authn.SessionID) AuthnAdminSession {
	return AuthnAdminSession{
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
func (m *product) handleAccountSessions(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionSessionRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	sessions, err := m.sessions.List(ctx, accountID)
	if err != nil {
		return nil, m.unavailable("listing sessions", err)
	}
	views := make([]AuthnAdminSession, len(sessions))
	for i, s := range sessions {
		views[i] = sessionViewOf(s, id.Session())
	}
	return speedhttp.JSONReply(200, AuthnAdminSessionList{Sessions: views}), nil
}

// handleRevokeAccountSessions answers POST
// /admin/accounts/:id/revoke-sessions: it revokes the account's unrevoked
// browser and token sessions, writing one audit row per session before the
// action. The set is read under the statement's own condition, so a session past
// its deadline is named like any other and no revocation happens without a row
// to account for it. The action is session.revoke.
func (m *product) handleRevokeAccountSessions(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionSessionRevoke, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	// The audit row of a revocation names the session it revoked, and the row is
	// written before the action, so the set is read first — under the condition
	// the statement itself uses, so the rows and the revocation name one set. An
	// account with nothing in that set has nothing revoked and nothing written.
	targets, err := m.sessions.RevocableSessions(ctx, accountID)
	if err != nil {
		return nil, m.unavailable("listing an account's revocable sessions", err)
	}
	actionID := newActionID()
	for _, target := range targets {
		if err = m.auditRequired(ctx, mandatory(actionSessionRevoke, string(target),
			map[string]string{detailActionID: actionID})); err != nil {
			return nil, err
		}
	}
	revoked, err := m.sessions.RevokeAccount(ctx, accountID, "", authn.ReasonAdmin)
	if err != nil {
		for _, target := range targets {
			m.auditBestEffort(ctx, failedAudit(actionSessionRevoke, string(target), actionID))
		}
		return m.replyFailure("revoking an account's sessions", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminRevoked{Revoked: len(revoked)}), nil
}

// handleSessionRevoke answers DELETE /admin/sessions/:id: it reads the session
// to learn the account it belongs to, then revokes it. The action is
// session.revoke and the object the Decision carries is the session, the
// TargetAccount the account it belongs to.
func (m *product) handleSessionRevoke(ctx context.Context, in sessionRevokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	sessionID := authn.SessionID(in.ID)
	info, err := m.sessions.Get(ctx, sessionID)
	if err != nil {
		return m.replyFailure("reading a session", err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionSessionRevoke, Actor: id,
		Target: string(sessionID), TargetAccount: string(info.Account),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionSessionRevoke, string(sessionID),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	if err := m.sessions.Revoke(ctx, sessionID, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionSessionRevoke, string(sessionID), actionID))
		return m.replyFailure("revoking a session", err)
	}
	return speedhttp.EmptyReply(204), nil
}
