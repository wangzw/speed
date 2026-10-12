package webauthn

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa"
)

// The causes this module puts on an answer. None carries a value a client sent.
var (
	// errNotEnrolled is the cause of a call about a factor that has no
	// credential, which the client is answered as a login that failed.
	errNotEnrolled = errors.New("the factor has no credential registered")
	// errNoHandle is the cause of a call for an account that holds no user
	// handle, which is an account with no registration here.
	errNoHandle = errors.New("the account holds no webauthn user handle")
	// errNoCredential is the cause of an answer that carries no credential.
	errNoCredential = errors.New("the answer carries no credential")
)

// Kind is the kind of factor this module is.
func (p *product) Kind() authn.FactorKind { return authn.FactorWebAuthn }

// Enroll registers a credential, one step at a time. The first step draws the
// account's user handle if it has none and answers with the creation options;
// the second verifies the client's response and writes the credential. The
// profile is the one the client named, a passkey by default.
func (p *product) Enroll(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	rec, ok := mfa.PendingFactor(ctx)
	if !ok || rec.Account == "" {
		// The multi-factor module is the only caller that hands a pending row,
		// so a call without one is not a registration and writes nothing.
		return authn.StepRefused(errNotEnrolled), nil
	}
	if len(f.Private) == 0 {
		return p.beginEnroll(ctx, rec, in)
	}
	return p.finishEnroll(ctx, rec, f, in)
}

// beginEnroll is the first registration step: it makes sure the account has a
// user handle and answers with the creation options for the named profile.
func (p *product) beginEnroll(ctx context.Context, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNoCredential), nil
	}
	passkey := in.Fields[fieldProfile] != profileSecurityKey
	handle, err := p.register.ensureHandle(ctx, rec.Account)
	if err != nil {
		return authn.Step{}, err
	}
	u, err := p.buildUser(ctx, rec.Account, handle, nil)
	if err != nil {
		return authn.Step{}, err
	}
	options, state, err := beginRegistration(p.wa, u, passkey)
	if err != nil {
		return authn.Step{}, err
	}
	return authn.StepData(stepRegistration, state, options), nil
}

// finishEnroll is the last registration step: it verifies the response, writes
// the credential and answers that the factor is registered.
func (p *product) finishEnroll(ctx context.Context, rec authn.FactorRecord, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNoCredential), nil
	}
	body, ok := credentialBody(in)
	if !ok {
		return authn.StepRefused(errNoCredential), nil
	}
	handle, found, err := p.register.handleOf(ctx, rec.Account)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(errNoHandle), nil
	}
	u, err := p.buildUser(ctx, rec.Account, handle, nil)
	if err != nil {
		return authn.Step{}, err
	}
	profile, cred, err := finishRegistration(p.wa, u, f.Private, body)
	if err != nil {
		return authn.StepRefused(err), nil
	}
	row := credentialRow{
		FactorID:        rec.ID,
		RPID:            p.config.rpID,
		CredentialID:    base64.RawURLEncoding.EncodeToString(cred.ID),
		PublicKey:       base64.RawURLEncoding.EncodeToString(cred.PublicKey),
		AAGUID:          base64.RawURLEncoding.EncodeToString(cred.Authenticator.AAGUID),
		AttestationType: cred.AttestationType,
		SignCount:       int64(cred.Authenticator.SignCount),
		UserVerified:    cred.Flags.UserVerified,
		BackupEligible:  cred.Flags.BackupEligible,
		BackupState:     cred.Flags.BackupState,
		Passkey:         profile != profileSecurityKey,
	}
	for _, t := range cred.Transport {
		row.Transports = joinTransports(append(row.transportList(), string(t)))
	}
	if err := p.register.insertCredential(ctx, row); err != nil {
		return authn.Step{}, err
	}
	return authn.StepEnrolled(rec), nil
}

