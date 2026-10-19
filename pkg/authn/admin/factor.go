package admin

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// factorViewOf builds one factor as the management routes show it: its kind,
// its label and its status, and nothing of its credential.
func factorViewOf(f authn.FactorRecord) AuthnAdminFactor {
	return AuthnAdminFactor{Kind: string(f.Kind), Label: f.Label, Status: factorStatusName(f.Status)}
}

// factorStatusName is the status's name as the view writes it.
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

// handleFactorList answers GET /admin/accounts/:id/factors. The action is
// account.read.
func (m *product) handleFactorList(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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

// handleMFARequired answers POST /admin/accounts/:id/mfa-required. The action is
// account.update.
func (m *product) handleMFARequired(ctx context.Context, in mfaFlagInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if _, err := m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "mfa-required"})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.SetMFARequired(ctx, accountID, in.Required); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, string(accountID), actionID))
		return m.replyFailure("changing the required flag", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleMFAReset answers POST /admin/accounts/:id/mfa-reset: it disables the
// account's factors. The action is mfa.reset; the actor needs a fresh, local,
// non-SMS multi-factor authentication and a seasoned factor of their own.
func (m *product) handleMFAReset(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionMFAReset, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err = m.requireAdministrator(ctx, id); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionMFAReset, string(accountID),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	removed, err := m.mfa.Reset(ctx, accountID, authn.ReasonAdmin)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionMFAReset, string(accountID), actionID))
		return m.replyFailure("resetting factors", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminRemoved{Removed: removed}), nil
}

// handlePasswordReset answers POST /admin/accounts/:id/password: an
// administrator sets a temporary password that must be changed at the next
// login, then every session of the account is revoked. The action is
// account.update; the actor needs a fresh, local, non-SMS multi-factor
// authentication and a seasoned factor of their own.
func (m *product) handlePasswordReset(ctx context.Context, in passwordResetInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	if in.Password == "" {
		return nil, m.bad()
	}
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
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
	if err = m.auditRequired(ctx, mandatory(actionAccountAuthReset, string(accountID),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	err = m.passwords.Set(ctx, accountID, in.Password, authn.SetPasswordOptions{MustChange: true})
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountAuthReset, string(accountID), actionID))
		return m.replyFailure("resetting a password", err)
	}
	m.revokeAfterPassword(ctx, accountID)
	return speedhttp.EmptyReply(204), nil
}

// revokeAfterPassword revokes every session of the account whose password was
// reset. It runs after the write has landed, so a failure does not undo the
// reset: it is logged and the sessions are left for the account's own next
// login to replace. With no session store in the assembly there is nothing to
// revoke, and that is logged too.
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

// requireAdministrator holds a high-privilege actor to a fresh, local,
// non-SMS multi-factor authentication and a seasoned factor of their own, and
// refuses a subject of the static credential table outright, which has neither
// factors nor an account to answer for. The static-subject refusal and the
// strength conditions are a step-up 401; a missing seasoned factor is the 403
// the step-up cannot clear.
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

// hasNonSMSSecond reports whether the method set carries a second factor that
// is not SMS: a one-time code or a passkey. SMS is a restricted authenticator
// and does not count for the highest-privilege actions.
func hasNonSMSSecond(methods []authn.Method) bool {
	for _, method := range methods {
		if method == authn.MethodTOTP || method == authn.MethodWebAuthn {
			return true
		}
	}
	return false
}
