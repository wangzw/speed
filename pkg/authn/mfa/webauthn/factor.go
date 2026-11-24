package webauthn

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa"
)

var (
	errNotEnrolled  = errors.New("the factor has no credential registered")
	errNoHandle     = errors.New("the account holds no webauthn user handle")
	errNoCredential = errors.New("the answer carries no credential")
)

func (p *product) Kind() authn.FactorKind { return authn.FactorWebAuthn }

// Enroll registers a credential: the first step draws the account's user handle if needed and answers creation options, and the second verifies the response and writes the credential.
func (p *product) Enroll(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	rec, ok := mfa.PendingFactor(ctx)
	if !ok || rec.Account == "" {
		return authn.StepRefused(errNotEnrolled), nil
	}
	if len(f.Private) == 0 {
		return p.beginEnroll(ctx, rec, in)
	}
	return p.finishEnroll(ctx, rec, f, in)
}

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

// Challenge starts a second-factor assertion on the named factor and keeps the session in the flow's private bytes.
func (p *product) Challenge(ctx context.Context, _ authn.FlowState, rec authn.FactorRecord) (authn.Step, error) {
	row, found, err := p.register.getCredential(ctx, rec.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found || row.CloneWarning {
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

// Verify checks a second-factor assertion against the stored credential and writes the counter and flags back.
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

// Remove deletes the factor's credential and, with the account's last one, the user handle; it is idempotent.
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

type assertionOutcome int

const (
	assertionPassed assertionOutcome = iota
	assertionClone
	assertionReplay
)

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

func (p *product) factorIDs(ctx context.Context, account authn.AccountID) ([]string, error) {
	return p.factorIDsOf(ctx, account, false)
}

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

func (p *product) buildUser(ctx context.Context, account authn.AccountID, handle string, only []credentialRow) (user, error) {
	rows := only
	if rows == nil {
		ids, err := p.activeFactorIDs(ctx, account)
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

func credentialBody(in authn.StepInput) ([]byte, bool) {
	v := in.Fields[fieldCredential]
	if v == "" {
		return nil, false
	}
	return []byte(v), true
}
