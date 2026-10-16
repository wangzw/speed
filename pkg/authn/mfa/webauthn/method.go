package webauthn

import (
	"context"
	"encoding/base64"
	"errors"

	libwebauthn "github.com/go-webauthn/webauthn/webauthn"

	"github.com/vislake/speed/pkg/authn"
)

// The causes this method puts on an answer. None carries a value the client
// sent: an unknown account, an unknown credential and a wrong signature all read
// the same here, in the log as on the wire.
var (
	errPurpose           = errors.New("this method does not serve this purpose")
	errNotSubmitted      = errors.New("the answer was not a submission")
	errUnknownStep       = errors.New("the flow named a step this method did not make")
	errUnknownCredential = errors.New("no account holds the credential that was presented")
)

// Name is the method's stable name.
func (p *product) Name() string { return methodName }

// Describe reports the passkey method: a ceremony button whose start answers the
// client a data step it completes before submitting. A passkey proves an account
// that holds the credential and never opens one.
func (p *product) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{
		Name: methodName,
		Kind: authn.MethodKindWebAuthn,
	}
}

// Begin starts a passkey login: it answers with the discoverable assertion
// options, which ask the client to pick a credential and prove the account with
// it. The account is not named here and is found from the handle the client
// returns at the answer.
func (p *product) Begin(_ context.Context, s authn.Start) (authn.Step, error) {
	if !servesPurpose(s.Purpose) {
		return authn.StepRefused(errPurpose), nil
	}
	options, state, err := beginDiscoverableAssertion(p.wa)
	if err != nil {
		return authn.Step{}, err
	}
	return authn.StepData(stepPasskey, state, options), nil
}

// Answer verifies the assertion a client answered with. The handle it returns
// finds the account; the library checks the assertion against that account's
// credential, and the counter is written back. A passkey that verified the user
// proves two factors at once, which Proof.UserVerified carries.
func (p *product) Answer(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if in.Action != authn.InputSubmit {
		return authn.StepRefused(errNotSubmitted), nil
	}
	if !servesPurpose(f.Purpose) {
		return authn.StepRefused(errPurpose), nil
	}
	if f.Step != stepPasskey {
		return authn.StepRefused(errUnknownStep), nil
	}
	body, ok := credentialBody(in)
	if !ok {
		return authn.StepRefused(errNoCredential), nil
	}

	var account authn.AccountID
	handler := func(_, userHandle []byte) (libwebauthn.User, error) {
		handle := base64.RawURLEncoding.EncodeToString(userHandle)
		acc, found, err := p.register.accountForHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errUnknownCredential
		}
		account = acc
		u, err := p.buildPasskeyUser(ctx, acc, handle)
		if err != nil {
			return nil, err
		}
		return u, nil
	}

	_, res, err := finishDiscoverableAssertion(p.wa, handler, f.Private, body)
	if err != nil {
		return authn.StepRefused(err), nil
	}
	if account == "" {
		return authn.StepRefused(errUnknownCredential), nil
	}
	row, found, err := p.register.credentialByID(ctx, p.config.rpID, res.credential.ID)
	if err != nil {
		return authn.Step{}, err
	}
	if !found {
		return authn.StepRefused(errNotEnrolled), nil
	}
	if row.CloneWarning {
		return authn.StepRefused(authn.ErrFactorFlagged), nil
	}
	outcome, err := p.settleAssertion(ctx, row, res)
	if err != nil {
		return authn.Step{}, err
	}
	switch outcome {
	case assertionPassed:
		return authn.StepProven(authn.Proof{
			Account:      account,
			Methods:      []authn.Method{authn.MethodWebAuthn},
			UserVerified: res.userVerified,
		}), nil
	case assertionClone:
		return authn.StepRefused(authn.ErrFactorFlagged), nil
	default:
		return authn.StepRefused(authn.ErrReplay), nil
	}
}

// servesPurpose reports whether the method answers a flow of this purpose. A
// passkey proves an account, which is what a login and a step-up need; a link
// binds an external identity and an enroll registers a factor, and neither is
// something a passkey proves. The zero purpose is none.
func servesPurpose(purpose string) bool {
	return purpose == authn.PurposeLogin || purpose == authn.PurposeStepUp
}
