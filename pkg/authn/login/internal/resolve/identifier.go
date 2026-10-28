package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// fromIdentifier resolves a proof of an identifier's ownership, which is a phone
// a verification code reached. A login of an unknown phone parks at the pending
// signup when sign-up is open and is authn.ErrSignupClosed otherwise. The login
// method hands a proof only for a number that may sign in: one no account holds
// or one its account chose as a sign-in way.
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
	return pendingPhone(idf.Value), nil
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

// createPhone opens the account a pending phone signup describes. The phone's
// uniqueness is the arbiter of a race: the login that lost it reads the account
// that won, whose number is already chosen as a sign-in way because that is how
// it was written.
func (r *Resolver) createPhone(ctx context.Context, p Pending, username string) (Result, error) {
	in, err := r.provision(ctx, authn.CreateAccount{
		Kind:                authn.KindUser,
		Username:            username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Phone:               p.Phone,
		PhoneVerifiedVia:    authn.Verification{Via: authn.VerifiedViaSMSCode},
		Via:                 p.Via,
	}, p.Via)
	if err != nil {
		return Result{}, err
	}
	acct, err := r.admin.Create(ctx, in)
	if errors.Is(err, authn.ErrAccountExists) {
		id, lerr := r.accounts.Lookup(ctx, authn.IdentifierPhone, p.Phone)
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
