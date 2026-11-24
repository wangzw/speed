package admin

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func accountViewOf(a authn.Account, identifiers []authn.Identifier) AuthnAdminAccount {
	return AuthnAdminAccount{
		ID:          string(a.ID),
		Kind:        string(a.Kind),
		Status:      a.Status.String(),
		DisplayName: a.DisplayName,
		MFARequired: a.MFARequired,
		Protected:   a.Protected,
		PasswordSet: a.PasswordSet,
		Attributes:  a.Attributes,
		Identifiers: identifiersOf(identifiers),
		CreatedAt:   a.CreatedAt.UTC(),
	}
}

func identifiersOf(identifiers []authn.Identifier) []AuthnAdminIdentifier {
	out := make([]AuthnAdminIdentifier, 0, len(identifiers))
	for _, i := range identifiers {
		out = append(out, AuthnAdminIdentifier{
			Kind:           string(i.Kind),
			Value:          i.Value,
			Proven:         authn.Verification{Via: i.VerifiedVia}.Proves(),
			SignIn:         i.SignIn,
			Contact:        i.Contact,
			DefaultContact: i.DefaultContact,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := identifierKindRank(authn.IdentifierKind(out[i].Kind)), identifierKindRank(authn.IdentifierKind(out[j].Kind))
		if ri != rj {
			return ri < rj
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func identifierKindRank(k authn.IdentifierKind) int {
	switch k {
	case authn.IdentifierUsername:
		return 0
	case authn.IdentifierEmail:
		return 1
	case authn.IdentifierPhone:
		return 2
	}
	return 3
}

func (m *product) viewOf(ctx context.Context, a authn.Account) (AuthnAdminAccount, error) {
	if a.Status == authn.StatusDeleted {
		return accountViewOf(a, nil), nil
	}
	identifiers, err := m.accounts.Identifiers(ctx, a.ID)
	if err != nil {
		return AuthnAdminAccount{}, err
	}
	return accountViewOf(a, identifiers), nil
}

var accountListParams = map[string]bool{
	"status":              true,
	"kind":                true,
	"identifier_prefix":   true,
	"identifier_kind":     true,
	"display_name_prefix": true,
	"after":               true,
	"limit":               true,
}

func (m *product) handleAccountList(ctx context.Context, in accountListInput) (AuthnAdminAccountList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminAccountList{}, err
	}
	if err = m.admit(id, platformDomain); err != nil {
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
	ids := make([]authn.AccountID, 0, len(accounts))
	for _, a := range accounts {
		if a.Status != authn.StatusDeleted {
			ids = append(ids, a.ID)
		}
	}
	var byID map[authn.AccountID][]authn.Identifier
	if len(ids) > 0 {
		byID, err = m.accounts.IdentifiersMany(ctx, ids)
		if err != nil {
			return AuthnAdminAccountList{}, m.unavailable("listing identifiers", err)
		}
	}
	views := make([]AuthnAdminAccount, len(accounts))
	for i, a := range accounts {
		views[i] = accountViewOf(a, byID[a.ID])
	}
	return AuthnAdminAccountList{Accounts: views, NextCursor: next}, nil
}

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

func (m *product) handleAccountCreate(ctx context.Context, in accountCreateInput) (AuthnAdminAccountEnvelope, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminAccountEnvelope{}, err
	}
	if err = m.admit(id, platformDomain); err != nil {
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
	if kind == authn.KindUser && strings.TrimSpace(in.Username) == "" {
		return AuthnAdminAccountEnvelope{}, m.bad()
	}
	if err = m.gateSignup(ctx, id); err != nil {
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
		return AuthnAdminAccountEnvelope{}, m.answer(ctx, "creating an account", err)
	}
	view, err := m.viewOf(ctx, account)
	if err != nil {
		return AuthnAdminAccountEnvelope{}, m.unavailable("listing identifiers", err)
	}
	return AuthnAdminAccountEnvelope{Account: view}, nil
}

func (m *product) handleAccountPatch(ctx context.Context, in accountPatchInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionAccountUpdate, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountUpdate, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailChange: "update"})); err != nil {
		return nil, err
	}
	if err = m.applyPatch(ctx, accountID, in, actionID); err != nil {
		return m.replyFailure(ctx, "updating an account", err)
	}
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	view, err := m.viewOf(ctx, account)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: view}), nil
}

