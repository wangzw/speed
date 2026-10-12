package admin

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// secretView is one service secret as the management routes show it: its
// identifier, its label, its expiry and whether it was revoked, and never the
// secret.
type secretView struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}

func secretViewOf(s authn.ServiceSecretInfo) secretView {
	return secretView{
		ID:        s.ID,
		Label:     s.Label,
		ExpiresAt: s.ExpiresAt.UTC(),
		Revoked:   !s.RevokedAt.IsZero(),
	}
}

// handleSecretList answers GET /admin/accounts/:id/secrets. The action is
// account.read.
func (m *product) handleSecretList(c *gin.Context) {
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
	secrets, err := m.accounts.ListServiceSecrets(c.Request.Context(), accountID)
	if err != nil {
		m.cause(c, "listing service secrets", err)
		return
	}
	views := make([]secretView, len(secrets))
	for i, s := range secrets {
		views[i] = secretViewOf(s)
	}
	c.JSON(200, gin.H{"secrets": views})
}

// createSecretBody is the request of a service-secret creation.
type createSecretBody struct {
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// handleSecretCreate answers POST /admin/accounts/:id/secrets: it mints a secret
// for a service account, handed over once and never again. The action is
// client.manage and the actor needs a fresh local multi-factor authentication.
func (m *product) handleSecretCreate(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	var body createSecretBody
	if !mount.Decode(c, &body) {
		return
	}
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionClientManage, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.require(c, id, authn.FreshLocalMulti(m.settings.recentWindow())) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionServiceKeyCreate, string(accountID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	secretID, secret, err := m.accountAdm.CreateServiceSecret(c.Request.Context(), accountID, body.Label, body.ExpiresAt)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionServiceKeyCreate, string(accountID), actionID))
		m.answer(c, "creating a service secret", err)
		return
	}
	expires := time.Time{}
	if body.ExpiresAt != nil {
		expires = body.ExpiresAt.UTC()
	}
	c.JSON(201, gin.H{"id": secretID, "label": body.Label, "secret": secret, "expires_at": expires})
}

// handleSecretRevoke answers DELETE /admin/accounts/:id/secrets/:secret. The
// action is client.manage and the actor needs a fresh local multi-factor
// authentication.
func (m *product) handleSecretRevoke(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	secretID := c.Param("secret")
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionClientManage, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.require(c, id, authn.FreshLocalMulti(m.settings.recentWindow())) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionServiceKeyRevoke, string(accountID),
		map[string]string{detailActionID: actionID, detailSecretID: secretID})) {
		return
	}
	if err := m.accountAdm.RevokeServiceSecret(c.Request.Context(), accountID, secretID); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionServiceKeyRevoke, string(accountID), actionID))
		m.answer(c, "revoking a service secret", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}
