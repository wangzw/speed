package admin

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func factorViewOf(f authn.FactorRecord) AuthnAdminFactor {
	return AuthnAdminFactor{Kind: string(f.Kind), Label: f.Label, Status: factorStatusName(f.Status)}
}

func factorStatusName(s authn.FactorStatus) string {
	switch s {
	case authn.FactorPending:
		return "pending"
	case authn.FactorActive:
		return "active"
	case authn.FactorDisabled:
		return "disabled"
	}
	return "unknown"
}

func (m *product) handleFactorList(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	factors, err := m.mfa.Factors(ctx, accountID)
	if err != nil {
		return nil, m.unavailable("listing factors", err)
	}
	views := make([]AuthnAdminFactor, len(factors))
	for i, f := range factors {
		views[i] = factorViewOf(f)
	}
	return speedhttp.JSONReply(200, AuthnAdminFactorList{Factors: views}), nil
}

func (m *product) handleMFARequired(ctx context.Context, in mfaFlagInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionAccountUpdate, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailChange: "mfa-required"})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.SetMFARequired(ctx, accountID, in.Required); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "changing the required flag", err)
	}
	return speedhttp.EmptyReply(204), nil
}

func (m *product) handleMFAReset(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionMFAReset, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err = m.requireAdministrator(ctx, id); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionMFAReset, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	hasWay, err := m.accountAdm.HasLoginWayOtherThanPasskey(ctx, accountID)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMFAReset, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "reading the account's ways to log in", err)
	}
	if !hasWay {
		m.auditBestEffort(ctx, failedAudit(actionMFAReset, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "resetting factors", authn.ErrLastLoginMethod)
	}
	removed, err := m.mfa.Reset(ctx, accountID, authn.ReasonAdmin)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMFAReset, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "resetting factors", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminRemoved{Removed: removed}), nil
}

func (m *product) handlePasswordReset(ctx context.Context, in passwordResetInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if in.Password == "" {
		return nil, m.bad()
	}
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	if err = m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err = m.requireAdministrator(ctx, id); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountAuthReset, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	err = m.passwords.Set(ctx, accountID, in.Password, authn.SetPasswordOptions{MustChange: true})
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountAuthReset, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "resetting a password", err)
	}
	m.revokeAfterPassword(ctx, accountID)
	return speedhttp.EmptyReply(204), nil
}

func (m *product) revokeAfterPassword(ctx context.Context, id authn.AccountID) {
	if m.sessions == nil {
		m.logger.Warn(ModuleName + ": a password was reset with no session store in this assembly, so no session was revoked")
		return
	}
	if _, err := m.sessions.RevokeAccount(ctx, id, "", authn.ReasonPasswordChange); err != nil {
		m.logger.Error(ModuleName+": revoking the sessions after a password reset failed",
			slog.String("cause", err.Error()))
	}
}

func (m *product) requireAdministrator(ctx context.Context, id authn.Identity) error {
	if account := id.Account(); account == "" || account.IsStatic() {
		return stepUpRequired(id, authn.FreshLocalMulti(m.settings.recentWindow()))
	}
	if err := m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return err
	}
	if !hasNonSMSSecond(id.Methods()) {
		return stepUpRequired(id, authn.FreshLocalMulti(m.settings.recentWindow()))
	}
	seasoned, err := m.mfa.Seasoned(ctx, id.Account())
	if err != nil {
		return m.unavailable("reading whether the actor has a seasoned factor", err)
	}
	if !seasoned {
		return authnFailure(403, mount.CodeFactorNotSeasoned)
	}
	return nil
}

func hasNonSMSSecond(methods []authn.Method) bool {
	for _, method := range methods {
		if method == authn.MethodTOTP || method == authn.MethodWebAuthn {
			return true
		}
	}
	return false
}
