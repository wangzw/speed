package admin

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// factorView is one factor as the management routes show it: its kind, its
// label and its status, and nothing of its credential.
type factorView struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

func factorViewOf(f authn.FactorRecord) factorView {
	return factorView{Kind: string(f.Kind), Label: f.Label, Status: factorStatusName(f.Status)}
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
func (m *product) handleFactorList(c *gin.Context) {
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
		Action: authn.ActionAccountRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	factors, err := m.mfa.Factors(c.Request.Context(), accountID)
	if err != nil {
		m.cause(c, "listing factors", err)
		return
	}
	views := make([]factorView, len(factors))
	for i, f := range factors {
		views[i] = factorViewOf(f)
	}
	c.JSON(200, gin.H{"factors": views})
}

// mfaRequiredBody is the request of a change of the required flag.
type mfaRequiredBody struct {
	Required bool `json:"required"`
}

// handleMFARequired answers POST /admin/accounts/:id/mfa-required. The action is
// account.update.
func (m *product) handleMFARequired(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	var body mfaRequiredBody
	if !mount.Decode(c, &body) {
		return
	}
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "mfa-required"})) {
		return
	}
	if err := m.accountAdm.SetMFARequired(c.Request.Context(), accountID, body.Required); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountUpdate, string(accountID), actionID))
		m.answer(c, "changing the required flag", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// handleMFAReset answers POST /admin/accounts/:id/mfa-reset: it disables the
// account's factors. The action is mfa.reset; the actor needs a fresh, local,
// non-SMS multi-factor authentication and a seasoned factor of their own.
func (m *product) handleMFAReset(c *gin.Context) {
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
		Action: authn.ActionMFAReset, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.requireAdministrator(c, id) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionMFAReset, string(accountID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	removed, err := m.mfa.Reset(c.Request.Context(), accountID, authn.ReasonAdmin)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionMFAReset, string(accountID), actionID))
		m.answer(c, "resetting factors", err)
		return
	}
	c.JSON(200, gin.H{"removed": removed})
}

// passwordBody is the request of an administrator's password reset. The
// temporary password is never echoed, logged or audited.
type passwordBody struct {
	Password string `json:"password"`
}

// handlePasswordReset answers POST /admin/accounts/:id/password: an
// administrator sets a temporary password that must be changed at the next
// login, then every session of the account is revoked. The action is
// account.update; the actor needs a fresh, local, non-SMS multi-factor
// authentication and a seasoned factor of their own.
func (m *product) handlePasswordReset(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	var body passwordBody
	if !mount.Decode(c, &body) {
		return
	}
	if body.Password == "" {
		m.bad(c)
		return
	}
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.requireAdministrator(c, id) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountAuthReset, string(accountID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	err := m.passwords.Set(c.Request.Context(), accountID, body.Password, authn.SetPasswordOptions{MustChange: true})
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountAuthReset, string(accountID), actionID))
		m.answer(c, "resetting a password", err)
		return
	}
	m.revokeAfterPassword(c.Request.Context(), accountID)
	c.Status(204)
	c.Writer.WriteHeaderNow()
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
func (m *product) requireAdministrator(c *gin.Context, id authn.Identity) bool {
	if account := id.Account(); account == "" || account.IsStatic() {
		mount.StepUpRequired(c, id, authn.FreshLocalMulti(m.settings.recentWindow()))
		return false
	}
	if !m.require(c, id, authn.FreshLocalMulti(m.settings.recentWindow())) {
		return false
	}
	if !hasNonSMSSecond(id.Methods()) {
		mount.StepUpRequired(c, id, authn.FreshLocalMulti(m.settings.recentWindow()))
		return false
	}
	seasoned, err := m.mfa.Seasoned(c.Request.Context(), id.Account())
	if err != nil {
		m.cause(c, "reading whether the actor has a seasoned factor", err)
		return false
	}
	if !seasoned {
		mount.Forbidden(c, mount.CodeFactorNotSeasoned)
		return false
	}
	return true
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
