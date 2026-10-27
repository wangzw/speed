package resolve

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// membershipSource is the Membership.Source of an account an enterprise
// provider created, followed by the provider's identifier.
const membershipSource = "jit:"

// enterpriseUnbound resolves an enterprise identity with no binding (A5). The
// provider's row, not the method, says which tenant it belongs to and how its
// users get accounts.
func (r *Resolver) enterpriseUnbound(ctx context.Context, providerID string, l authn.ExternalLink, ext *authn.External) (Result, error) {
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
		return r.jit(ctx, prov, l, ext)
	}
	return Result{}, fmt.Errorf("%w: resolve: the provider's provisioning is not one that is known", authn.ErrInvalidArgument)
}

// occupant finds the account that holds an address as an identifier. An address
// that cannot be normalized is held by nobody, and for an enterprise login that
// is a user the tenant has no account for.
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

// preprovisioned binds the identity to the account an administrator prepared
// for the address: an account that holds it as an identifier whose channel is not
// an import's unproven record. The administrator's own assertion counts here,
// because the account was made for this person, and that is the one place it does.
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
			authn.Verification{Via: i.VerifiedVia}.Valid() && i.VerifiedVia != authn.VerifiedViaImportUnverified
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

// jit creates the account of a first enterprise login and its membership of the
// provider's tenant, or merges into the account that holds the address when the
// provider was set up to allow it and the account passes every condition. In any
// other case the address being taken is authn.ErrLinkRequired.
func (r *Resolver) jit(ctx context.Context, prov authn.SSOProvider, l authn.ExternalLink, ext *authn.External) (Result, error) {
	id, found, err := r.occupant(ctx, ext.Email)
	if err != nil {
		return Result{}, err
	}
	if found {
		return r.jitOccupied(ctx, id, prov, l, ext)
	}
	create := authn.CreateAccount{
		Kind:             authn.KindUser,
		DisplayName:      displayName(ext.DisplayName),
		Email:            ext.Email,
		EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaIDPPrefix + prov.ID},
		Via:              viaJIT,
	}
	if !create.EmailVerifiedVia.Valid() {
		return Result{}, fmt.Errorf("%w: resolve: the provider's identifier cannot name a verification", authn.ErrInvalidArgument)
	}
	create, err = r.provision(ctx, create, viaJIT)
	if err != nil {
		return Result{}, err
	}
	res, err := r.finishCreated(ctx, newAccount{
		create:     create,
		link:       l,
		membership: &authn.Membership{Tenant: prov.Tenant, Active: true, Source: membershipSource + prov.ID},
	})
	if errors.Is(err, authn.ErrAccountExists) {
		// The address was taken after it was looked up, by an account that is not
		// this identity's.
		return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
	}
	return res, err
}

// jitOccupied is the case of an address an account already holds.
func (r *Resolver) jitOccupied(ctx context.Context, id authn.AccountID, prov authn.SSOProvider, l authn.ExternalLink, ext *authn.External) (Result, error) {
	if !prov.MergeExisting {
		return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
	}
	ok, err := r.mergeable(ctx, id, prov.Tenant, ext.Email)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: ext.Email})
	}
	bound, err := r.bindExisting(ctx, id, l)
	if err != nil {
		return Result{}, err
	}
	return merged(id, bound), nil
}

// mergeable is the condition of A5 on an account a provider may merge into: it
// can log in, the address is on it as an identifier a provider proved, every
// membership it has is of this tenant, and it has no password, no phone, no other
// binding and no factor. An account that is in more than one tenant or has
// another way to log in is never merged into, because the account is global and
// the merge would give this tenant's provider a way into everything else the
// account is. Whatever cannot be read leaves the account unmerged, and a
// dependency's failure is the login's.
func (r *Resolver) mergeable(ctx context.Context, id authn.AccountID, tenant, email string) (bool, error) {
	if r.factors == nil {
		return false, nil
	}
	a, found, err := r.getAccount(ctx, id)
	if err != nil || !found || !canLogIn(a) || a.PasswordSet {
		return false, err
	}
	identifiers, err := r.accounts.Identifiers(ctx, id)
	if err != nil {
		return false, err
	}
	var proved bool
	for _, i := range identifiers {
		switch {
		case i.Kind == authn.IdentifierPhone:
			return false, nil
		case i.Kind == authn.IdentifierEmail && strings.EqualFold(i.Value, email) && providerProof(i.VerifiedVia):
			proved = true
		}
	}
	if !proved {
		return false, nil
	}
	bindings, err := r.accounts.Externals(ctx, id)
	if err != nil || len(bindings) > 0 {
		return false, err
	}
	memberships, err := r.memberships.Tenants(ctx, id)
	if err != nil || slices.ContainsFunc(memberships, func(m authn.Membership) bool { return m.Tenant != tenant }) {
		return false, err
	}
	factors, err := r.factors.Factors(ctx, id)
	if err != nil {
		return false, err
	}
	return len(factors) == 0, nil
}
