package admin

import (
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// accountView is one account as the management routes show it. It is built field
// by field so that a field added to the account later does not reach a client by
// being there.
type accountView struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Status      string            `json:"status"`
	DisplayName string            `json:"display_name"`
	MFARequired bool              `json:"mfa_required"`
	Protected   bool              `json:"protected"`
	PasswordSet bool              `json:"password_set"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

func accountViewOf(a authn.Account) accountView {
	return accountView{
		ID:          string(a.ID),
		Kind:        string(a.Kind),
		Status:      a.Status.String(),
		DisplayName: a.DisplayName,
		MFARequired: a.MFARequired,
		Protected:   a.Protected,
		PasswordSet: a.PasswordSet,
		Attributes:  a.Attributes,
		CreatedAt:   a.CreatedAt.UTC(),
	}
}

// accountListParams is the set of query parameters the account list route
// accepts, a closed set. A name not among them is a shape the module refuses, so
// that a misspelled filter is reported rather than swallowed by a read that
// ignores it. The set is what the OpenAPI fragment declares and what accountQuery
// reads.
var accountListParams = map[string]bool{
	"status":              true,
	"kind":                true,
	"identifier_prefix":   true,
	"identifier_kind":     true,
	"display_name_prefix": true,
	"after":               true,
	"limit":               true,
}

// handleAccountList answers GET /admin/accounts: one page of accounts, filtered
// by status, kind and an identifier prefix. The action is account.read.
func (m *product) handleAccountList(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	q, ok := m.accountQuery(c)
	if !ok {
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionAccountRead, Actor: id}) {
		return
	}
	accounts, next, err := m.accounts.List(c.Request.Context(), q)
	if err != nil {
		m.cause(c, "listing accounts", err)
		return
	}
	views := make([]accountView, len(accounts))
	for i, a := range accounts {
		views[i] = accountViewOf(a)
	}
	c.JSON(200, gin.H{"accounts": views, "next_cursor": next})
}

// accountQuery reads the list's query parameters into a read query. A parameter
// the route does not accept, a filter it does not know, an identifier prefix
// without a kind, or a display-name prefix that is not text is a 400. An empty
// value of an accepted filter — including an empty display_name_prefix — is no
// filter, the same reading status, kind and after take.
func (m *product) accountQuery(c *gin.Context) (authn.AccountQuery, bool) {
	if !onlyAccepted(c, accountListParams) {
		m.bad(c)
		return authn.AccountQuery{}, false
	}
	var q authn.AccountQuery
	if s := c.Query("status"); s != "" {
		st, ok := statusOf(s)
		if !ok {
			m.bad(c)
			return authn.AccountQuery{}, false
		}
		q.Status = &st
	}
	if k := c.Query("kind"); k != "" {
		kind := authn.AccountKind(k)
		if !kind.Valid() {
			m.bad(c)
			return authn.AccountQuery{}, false
		}
		q.Kind = kind
	}
	prefix, kind := c.Query("identifier_prefix"), c.Query("identifier_kind")
	if (prefix == "") != (kind == "") {
		m.bad(c)
		return authn.AccountQuery{}, false
	}
	if prefix != "" {
		ik := authn.IdentifierKind(kind)
		if !identifierKindValid(ik) {
			m.bad(c)
			return authn.AccountQuery{}, false
		}
		q.IdentifierKind, q.IdentifierPrefix = ik, prefix
	}
	if p := c.Query("display_name_prefix"); p != "" {
		// A display name is stored as text, so a prefix that is not text is a
		// shape the route refuses here rather than a byte sequence the read would
		// hand to the database, which rejects it and answers an outage.
		if !utf8.ValidString(p) {
			m.bad(c)
			return authn.AccountQuery{}, false
		}
		q.DisplayNamePrefix = p
	}
	q.After = c.Query("after")
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			m.bad(c)
			return authn.AccountQuery{}, false
		}
		q.Limit = n
	}
	return q, true
}

// onlyAccepted reports whether every query parameter on the request is in the
// accepted set. A nil set accepts none.
func onlyAccepted(c *gin.Context, accepted map[string]bool) bool {
	for key := range c.Request.URL.Query() {
		if !accepted[key] {
			return false
		}
	}
	return true
}

// createBody is the request of a create.
type createBody struct {
	Kind             string            `json:"kind"`
	DisplayName      string            `json:"display_name"`
	Username         string            `json:"username"`
	Email            string            `json:"email"`
	EmailVerifiedVia string            `json:"email_verified_via"`
	Phone            string            `json:"phone"`
	PhoneVerifiedVia string            `json:"phone_verified_via"`
	Password         string            `json:"password"`
	Attributes       map[string]string `json:"attributes"`
	MFARequired      bool              `json:"mfa_required"`
}

// handleAccountCreate answers POST /admin/accounts. The action is
// account.create, and a service account additionally needs client.manage. An
// email address and a phone number are written with a way an administrator may
// state — the administrator's own assertion or an import record — and never a
// proof of ownership, which is not the administrator's to make.
func (m *product) handleAccountCreate(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	var body createBody
	if !mount.Decode(c, &body) {
		return
	}
	kind := authn.AccountKind(body.Kind)
	if !kind.Valid() {
		m.bad(c)
		return
	}
	emailVia, ok := administratorVia(body.EmailVerifiedVia)
	if !ok {
		m.bad(c)
		return
	}
	phoneVia, ok := administratorVia(body.PhoneVerifiedVia)
	if !ok {
		m.bad(c)
		return
	}
	if !m.asks(c, authn.Decision{Action: authn.ActionAccountCreate, Actor: id}) {
		return
	}
	if kind == authn.KindService {
		if !m.asks(c, authn.Decision{Action: authn.ActionClientManage, Actor: id}) {
			return
		}
	}

	in := authn.CreateAccount{
		Kind:             kind,
		DisplayName:      body.DisplayName,
		Username:         body.Username,
		Email:            body.Email,
		EmailVerifiedVia: emailVia,
		Phone:            body.Phone,
		PhoneVerifiedVia: phoneVia,
		Password:         body.Password,
		Attributes:       body.Attributes,
		MFARequired:      body.MFARequired,
		Via:              authn.ReasonAdmin,
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountCreate, "",
		map[string]string{detailActionID: actionID, detailKind: string(kind), detailSource: authn.ReasonAdmin})) {
		return
	}
	account, err := m.accountAdm.Create(c.Request.Context(), in)
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountCreate, "", actionID))
		m.answer(c, "creating an account", err)
		return
	}
	c.JSON(201, gin.H{"account": accountViewOf(account)})
}

// patchBody is the request of a patch: each field left out is left as it is.
type patchBody struct {
	DisplayName *string           `json:"display_name"`
	Attributes  map[string]string `json:"attributes"`
	Protected   *bool             `json:"protected"`
}

// handleAccountPatch answers PATCH /admin/accounts/:id: the display name, the
// attribute set and the protected flag. The action is account.update.
func (m *product) handleAccountPatch(c *gin.Context) {
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
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	var body patchBody
	if !mount.Decode(c, &body) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "update"})) {
		return
	}
	if !m.applyPatch(c, accountID, body, actionID) {
		return
	}
	account, err := m.accounts.Get(c.Request.Context(), accountID)
	if err != nil {
		m.getFailure(c, err)
		return
	}
	c.JSON(200, gin.H{"account": accountViewOf(account)})
}

// applyPatch writes each field the request named, in order. A failure closes the
// patch with a failed row and the answer it belongs to.
func (m *product) applyPatch(c *gin.Context, id authn.AccountID, body patchBody, actionID string) bool {
	done := func(err error) bool {
		if err != nil {
			m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountUpdate, string(id), actionID))
			m.answer(c, "updating an account", err)
			return false
		}
		return true
	}
	if body.DisplayName != nil {
		if !done(m.accountAdm.SetDisplayName(c.Request.Context(), id, *body.DisplayName)) {
			return false
		}
	}
	if body.Attributes != nil {
		if !done(m.accountAdm.SetAttributes(c.Request.Context(), id, body.Attributes)) {
			return false
		}
	}
	if body.Protected != nil {
		if !done(m.accountAdm.SetProtected(c.Request.Context(), id, *body.Protected)) {
			return false
		}
	}
	return true
}

// handleAccountGet answers GET /admin/accounts/:id. The action is account.read.
func (m *product) handleAccountGet(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	account, err := m.accounts.Get(c.Request.Context(), accountID)
	if err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionAccountRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	c.JSON(200, gin.H{"account": accountViewOf(account)})
}

// handleAccountDelete answers DELETE /admin/accounts/:id. The action is
// account.delete and the actor needs a fresh local multi-factor authentication.
func (m *product) handleAccountDelete(c *gin.Context) {
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
		Action: authn.ActionAccountDelete, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.require(c, id, authn.FreshLocalMulti(m.settings.recentWindow())) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountDelete, string(accountID),
		map[string]string{detailActionID: actionID})) {
		return
	}
	if err := m.accountAdm.Delete(c.Request.Context(), accountID, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountDelete, string(accountID), actionID))
		m.answer(c, "deleting an account", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// statusBody is the request of a status change.
type statusBody struct {
	Status string `json:"status"`
}

// handleAccountStatus answers POST /admin/accounts/:id/status: it disables an
// account or restores it. The action is account.disable and the actor needs a
// fresh local multi-factor authentication.
func (m *product) handleAccountStatus(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	var body statusBody
	if !mount.Decode(c, &body) {
		return
	}
	status, ok := statusOf(body.Status)
	if !ok || status == authn.StatusDeleted {
		m.bad(c)
		return
	}
	if _, err := m.accounts.Get(c.Request.Context(), accountID); err != nil {
		m.getFailure(c, err)
		return
	}
	if !m.asks(c, authn.Decision{
		Action: authn.ActionAccountDisable, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	if !m.require(c, id, authn.FreshLocalMulti(m.settings.recentWindow())) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountDisable, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: body.Status})) {
		return
	}
	if err := m.accountAdm.SetStatus(c.Request.Context(), accountID, status, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountDisable, string(accountID), actionID))
		m.answer(c, "changing an account's status", err)
		return
	}
	account, err := m.accounts.Get(c.Request.Context(), accountID)
	if err != nil {
		m.getFailure(c, err)
		return
	}
	c.JSON(200, gin.H{"account": accountViewOf(account)})
}

// identifierBody is the request of adding an identifier.
type identifierBody struct {
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	VerifiedVia string `json:"verified_via"`
}

// handleIdentifierAdd answers POST /admin/accounts/:id/identifiers: it adds a
// login name or an email address an administrator asserts. The action is
// account.update.
func (m *product) handleIdentifierAdd(c *gin.Context) {
	id, ok := m.caller(c)
	if !ok || !m.admit(c, id) {
		return
	}
	accountID := authn.AccountID(c.Param("id"))
	var body identifierBody
	if !mount.Decode(c, &body) {
		return
	}
	kind := authn.IdentifierKind(body.Kind)
	if !adminIdentifierKind(kind) || body.Value == "" {
		m.bad(c)
		return
	}
	if body.VerifiedVia != "" && body.VerifiedVia != authn.VerifiedViaAdmin {
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
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "identifier-add"})) {
		return
	}
	err := m.accountAdm.AddIdentifier(c.Request.Context(), accountID, kind, body.Value, authn.Verification{Via: authn.VerifiedViaAdmin})
	if err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountUpdate, string(accountID), actionID))
		m.answer(c, "adding an identifier", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// handleIdentifierRemove answers DELETE
// /admin/accounts/:id/identifiers/:identifier. The action is account.update.
func (m *product) handleIdentifierRemove(c *gin.Context) {
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
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}) {
		return
	}
	actionID := newActionID()
	if !m.auditRequired(c, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "identifier-remove"})) {
		return
	}
	if err := m.accountAdm.RemoveIdentifier(c.Request.Context(), accountID, c.Param("identifier")); err != nil {
		m.auditBestEffort(c.Request.Context(), failedAudit(actionAccountUpdate, string(accountID), actionID))
		m.answer(c, "removing an identifier", err)
		return
	}
	c.Status(204)
	c.Writer.WriteHeaderNow()
}

// getFailure answers a read of an object that is not there with a 404 and any
// other failure with a 503.
func (m *product) getFailure(c *gin.Context, err error) {
	m.answer(c, "reading an object", err)
}

// statusOf maps a status name to the value it names.
func statusOf(s string) (authn.AccountStatus, bool) {
	switch s {
	case "disabled":
		return authn.StatusDisabled, true
	case "active":
		return authn.StatusActive, true
	case "deleted":
		return authn.StatusDeleted, true
	}
	return 0, false
}

// identifierKindValid reports whether k is one of the identifier kinds.
func identifierKindValid(k authn.IdentifierKind) bool {
	switch k {
	case authn.IdentifierUsername, authn.IdentifierEmail, authn.IdentifierPhone:
		return true
	}
	return false
}

// adminIdentifierKind reports whether an administrator's identifier write may
// name k. The route adds a login name or an email address an administrator
// asserts; a phone number is added or changed only through an SMS code.
func adminIdentifierKind(k authn.IdentifierKind) bool {
	switch k {
	case authn.IdentifierUsername, authn.IdentifierEmail:
		return true
	}
	return false
}

// administratorVia reads the way an administrator may state an identifier came
// to be verified. An administrator asserts an identifier (admin) or records an
// import (import-unverified); a proof of ownership — an SMS code or an IdP
// claim — is not the administrator's to state and is refused. The empty string
// is passed through, so an identifier written without a way is refused by the
// capability.
func administratorVia(v string) (authn.Verification, bool) {
	switch v {
	case "", authn.VerifiedViaAdmin, authn.VerifiedViaImportUnverified:
		return authn.Verification{Via: v}, true
	}
	return authn.Verification{}, false
}
