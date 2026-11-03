package resolve

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// fromIdentifier resolves a proof of an identifier's ownership, which is a phone
// a verification code reached. A login of an unknown phone parks at the pending
// signup when sign-up is open and is authn.ErrSignupClosed otherwise. A phone the
// account holds but its holder did not choose as a sign-in way completes no
// login: the number is treated as a credential that did not hold, and the login
// does not open an account in its place.
func (r *Resolver) fromIdentifier(ctx context.Context, req Request) (Result, error) {
	idf := req.Proof.Identifier
	if idf.Kind != authn.IdentifierPhone || idf.Value == "" {
		return Result{}, fmt.Errorf("%w: resolve: only a phone is proved by a code", authn.ErrInvalidArgument)
	}
	id, err := r.accounts.Lookup(ctx, authn.IdentifierPhone, idf.Value)
	switch {
	case err == nil:
		return r.phoneAccount(ctx, req, id, idf.Value)
	case !errors.Is(err, authn.ErrAccountNotFound):
		return Result{}, err
	case req.Purpose != authn.PurposeLogin:
		return Result{}, credentialsFailure("the phone is not the flow's account's", nil)
	case !r.signupOpen():
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrSignupClosed)
	}
	return pendingPhone(idf.Value), nil
}

// phoneAccount ends the resolution of a phone that belongs to an account. A login
// lands only on a number the holder chose as a sign-in way; a step-up or an
// enrollment asks an account that already signed in to prove the number it
// holds, so the choice is not read there.
func (r *Resolver) phoneAccount(ctx context.Context, req Request, id authn.AccountID, value string) (Result, error) {
	a, found, err := r.getAccount(ctx, id)
	switch {
	case err != nil:
		return Result{}, err
	case !found:
		return Result{}, credentialsFailure("no such account", authn.ErrAccountNotFound)
	case !canLogIn(a):
		return Result{}, credentialsFailure("the account cannot log in", nil)
	}
	if req.Purpose == authn.PurposeLogin {
		chosen, err := r.phoneChosen(ctx, id, value)
		switch {
		case err != nil:
			return Result{}, err
		case !chosen:
			return Result{}, credentialsFailure("the phone is not one of the account's sign-in ways", nil)
		}
	}
	return r.confirmed(req, a.ID)
}

// phoneChosen reports whether the account holds the number as a sign-in way. The
// account's read surface is the one that exposes the choice; Lookup, which other
// paths use to find the owner of a number, answers for every number an account
// holds.
func (r *Resolver) phoneChosen(ctx context.Context, id authn.AccountID, value string) (bool, error) {
	held, err := r.accounts.Identifiers(ctx, id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(held, func(i authn.Identifier) bool {
		return i.Kind == authn.IdentifierPhone && i.Value == value && i.SignIn
	}), nil
}

// createPhone opens the account a pending phone signup describes, with the
// password the signup step collected. The phone's uniqueness is the arbiter of a
// race: the login that lost it reads the account that won, whose number is
// already chosen as a sign-in way because that is how it was written. The display
// name is the one the signup step carried; the common provision layer makes it
// fit to store and falls back to the username when it was left empty.
func (r *Resolver) createPhone(ctx context.Context, p Pending, username, password, name string) (Result, error) {
	in, err := r.provision(ctx, authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         name,
		Username:            username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Phone:               p.Phone,
		PhoneVerifiedVia:    authn.Verification{Via: authn.VerifiedViaSMSCode},
		Password:            password,
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
		return r.phoneAccount(ctx, Request{Purpose: authn.PurposeLogin}, id, p.Phone)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Account: acct.ID, Outcome: OutcomeCreated}, nil
}
