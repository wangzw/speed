package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// socialUnbound resolves a social identity with no binding: an address the provider declares
// verified and an existing account holds is link-required; without one the first login parks at
// the pending signup. An unverified address is treated as absent.
func (r *Resolver) socialUnbound(ctx context.Context, ext *authn.External,
	aliases []authn.ExternalLink,
) (Result, error) {
	declared := ""
	if ext.EmailVerified && ext.Email != "" {
		_, err := r.accounts.Lookup(ctx, authn.IdentifierEmail, ext.Email)
		switch {
		case err == nil:
			return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
		case errors.Is(err, authn.ErrAccountNotFound):
			declared = ext.Email
		case errors.Is(err, authn.ErrInvalidIdentifier):
		default:
			return Result{}, err
		}
	}
	if !r.signupOpen() {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrSignupClosed)
	}
	return pendingSocial(ext, aliases, declared), nil
}

// createSocial opens the account a pending social signup describes: the provider's display
// name, the chosen username and password, and the declared verified address as contact. A
// username taken in the race is authn.ErrAccountExists.
func (r *Resolver) createSocial(ctx context.Context, p Pending, username, password string) (Result, error) {
	create := authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         p.DisplayName,
		Username:            username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Email:               p.Email,
		EmailVerifiedVia:    authn.Verification{Via: p.EmailVerifiedVia},
		Password:            password,
		Via:                 p.Via,
	}
	create, err := r.provision(ctx, create, p.Via)
	if err != nil {
		return Result{}, err
	}
	return r.finishCreated(ctx, newAccount{create: create, link: linkOf(p)})
}

// finishCreated creates and binds an account and reports the outcome. A login
// that lost a race for the identity ends on the winner's account, which has to
// be one that can log in.
func (r *Resolver) finishCreated(ctx context.Context, n newAccount) (Result, error) {
	id, created, err := r.createBound(ctx, n)
	if err != nil {
		return Result{}, err
	}
	if !created {
		if err := r.externalAccount(ctx, id); err != nil {
			return Result{}, err
		}
		return Result{Account: id, Outcome: OutcomeBound}, nil
	}
	return Result{Account: id, Outcome: OutcomeCreated, MembershipCreated: n.membership != nil}, nil
}
