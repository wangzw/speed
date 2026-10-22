package admin

import (
	"context"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// accountViewOf builds one account as the management routes show it.
func accountViewOf(a authn.Account) AuthnAdminAccount {
	return AuthnAdminAccount{
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

// accountListParams is the closed set of query parameters the account list
// route declares. The set is what the OpenAPI fragment publishes; the route
// check holds the declaration to it.
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
func (m *product) handleAccountList(ctx context.Context, in accountListInput) (AuthnAdminAccountList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminAccountList{}, err
	}
	if err = m.admit(id); err != nil {
		return AuthnAdminAccountList{}, err
	}
	q, err := m.accountQuery(in)
	if err != nil {
		return AuthnAdminAccountList{}, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionAccountRead, Actor: id}); err != nil {
		return AuthnAdminAccountList{}, err
	}
	accounts, next, err := m.accounts.List(ctx, q)
	if err != nil {
		return AuthnAdminAccountList{}, m.unavailable("listing accounts", err)
	}
	views := make([]AuthnAdminAccount, len(accounts))
	for i, a := range accounts {
		views[i] = accountViewOf(a)
	}
	return AuthnAdminAccountList{Accounts: views, NextCursor: next}, nil
}

// accountQuery reads the list's query parameters into a read query. A filter it
// does not know, an identifier prefix without a kind, or a display-name prefix
// that is not text is a 400. An empty value of an accepted filter — including an
// empty display_name_prefix — is no filter, the same reading status, kind and
// after take.
func (m *product) accountQuery(in accountListInput) (authn.AccountQuery, error) {
	var q authn.AccountQuery
	if s := in.Status; s != "" {
		st, ok := statusOf(s)
		if !ok {
			return authn.AccountQuery{}, m.bad()
		}
		q.Status = &st
	}
	if k := in.Kind; k != "" {
		kind := authn.AccountKind(k)
		if !kind.Valid() {
			return authn.AccountQuery{}, m.bad()
		}
		q.Kind = kind
	}
	prefix, kind := in.IdentifierPrefix, in.IdentifierKind
	if (prefix == "") != (kind == "") {
		return authn.AccountQuery{}, m.bad()
	}
	if prefix != "" {
		ik := authn.IdentifierKind(kind)
		if !identifierKindValid(ik) {
			return authn.AccountQuery{}, m.bad()
		}
		q.IdentifierKind, q.IdentifierPrefix = ik, prefix
	}
	if p := in.DisplayNamePrefix; p != "" {
		// A display name is stored as text, so a prefix that is not text is a
		// shape the route refuses here rather than a byte sequence the read would
		// hand to the database, which rejects it and answers an outage.
		if !utf8.ValidString(p) {
			return authn.AccountQuery{}, m.bad()
		}
		q.DisplayNamePrefix = p
	}
	q.After = in.After
	if in.Limit < 0 {
		return authn.AccountQuery{}, m.bad()
	}
	q.Limit = in.Limit
	return q, nil
}

// handleAccountCreate answers POST /admin/accounts. The action is
// account.create, and a service account additionally needs client.manage. An
// email address and a phone number are written with a way an administrator may
// state — the administrator's own assertion or an import record — and never a
// proof of ownership, which is not the administrator's to make. A user account
// requires at least one login identifier: with no username, email address or
// phone number it has no way to sign in, so the route refuses it before the
// module writes a row nothing can use. A service account is not held to that:
// it signs in with a service secret, not an identifier.
func (m *product) handleAccountCreate(ctx context.Context, in accountCreateInput) (AuthnAdminAccountEnvelope, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	if err = m.admit(id); err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	kind := authn.AccountKind(in.Kind)
	if !kind.Valid() {
		return AuthnAdminAccountEnvelope{}, m.bad()
	}
	emailVia, ok := administratorVia(in.EmailVerifiedVia)
	if !ok {
		return AuthnAdminAccountEnvelope{}, m.bad()
	}
	phoneVia, ok := administratorVia(in.PhoneVerifiedVia)
	if !ok {
		return AuthnAdminAccountEnvelope{}, m.bad()
	}
	if kind == authn.KindUser && in.Username == "" && in.Email == "" && in.Phone == "" {
		return AuthnAdminAccountEnvelope{}, m.bad()
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionAccountCreate, Actor: id}); err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	if kind == authn.KindService {
		if err = m.asks(ctx, authn.Decision{Action: authn.ActionClientManage, Actor: id}); err != nil {
			return AuthnAdminAccountEnvelope{}, err
		}
	}

	create := authn.CreateAccount{
		Kind:             kind,
		DisplayName:      in.DisplayName,
		Username:         in.Username,
		Email:            in.Email,
		EmailVerifiedVia: emailVia,
		Phone:            in.Phone,
		PhoneVerifiedVia: phoneVia,
		Password:         in.Password,
		Attributes:       in.Attributes,
		MFARequired:      in.MFARequired,
		Via:              authn.ReasonAdmin,
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountCreate, "",
		map[string]string{detailActionID: actionID, detailKind: string(kind), detailSource: authn.ReasonAdmin})); err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	account, err := m.accountAdm.Create(ctx, create)
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountCreate, "", actionID))
		return AuthnAdminAccountEnvelope{}, m.answer("creating an account", err)
	}
	return AuthnAdminAccountEnvelope{Account: accountViewOf(account)}, nil
}

