package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// claimEnterprise opens the account a claim-strategy just-in-time login describes. The
// username comes from the configured claim, normalized and checked before anything is written;
// a missing, invalid or taken name and an unusable strategy each refuse without writing.
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
	// The lookup normalizes too, so a value the slug rule accepts but the account module
	// keeps for itself, such as an account-identifier shape, is not a usable username.
	_, err = r.accounts.Lookup(ctx, authn.IdentifierUsername, username)
	switch {
	case err == nil:
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameTaken)
	case authn.IsUsernameShape(err), errors.Is(err, authn.ErrInvalidIdentifier):
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameInvalid)
	case !errors.Is(err, authn.ErrAccountNotFound):
		return Result{}, err
	}
	res, err := r.createEnterprise(ctx, enterprisePending(prov, ext, aliases), username,
		authn.Verification{Via: authn.VerifiedViaIDPPrefix + prov.ID})
	switch {
	case errors.Is(err, authn.ErrAccountExists):
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameTaken)
	case errors.Is(err, authn.ErrUsernameReserved), authn.IsUsernameShape(err),
		errors.Is(err, authn.ErrInvalidIdentifier):
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameInvalid)
	}
	return res, err
}

// claimValue reads the claim a claim strategy names; a missing claim is missing, an unknown
// strategy is a configuration failure, and the email claim's value is its local part.
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
