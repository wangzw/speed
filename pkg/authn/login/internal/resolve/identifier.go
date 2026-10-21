package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// fromIdentifier resolves a proof of an identifier's ownership, which is a phone
// a verification code reached. A login of an unknown phone creates the account
// when sign-up is open and is authn.ErrSignupClosed otherwise.
func (r *Resolver) fromIdentifier(ctx context.Context, req Request) (Result, error) {
	idf := req.Proof.Identifier
	if idf.Kind != authn.IdentifierPhone || idf.Value == "" {
		return Result{}, fmt.Errorf("%w: resolve: only a phone is proved by a code", authn.ErrInvalidArgument)
	}
	id, err := r.accounts.Lookup(ctx, authn.IdentifierPhone, idf.Value)
	switch {
	case err == nil:
		return r.phoneAccount(ctx, req, id)
	case !errors.Is(err, authn.ErrAccountNotFound):
		return Result{}, err
	case req.Purpose != authn.PurposeLogin:
		return Result{}, credentialsFailure("the phone is not the flow's account's", nil)
	case !r.signupOpen():
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrSignupClosed)
	}
	return r.createPhone(ctx, idf.Value)
}

// phoneAccount ends the resolution of a phone that belongs to an account.
func (r *Resolver) phoneAccount(ctx context.Context, req Request, id authn.AccountID) (Result, error) {
	a, found, err := r.getAccount(ctx, id)
	switch {
	case err != nil:
		return Result{}, err
	case !found:
		return Result{}, credentialsFailure("no such account", authn.ErrAccountNotFound)
	case !canLogIn(a):
		return Result{}, credentialsFailure("the account cannot log in", nil)
	}
	return r.confirmed(req, a.ID)
}

// createPhone creates the account of a phone that was just proved and has none.
// The phone's uniqueness is the arbiter of a race: the login that lost it reads
// the account that won.
func (r *Resolver) createPhone(ctx context.Context, phone string) (Result, error) {
	in, err := r.provision(ctx, authn.CreateAccount{
		Kind:             authn.KindUser,
		Phone:            phone,
		PhoneVerifiedVia: authn.Verification{Via: authn.VerifiedViaSMSCode},
		Via:              viaSignup,
	}, viaSignup)
	if err != nil {
		return Result{}, err
	}
	acct, err := r.admin.Create(ctx, in)
	if errors.Is(err, authn.ErrAccountExists) {
		id, lerr := r.accounts.Lookup(ctx, authn.IdentifierPhone, phone)
		if lerr != nil {
			return Result{}, err
		}
		return r.phoneAccount(ctx, Request{Purpose: authn.PurposeLogin}, id)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Account: acct.ID, Outcome: OutcomeCreated}, nil
}
