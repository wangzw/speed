package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// claimEnterprise opens the account a claim-strategy just-in-time login
// describes. The username is taken from the provider's configured claim,
// normalized to the shared slug form and checked against the accounts that
// exist before anything is written. A missing claim, a value that is not a
// username, a name another account holds and a provider that names no usable
// strategy each refuse the login with a distinguishable sentinel, and none of
// them writes an account or its personal tenant: the write happens only after
// every check has passed.
func (r *Resolver) claimEnterprise(ctx context.Context, prov authn.SSOProvider, ext *authn.External,
	aliases []authn.ExternalLink,
) (Result, error) {
	raw, err := claimValue(prov.UsernameClaim, ext)
	if err != nil {
		return Result{}, err
	}
	username, ok := r.normalize(raw)
	if !ok {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameInvalid)
	}
	// The unique constraint of the creation transaction arbitrates a race for the
	// name; this read is what tells a name another account holds apart from one
	// that does not normalize.
	_, err = r.accounts.Lookup(ctx, authn.IdentifierUsername, username)
	switch {
	case err == nil:
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameTaken)
	case !errors.Is(err, authn.ErrAccountNotFound):
		return Result{}, err
	}
	res, err := r.createEnterprise(ctx, enterprisePending(prov, ext, aliases), username,
		authn.Verification{Via: authn.VerifiedViaIDPPrefix + prov.ID})
	switch {
	case errors.Is(err, authn.ErrAccountExists):
		// A concurrent login took the name between the read and the write.
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameTaken)
	case errors.Is(err, authn.ErrUsernameReserved), authn.IsUsernameShape(err),
		errors.Is(err, authn.ErrInvalidIdentifier):
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameInvalid)
	}
	return res, err
}

// claimValue reads the claim a claim strategy names. A claim the assertion does
// not carry is missing; a strategy value this build does not know is a
// configuration failure. The email claim's value is the address's local part,
// which is the only part a username can be made from.
func claimValue(claim string, ext *authn.External) (string, error) {
	if ext == nil {
		return "", fmt.Errorf("resolve: %w", authn.ErrJITUsernameMissing)
	}
	switch claim {
	case authn.UsernameClaimPreferredUsername:
		if ext.PreferredUsername == "" {
			return "", fmt.Errorf("resolve: %w", authn.ErrJITUsernameMissing)
		}
		return ext.PreferredUsername, nil
	case authn.UsernameClaimEmail:
		local, _, found := strings.Cut(ext.Email, "@")
		if !found || local == "" {
			return "", fmt.Errorf("resolve: %w", authn.ErrJITUsernameMissing)
		}
		return local, nil
	}
	return "", fmt.Errorf("resolve: %w", authn.ErrJITUsernameStrategy)
}