// handleAccountPatch answers PATCH /admin/accounts/:id: the display name, the
// attribute set and the protected flag. The action is account.update.
func (m *product) handleAccountPatch(ctx context.Context, in accountPatchInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "update"})); err != nil {
		return nil, err
	}
	if err = m.applyPatch(ctx, accountID, in, actionID); err != nil {
		return m.replyFailure("updating an account", err)
	}
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure("reading an object", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: accountViewOf(account)}), nil
}

// applyPatch writes each field the request named, in order. A failure closes the
// patch with a failed row and the answer it belongs to.
func (m *product) applyPatch(ctx context.Context, id authn.AccountID, in accountPatchInput, actionID string) error {
	done := func(err error) error {
		if err != nil {
			m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, string(id), actionID))
		}
		return err
	}
	if in.DisplayName != nil {
		if err := done(m.accountAdm.SetDisplayName(ctx, id, *in.DisplayName)); err != nil {
			return err
		}
	}
	if in.Attributes != nil {
		if err := done(m.accountAdm.SetAttributes(ctx, id, in.Attributes)); err != nil {
			return err
		}
	}
	if in.Protected != nil {
		if err := done(m.accountAdm.SetProtected(ctx, id, *in.Protected)); err != nil {
			return err
		}
	}
	return nil
}

// handleAccountGet answers GET /admin/accounts/:id. The action is account.read.
func (m *product) handleAccountGet(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: accountViewOf(account)}), nil
}

// handleAccountDelete answers DELETE /admin/accounts/:id. The action is
// account.delete and the actor needs a fresh local multi-factor authentication.
func (m *product) handleAccountDelete(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountDelete, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err := m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err := m.auditRequired(ctx, mandatory(actionAccountDelete, string(accountID),
		map[string]string{detailActionID: actionID})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.Delete(ctx, accountID, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountDelete, string(accountID), actionID))
		return m.replyFailure("deleting an account", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleAccountStatus answers POST /admin/accounts/:id/status: it disables an
// account or restores it. The action is account.disable and the actor needs a
// fresh local multi-factor authentication.
func (m *product) handleAccountStatus(ctx context.Context, in accountStatusInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	status, ok := statusOf(in.Status)
	if !ok || status == authn.StatusDeleted {
		return nil, m.bad()
	}
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountDisable, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err = m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountDisable, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: in.Status})); err != nil {
		return nil, err
	}
	if err = m.accountAdm.SetStatus(ctx, accountID, status, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountDisable, string(accountID), actionID))
		return m.replyFailure("changing an account's status", err)
	}
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure("reading an object", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: accountViewOf(account)}), nil
}

// handleIdentifierAdd answers POST /admin/accounts/:id/identifiers: it adds a
// login name or an email address an administrator asserts. The action is
// account.update.
func (m *product) handleIdentifierAdd(ctx context.Context, in identifierAddInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	kind := authn.IdentifierKind(in.Kind)
	if !adminIdentifierKind(kind) || in.Value == "" {
		return nil, m.bad()
	}
	if in.VerifiedVia != "" && in.VerifiedVia != authn.VerifiedViaAdmin {
		return nil, m.bad()
	}
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure("reading an object", err)
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "identifier-add"})); err != nil {
		return nil, err
	}
	err = m.accountAdm.AddIdentifier(ctx, accountID, kind, in.Value, authn.Verification{Via: authn.VerifiedViaAdmin})
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, string(accountID), actionID))
		return m.replyFailure("adding an identifier", err)
	}
	return speedhttp.EmptyReply(204), nil
}

// handleIdentifierRemove answers DELETE
// /admin/accounts/:id/identifiers/:identifier. The action is account.update.
func (m *product) handleIdentifierRemove(ctx context.Context, in identifierRemoveInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
	if err := m.auditRequired(ctx, mandatory(actionAccountUpdate, string(accountID),
		map[string]string{detailActionID: actionID, detailChange: "identifier-remove"})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.RemoveIdentifier(ctx, accountID, in.Identifier); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, string(accountID), actionID))
		return m.replyFailure("removing an identifier", err)
	}
	return speedhttp.EmptyReply(204), nil
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