// Challenge starts a second-factor assertion on the named factor: it answers
// with the assertion options and keeps the session in the flow's private bytes.
func (p *product) Challenge(ctx context.Context, _ authn.FlowState, rec authn.FactorRecord) (authn.Step, error) {
	row, found, err := p.register.getCredential(ctx, rec.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found || row.CloneWarning {
		// A factor with no credential, or one already flagged, starts nothing:
		// the client is answered as a login that failed.
		return authn.StepRefused(errNotEnrolled), nil
	}
	handle, found, err := p.register.handleOf(ctx, rec.Account)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(errNoHandle), nil
	}
	allowed, err := row.toLibrary()
	if err != nil {
		return authn.StepRefused(err), nil
	}
	u, err := p.buildUser(ctx, rec.Account, handle, []credentialRow{row})
	if err != nil {
		return authn.Step{}, err
	}
	options, state, err := beginAssertion(p.wa, u, []libraryCredential{allowed})
	if err != nil {
		return authn.Step{}, err
	}
	return authn.StepData(stepAssertion, state, options), nil
}

// Verify checks a second-factor assertion. The factor's back-off and its audit
// row are the multi-factor module's; this call parses the response, has the
// library check it against the stored credential, and writes the counter and
// flags back.
func (p *product) Verify(ctx context.Context, f authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	row, found, err := p.register.getCredential(ctx, rec.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(errNotEnrolled), nil
	}
	if row.CloneWarning {
		return authn.StepRefused(authn.ErrFactorFlagged), nil
	}
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNoCredential), nil
	}
	body, ok := credentialBody(in)
	if !ok {
		return authn.StepRefused(errNoCredential), nil
	}
	handle, found, err := p.register.handleOf(ctx, rec.Account)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(errNoHandle), nil
	}
	u, err := p.buildUser(ctx, rec.Account, handle, []credentialRow{row})
	if err != nil {
		return authn.Step{}, err
	}
	res, err := finishAssertion(p.wa, u, f.Private, body)
	if err != nil {
		return authn.StepRefused(err), nil
	}
	outcome, err := p.settleAssertion(ctx, row, res)
	if err != nil {
		return authn.Step{}, err
	}
	switch outcome {
	case assertionPassed:
		return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodWebAuthn}, UserVerified: res.userVerified}), nil
	case assertionClone:
		return authn.StepRefused(authn.ErrFactorFlagged), nil
	default:
		return authn.StepRefused(authn.ErrReplay), nil
	}
}

