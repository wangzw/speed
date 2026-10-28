package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// socialUnbound resolves a social identity with no binding by the rules of A3:
// an address the provider declares verified and an existing account holds is
// link-required, and without such an address the first login opens a new
// account. A first login that would open an account parks at the pending signup,
// which asks its person for a username before anything is written.
//
// The address a provider declared is a clue only when the provider says it
// verified it. An address it says it did not verify is treated as absent, so
// that nothing about the accounts that exist is answered to a caller whose claim
// the provider does not stand behind. A declared address is never written into
// an account, whether or not one holds it.
func (r *Resolver) socialUnbound(ctx context.Context, ext *authn.External,
	aliases []authn.ExternalLink,
) (Result, error) {
	if ext.EmailVerified && ext.Email != "" {
		_, err := r.accounts.Lookup(ctx, authn.IdentifierEmail, ext.Email)
		switch {
		case err == nil:
			return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
		case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
		default:
			return Result{}, err
		}
	}
	if !r.signupOpen() {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrSignupClosed)
	}
	return pendingSocial(ext, aliases), nil
}

// createSocial opens the account a pending social signup describes. It writes
// none of the address the provider declared: the account takes the provider's
// display name and nothing else, and the username is the one its person chose.
// A username another login took in the race for the same name leaves
// authn.ErrAccountExists for the answer route's 409.
func (r *Resolver) createSocial(ctx context.Context, p Pending, username string) (Result, error) {
	create := authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         p.DisplayName,
		Username:            username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
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
