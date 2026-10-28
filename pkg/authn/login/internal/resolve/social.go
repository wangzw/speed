package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// socialUnbound resolves a social identity with no binding, by the flow chart of
// the account design: the automatic link of A4 when it is on and every condition
// holds, otherwise A3, and without a clue to an existing account the first-login
// provisioning. A first login that would open an account parks at the pending
// signup, which asks its person for a username before anything is written.
//
// The address a provider declared is a clue only when the provider says it
// verified it. An address it says it did not verify is treated as absent, so
// that nothing about the accounts that exist is answered to a caller whose claim
// the provider does not stand behind.
func (r *Resolver) socialUnbound(ctx context.Context, l authn.ExternalLink, ext *authn.External,
	aliases []authn.ExternalLink,
) (Result, error) {
	var email string
	if ext.EmailVerified && ext.Email != "" {
		id, err := r.accounts.Lookup(ctx, authn.IdentifierEmail, ext.Email)
		switch {
		case err == nil:
			return r.socialKnownEmail(ctx, id, l, ext)
		case errors.Is(err, authn.ErrAccountNotFound):
			email = ext.Email
		case errors.Is(err, authn.ErrInvalidIdentifier):
		default:
			return Result{}, err
		}
	}
	if !r.signupOpen() {
		return Result{}, fmt.Errorf("resolve: %w", authn.ErrSignupClosed)
	}
	return pendingSocial(ext, email, aliases), nil
}

// socialKnownEmail resolves a social identity whose verified address is on an
// existing account: linked and merged when A4 allows it, and authn.ErrLinkRequired
// otherwise (A3).
func (r *Resolver) socialKnownEmail(ctx context.Context, id authn.AccountID, l authn.ExternalLink, ext *authn.External) (Result, error) {
	ok, err := r.autoLinkAllowed(ctx, id, ext)
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

// autoLinkAllowed is A4: the switch is on, and the provider declared the address
// verified, and the address on the account was proved by a provider and not
// asserted by an administrator or an import, and the account is enabled, and the
// address's domain is not one any tenant has verified for its own SSO, which
// social login would otherwise go around. WeChat has no address and is never
// linked this way. A failure of the register of SSO domains is a failure of the
// login: without the answer the last condition cannot be shown to hold.
func (r *Resolver) autoLinkAllowed(ctx context.Context, id authn.AccountID, ext *authn.External) (bool, error) {
	if !r.autoLinkVerifiedEmail() || !ext.EmailVerified || isWeChat(ext.Provider) {
		return false, nil
	}
	domain := emailDomain(ext.Email)
	if domain == "" {
		return false, nil
	}
	a, found, err := r.getAccount(ctx, id)
	if err != nil || !found || !canLogIn(a) {
		return false, err
	}
	provedAddress, err := r.holdsProvedEmail(ctx, id, ext.Email)
	if err != nil || !provedAddress {
		return false, err
	}
	if r.sso == nil {
		return true, nil
	}
	providers, err := r.sso.Discover(ctx, domain)
	if err != nil {
		return false, err
	}
	return len(providers) == 0, nil
}

// isWeChat reports whether a provider is of the WeChat family.
func isWeChat(provider string) bool {
	return provider == providerWeChat || strings.HasPrefix(provider, providerWeChatOpenPre)
}

// holdsProvedEmail reports whether the account has the address as an identifier
// whose ownership a provider proved. The administrator's assertion and the
// import's record are not proofs, and neither is the code that proved a phone.
func (r *Resolver) holdsProvedEmail(ctx context.Context, id authn.AccountID, email string) (bool, error) {
	identifiers, err := r.accounts.Identifiers(ctx, id)
	if err != nil {
		return false, err
	}
	for _, i := range identifiers {
		if i.Kind == authn.IdentifierEmail && strings.EqualFold(i.Value, email) && providerProof(i.VerifiedVia) {
			return true, nil
		}
	}
	return false, nil
}

// providerProof reports whether a verification channel is a provider's
// declaration of an address verified: idp: followed by the provider.
func providerProof(via string) bool {
	return authn.Verification{Via: via}.Valid() && strings.HasPrefix(via, authn.VerifiedViaIDPPrefix)
}

// createSocial opens the account a pending social signup describes. A username
// another login took in the race for the same name leaves authn.ErrAccountExists
// for the answer route's 409; a verified address another account took in the
// meantime is dropped rather than become a reason to refuse the login.
func (r *Resolver) createSocial(ctx context.Context, p Pending, username string) (Result, error) {
	create := authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         p.DisplayName,
		Username:            username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Via:                 p.Via,
	}
	if p.Email != "" && p.EmailVia != "" {
		create.Email, create.EmailVerifiedVia = p.Email, authn.Verification{Via: p.EmailVia}
	}
	create, err := r.provision(ctx, create, p.Via)
	if err != nil {
		return Result{}, err
	}
	return r.finishCreated(ctx, newAccount{create: create, link: linkOf(p), keepWithoutEmail: true})
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