// Remove deletes a factor's credential and, with it the account's last one, the
// account's user handle. It is idempotent.
func (p *product) Remove(ctx context.Context, account authn.AccountID, rec authn.FactorRecord) error {
	deleted, err := p.register.deleteCredential(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !deleted {
		return nil
	}
	ids, err := p.factorIDs(ctx, account)
	if err != nil {
		return err
	}
	left, err := p.register.anyCredential(ctx, ids)
	if err != nil {
		return err
	}
	if !left {
		if err := p.register.dropHandle(ctx, account); err != nil {
			return err
		}
	}
	return nil
}

// assertionOutcome is how settleAssertion reads a verified assertion.
type assertionOutcome int

const (
	assertionPassed assertionOutcome = iota
	assertionClone
	assertionReplay
)

// settleAssertion writes the counter and flags a verified assertion carried. A
// counter that went backwards is the library's clone warning: the credential is
// flagged and the assertion is refused. Otherwise the write-back is conditional,
// so a concurrent assertion that advanced the counter first makes this one a
// replay. A false winner is not an error.
func (p *product) settleAssertion(ctx context.Context, stored credentialRow, res assertionResult) (assertionOutcome, error) {
	if res.credential.Authenticator.CloneWarning {
		if err := p.register.flagClone(ctx, stored.FactorID); err != nil {
			return assertionReplay, err
		}
		return assertionClone, nil
	}
	presented := credentialRow{
		SignCount:    int64(res.credential.Authenticator.SignCount),
		UserVerified: res.credential.Flags.UserVerified,
		BackupState:  res.credential.Flags.BackupState,
	}
	won, err := p.register.writeBack(ctx, stored.FactorID, presented)
	if err != nil {
		return assertionReplay, err
	}
	if !won {
		return assertionReplay, nil
	}
	return assertionPassed, nil
}

// factorIDs is the account's factor rows of this module's kind, read through the
// multi-factor module's seam. The module's own tables are keyed by these ids and
// carry no account of their own.
func (p *product) factorIDs(ctx context.Context, account authn.AccountID) ([]string, error) {
	return p.factorIDsOf(ctx, account, false)
}

// activeFactorIDs is factorIDs restricted to the factors that are active. A
// factor a reset disabled, or a clone warning did, authenticates nothing, so its
// credential is not offered to the library. Remove reads the account's rows whole
// — a handle goes only with the account's last credential, whatever its factor's
// status — which is where the two differ.
func (p *product) activeFactorIDs(ctx context.Context, account authn.AccountID) ([]string, error) {
	return p.factorIDsOf(ctx, account, true)
}

func (p *product) factorIDsOf(ctx context.Context, account authn.AccountID, activeOnly bool) ([]string, error) {
	recs, err := p.mfa.Factors(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("reading the account's factors: %w", err)
	}
	var ids []string
	for _, rec := range recs {
		if rec.Kind != authn.FactorWebAuthn {
			continue
		}
		if activeOnly && rec.Status != authn.FactorActive {
			continue
		}
		ids = append(ids, rec.ID)
	}
	return ids, nil
}

// buildUser is the library's view of an account. Which credentials go in it is
// the caller's: a registration excludes them all, a challenge may hand the
// library just the one credential it is for.
func (p *product) buildUser(ctx context.Context, account authn.AccountID, handle string, only []credentialRow) (user, error) {
	rows := only
	if rows == nil {
		ids, err := p.factorIDs(ctx, account)
		if err != nil {
			return user{}, err
		}
		rows, err = p.register.credentialsFor(ctx, ids, p.config.rpID)
		if err != nil {
			return user{}, err
		}
	}
	return p.userOf(ctx, account, handle, rows)
}

// buildPasskeyUser is the library's view of an account for a passkey login. Only
// an active passkey factor's credential may prove a first factor: a factor a
// reset or a clone warning disabled authenticates nothing, and a security key's
// credential is a second factor only. Neither is offered to the library, so an
// assertion against either is refused before it can stand for a first factor.
func (p *product) buildPasskeyUser(ctx context.Context, account authn.AccountID, handle string) (user, error) {
	ids, err := p.activeFactorIDs(ctx, account)
	if err != nil {
		return user{}, err
	}
	rows, err := p.register.credentialsFor(ctx, ids, p.config.rpID)
	if err != nil {
		return user{}, err
	}
	passkeys := make([]credentialRow, 0, len(rows))
	for _, r := range rows {
		if r.Passkey {
			passkeys = append(passkeys, r)
		}
	}
	return p.userOf(ctx, account, handle, passkeys)
}

// userOf is the library's user built from the credential rows given, with the
// account's handle and the name an authenticator shows.
func (p *product) userOf(ctx context.Context, account authn.AccountID, handle string, rows []credentialRow) (user, error) {
	id, err := handleBytesOf(handle)
	if err != nil {
		return user{}, err
	}
	creds := make([]libraryCredential, 0, len(rows))
	for _, r := range rows {
		c, cerr := r.toLibrary()
		if cerr != nil {
			return user{}, cerr
		}
		creds = append(creds, c)
	}
	name := p.nameOf(ctx, account)
	return user{id: id, name: name, display: name, creds: creds}, nil
}

// nameOf is the name the authenticator shows for the account: its username
// identifier, or the account identifier when it has none. It never fails a call:
// a read that fails falls back to the identifier, because the name is shown to
// the person registering and decides nothing.
func (p *product) nameOf(ctx context.Context, account authn.AccountID) string {
	ids, err := p.accounts.Identifiers(ctx, account)
	if err != nil {
		return string(account)
	}
	for _, id := range ids {
		if id.Kind == authn.IdentifierUsername && id.Value != "" {
			return id.Value
		}
	}
	return string(account)
}

// credentialBody reads the WebAuthn response a client submitted. The field is
// the one this module's options tell the client to answer in; the engine has
// already bounded the request.
func credentialBody(in authn.StepInput) ([]byte, bool) {
	v := in.Fields[fieldCredential]
	if v == "" {
		return nil, false
	}
	return []byte(v), true
}
