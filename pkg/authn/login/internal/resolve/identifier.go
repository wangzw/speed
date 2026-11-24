package resolve

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/vislake/speed/pkg/authn"
)

// fromIdentifier resolves a proof of an identifier's ownership, a phone a code reached. An
// unknown phone parks at the pending signup when sign-up is open and is authn.ErrSignupClosed
// otherwise; a phone the holder did not choose as a sign-in way completes no login.
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

// phoneAccount ends the resolution of a phone that belongs to an account: a login lands only
// on a number chosen as a sign-in way; a step-up or enrollment asks only for the number held.
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

// phoneChosen reports whether the account holds the number as a sign-in way. Lookup answers
// for every number an account holds.
func (r *Resolver) phoneChosen(ctx context.Context, id authn.AccountID, value string) (bool, error) {
	held, err := r.accounts.Identifiers(ctx, id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(held, func(i authn.Identifier) bool {
		return i.Kind == authn.IdentifierPhone && i.Value == value && i.SignIn
	}), nil
}

// createPhone opens the account a pending phone signup describes. The phone's uniqueness
// arbitrates a race; the loser reads the winner's account. The display name is made fit to
// store, falling back to the username.
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