func (m *product) applyPatch(ctx context.Context, id authn.AccountID, in accountPatchInput, actionID string) error {
	done := func(err error) error {
		if err != nil {
			m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, audit.AccountObject(string(id)), actionID))
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

func (m *product) handleAccountGet(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	if err = m.asks(ctx, authn.Decision{
		Action: authn.ActionAccountRead, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	view, err := m.viewOf(ctx, account)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: view}), nil
}

func (m *product) handleAccountDelete(ctx context.Context, in accountIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		Action: authn.ActionAccountDelete, Actor: id,
		Target: string(accountID), TargetAccount: string(accountID),
	}); err != nil {
		return nil, err
	}
	if err := m.require(id, authn.FreshLocalMulti(m.settings.recentWindow())); err != nil {
		return nil, err
	}
	// The audit row follows the commit: a row written first would describe an
	// action that may roll back.
	if err := m.accountAdm.Delete(ctx, accountID, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountDelete, audit.AccountObject(string(accountID)), newActionID()))
		return m.replyFailure(ctx, "deleting an account", err)
	}
	m.auditBestEffort(ctx, mandatory(actionAccountDelete, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: newActionID()}))
	return speedhttp.EmptyReply(204), nil
}

func (m *product) handleAccountStatus(ctx context.Context, in accountStatusInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	accountID := authn.AccountID(in.ID)
	status, ok := statusOf(in.Status)
	if !ok || status == authn.StatusDeleted {
		return nil, m.bad()
	}
	if _, err = m.accounts.Get(ctx, accountID); err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	if err = m.gateSignup(ctx, id); err != nil {
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
	if err = m.auditRequired(ctx, mandatory(actionAccountDisable, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailChange: in.Status})); err != nil {
		return nil, err
	}
	if err = m.accountAdm.SetStatus(ctx, accountID, status, authn.ReasonAdmin); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountDisable, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "changing an account's status", err)
	}
	account, err := m.accounts.Get(ctx, accountID)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	view, err := m.viewOf(ctx, account)
	if err != nil {
		return m.replyFailure(ctx, "reading an object", err)
	}
	return speedhttp.JSONReply(200, AuthnAdminAccountEnvelope{Account: view}), nil
}

func (m *product) handleIdentifierAdd(ctx context.Context, in identifierAddInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
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
	actionID := newActionID()
	if err = m.auditRequired(ctx, mandatory(actionAccountUpdate, audit.AccountObject(string(accountID)),
		map[string]string{detailActionID: actionID, detailChange: "identifier-add"})); err != nil {
		return nil, err
	}
	err = m.accountAdm.AddIdentifier(ctx, accountID, kind, in.Value, authn.Verification{Via: authn.VerifiedViaAdmin})
	if err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "adding an identifier", err)
	}
	return speedhttp.EmptyReply(204), nil
}

func (m *product) handleIdentifierRemove(ctx context.Context, in identifierRemoveInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
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
		map[string]string{detailActionID: actionID, detailChange: "identifier-remove"})); err != nil {
		return nil, err
	}
	if err := m.accountAdm.RemoveIdentifier(ctx, accountID, in.Identifier); err != nil {
		m.auditBestEffort(ctx, failedAudit(actionAccountUpdate, audit.AccountObject(string(accountID)), actionID))
		return m.replyFailure(ctx, "removing an identifier", err)
	}
	return speedhttp.EmptyReply(204), nil
}

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

func identifierKindValid(k authn.IdentifierKind) bool {
	switch k {
	case authn.IdentifierUsername, authn.IdentifierEmail, authn.IdentifierPhone:
		return true
	}
	return false
}

func adminIdentifierKind(k authn.IdentifierKind) bool {
	switch k {
	case authn.IdentifierUsername, authn.IdentifierEmail:
		return true
	}
	return false
}

func administratorVia(v string) (authn.Verification, bool) {
	switch v {
	case "", authn.VerifiedViaAdmin, authn.VerifiedViaImportUnverified:
		return authn.Verification{Via: v}, true
	}
	return authn.Verification{}, false
}
