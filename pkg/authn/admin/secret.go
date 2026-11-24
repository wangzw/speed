package admin

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func secretViewOf(s authn.ServiceSecretInfo) AuthnAdminSecret {
	return AuthnAdminSecret{
		ID:        s.ID,
		Label:     s.Label,
		ExpiresAt: utcPtr(s.ExpiresAt),
		Revoked:   !s.RevokedAt.IsZero(),
	}
}

func (m *product) handleSecretList(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionAccountRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	secrets, err := m.accounts.ListServiceSecrets(ctx, accountID)
	if err != nil {
		return nil, m.unavailable("listing service secrets", err)
	}
	views := make([]AuthnAdminSecret, len(secrets))
	for i, s := range secrets {
		views[i] = secretViewOf(s)
	}
	return speedhttp.JSONReply(200, AuthnAdminSecretList{Secrets: views}), nil
}

func (m *product) handleSecretCreate(ctx context.Context, in secretCreateInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionClientManage, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err = m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionServiceKeyCreate, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	secretID, secret, err := m.accountAdm.CreateServiceSecret(ctx, accountID, in.Label, in.ExpiresAt)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionServiceKeyCreate, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "creating a service secret", err)
	}
	var expires *time.Time
	if in.ExpiresAt != nil {
		expires = utcPtr(*in.ExpiresAt)
	}
	return speedhttp.JSONReply(201, AuthnAdminSecretCreated{
		ID: secretID, Label: in.Label, Secret: secret, ExpiresAt: expires,
	}), nil
}

func (m *product) handleSecretRevoke(ctx context.Context, in secretRevokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err := m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionClientManage, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err := m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionServiceKeyRevoke, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailSecretID: in.Secret})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.RevokeServiceSecret(ctx, accountID, in.Secret); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionServiceKeyRevoke, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "revoking a service secret", err)
	}
	return speedhttp.EmptyReply(204), nil
}
