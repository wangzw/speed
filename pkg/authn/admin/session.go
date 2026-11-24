package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

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

func (m *product) handleAccountSessions(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure(ctx, "reading an object", err)
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

func (m *product) handleRevokeAccountSessions(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure(ctx, "reading an object", err)
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
		return m.replyFailure(ctx, "revoking an account's sessions", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminRevoked{Revoked: len(revoked)}), nil
}

func (m *product) handleSessionRevoke(ctx context.Context, in sessionRevokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	sessionID := authn.SessionID(in.ID)
	info, err := m.sessions.Get(ctx, sessionID)
	if err != nil {
		return m.replyFailure(ctx, "reading a session", err)
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
		return m.replyFailure(ctx, "revoking a session", err)
	}
	return speedhttp.EmptyReply(204), nil
}
