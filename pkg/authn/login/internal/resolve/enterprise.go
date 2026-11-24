package resolve

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

const membershipSource = "jit:"

// enterpriseUnbound resolves an enterprise identity with no binding: the provider's row names
// the tenant and how its users get accounts, and a just-in-time first login parks at the pending
// signup.
func (r *Resolver) enterpriseUnbound(ctx context.Context, providerID string, l authn.ExternalLink, ext *authn.External,
	aliases []authn.ExternalLink,
) (Result, error) {
	if r.sso == nil {
		return Result{}, errNoSSO()
	}
	prov, err := r.sso.Get(ctx, providerID)
	if err != nil {
		return Result{}, err
	}
	if prov.Tenant == "" || (ext.Tenant != "" && ext.Tenant != prov.Tenant) {
		return Result{}, fmt.Errorf("%w: resolve: the identity's tenant is not its provider's", authn.ErrInvalidArgument)
	}
	if ext.Email == "" {
		return Result{}, fmt.Errorf("resolve: no address to place the identity by: %w", authn.ErrNotProvisioned)
	}
	domain := emailDomain(ext.Email)
	if domain == "" {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrDomainMismatch)
	}
	providers, err := r.sso.Discover(ctx, domain)
	if err != nil {
		return Result{}, err
	}
	if !slices.ContainsFunc(providers, func(p authn.SSOProvider) bool { return p.Tenant == prov.Tenant }) {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrDomainMismatch)
	}
	switch prov.Provisioning {
	case authn.ProvisioningPreprovisioned:
		return r.preprovisioned(ctx, l, ext)
	case authn.ProvisioningJIT:
		_, found, err := r.occupant(ctx, ext.Email)
		if err != nil {
			return Result{}, err
		}
		if found {
			return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
		}
		switch prov.UsernameStrategy {
		case authn.UsernameStrategyClaim:
			return r.claimEnterprise(ctx, prov, ext, aliases)
		case authn.UsernameStrategyUserConfirm:
			return pendingEnterprise(prov, ext, aliases), nil
		default:
			return Result{}, fmt.Errorf("resolve: %w", authn.ErrJITUsernameStrategy)
		}
	}
	return Result{}, fmt.Errorf("%w: resolve: the provider's provisioning is not one that is known", authn.ErrInvalidArgument)
}

// occupant finds the account that holds an address as an identifier; an address that cannot
// be normalized is held by nobody.
func (r *Resolver) occupant(ctx context.Context, email string) (id authn.AccountID, found bool, err error) {
	id, err = r.accounts.Lookup(ctx, authn.IdentifierEmail, email)
	switch {
	case err == nil:
		return id, true, nil
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrInvalidIdentifier):
		return "", false, nil
	}
	return "", false, err
}

// preprovisioned binds the identity to the account an administrator prepared for the address:
// one that holds it as a proven identifier or was asserted by an administrator. An invitation is
// not a proof of the address.
func (r *Resolver) preprovisioned(ctx context.Context, l authn.ExternalLink, ext *authn.External) (Result, error) {
	id, found, err := r.occupant(ctx, ext.Email)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrNotProvisioned)
	}
	identifiers, err := r.accounts.Identifiers(ctx, id)
	if err != nil {
		return Result{}, err
	}
	prepared := slices.ContainsFunc(identifiers, func(i authn.Identifier) bool {
		return i.Kind == authn.IdentifierEmail && strings.EqualFold(i.Value, ext.Email) &&
			(authn.Verification{Via: i.VerifiedVia}.Proves() || i.VerifiedVia == authn.VerifiedViaAdmin)
	})
	if !prepared {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrNotProvisioned)
	}
	if err := r.externalAccount(ctx, id); err != nil {
		return Result{}, err
	}
	if _, err := r.bindExisting(ctx, id, l); err != nil {
		return Result{}, err
	}
	return Result{Account: id, Outcome: OutcomeMatched}, nil
}

// createEnterprise opens the account a pending enterprise signup describes and its membership.
// The account takes the provider's display name and no address; a username taken in the race is
// authn.ErrAccountExists.
func (r *Resolver) createEnterprise(ctx context.Context, p Pending, username string,
	usernameVia authn.Verification,
) (Result, error) {
	create := authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         p.DisplayName,
		Username:            username,
		UsernameVerifiedVia: usernameVia,
		Via:                 p.Via,
	}
	create, err := r.provision(ctx, create, p.Via)
	if err != nil {
		return Result{}, err
	}
	return r.finishCreated(ctx, newAccount{
		create:     create,
		link:       linkOf(p),
		membership: &authn.Membership{Tenant: p.Tenant, Active: true, Source: membershipSource + p.Provider},
	})
}
