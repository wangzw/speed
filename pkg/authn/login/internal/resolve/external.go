package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// The provider names of the WeChat family, which is the only one whose identity
// has more than one key: the union identifier, when the provider sends it, and
// the open identifier of one application, which an earlier login may have been
// bound under.
const (
	providerWeChat        = "wechat"
	providerWeChatOpenPre = "wechat-openid:"
)

// The namespaces an enterprise provider's subjects are keyed in; the identifier
// of the provider follows the colon.
const (
	namespaceOIDC = "oidc:"
	namespaceSAML = "saml:"
)

// fromExternal resolves an external identity, the one source that can create,
// link or merge.
func (r *Resolver) fromExternal(ctx context.Context, req Request) (Result, error) {
	ext := req.Proof.External
	if ext.Provider == "" || ext.Subject == "" {
		return Result{}, fmt.Errorf("%w: resolve: an external identity has a provider and a subject", authn.ErrInvalidArgument)
	}
	if err := checkNamespace(ext.Provider, req.Proof.Provider); err != nil {
		return Result{}, err
	}
	link := authn.ExternalLink{
		Provider:      ext.Provider,
		Subject:       ext.Subject,
		Email:         ext.Email,
		EmailVerified: ext.EmailVerified,
	}
	if req.Purpose != authn.PurposeLogin {
		return r.externalWithoutWrite(ctx, req, link)
	}
	owner, found, err := r.binding(ctx, ext.Provider, ext.Subject)
	if err != nil {
		return Result{}, err
	}
	aliasOwner, err := r.aliasOwner(ctx, req.Proof)
	if err != nil {
		return Result{}, err
	}
	switch {
	case found:
		if aliasOwner != "" && aliasOwner != owner {
			return Result{}, fmt.Errorf("resolve: %w", authn.ErrBindingConflict)
		}
		return r.boundExternal(ctx, owner, link)
	case aliasOwner != "":
		return r.upgradeBinding(ctx, aliasOwner, link)
	case req.Proof.Provider != "":
		return r.enterpriseUnbound(ctx, req.Proof.Provider, link, ext, req.Username)
	}
	return r.socialUnbound(ctx, link, ext, req.Username)
}

// checkNamespace holds the provider of an external identity to the source of the
// proof, so that no method can key an identity in a namespace that is not its
// own. An enterprise proof is keyed in oidc:<id> or saml:<id> of its own
// provider; a social one is in neither enterprise namespace, whose accounts are
// created only after the domain check of the tenant.
func checkNamespace(provider, enterprise string) error {
	if enterprise == "" {
		if strings.HasPrefix(provider, namespaceOIDC) || strings.HasPrefix(provider, namespaceSAML) {
			return fmt.Errorf("%w: resolve: a social proof is keyed in an enterprise namespace", authn.ErrInvalidArgument)
		}
		return nil
	}
	if provider != namespaceOIDC+enterprise && provider != namespaceSAML+enterprise {
		return fmt.Errorf("%w: resolve: the identity is keyed outside its provider's namespace", authn.ErrInvalidArgument)
	}
	return nil
}

// aliasOwner is the account the earlier keys of an external identity are bound
// to, or empty when none is. Two aliases bound to two accounts are
// authn.ErrBindingConflict. Only a WeChat identity has earlier keys, and only
// under the application-scoped namespace.
func (r *Resolver) aliasOwner(ctx context.Context, p authn.Proof) (authn.AccountID, error) {
	if len(p.Aliases) == 0 {
		return "", nil
	}
	if p.Provider != "" || p.External.Provider != providerWeChat {
		return "", fmt.Errorf("%w: resolve: only a WeChat identity has earlier keys", authn.ErrInvalidArgument)
	}
	var owner authn.AccountID
	for _, a := range p.Aliases {
		if !strings.HasPrefix(a.Provider, providerWeChatOpenPre) || a.Subject == "" {
			return "", fmt.Errorf("%w: resolve: an earlier key is not a WeChat open identifier", authn.ErrInvalidArgument)
		}
		id, found, err := r.binding(ctx, a.Provider, a.Subject)
		switch {
		case err != nil:
			return "", err
		case !found:
		case owner != "" && owner != id:
			return "", fmt.Errorf("resolve: %w", authn.ErrBindingConflict)
		default:
			owner = id
		}
	}
	return owner, nil
}

// boundExternal is A2: the identity is bound, the login is that account's, and
// only the display fields of the binding change. Memberships are neither read
// nor written.
func (r *Resolver) boundExternal(ctx context.Context, id authn.AccountID, l authn.ExternalLink) (Result, error) {
	if err := r.externalAccount(ctx, id); err != nil {
		return Result{}, err
	}
	return Result{Account: id, Outcome: OutcomeBound, RefreshErr: r.refreshDisplay(ctx, id, l)}, nil
}

// refreshDisplay writes the address a provider last declared into the binding,
// which is shown and decides nothing. A provider that declares no address leaves
// the stored one alone.
func (r *Resolver) refreshDisplay(ctx context.Context, id authn.AccountID, l authn.ExternalLink) error {
	if l.Email == "" {
		return nil
	}
	bindings, err := r.accounts.Externals(ctx, id)
	if err != nil {
		return err
	}
	for _, b := range bindings {
		if b.Provider == l.Provider && b.Subject == l.Subject && b.Email == l.Email && b.EmailVerified == l.EmailVerified {
			return nil
		}
	}
	return r.admin.LinkExternal(ctx, id, l)
}

// upgradeBinding is A8: an earlier key of the identity is bound and the new key
// is not, so the new key is bound to the same account. A key that has been taken
// by another account in the meantime is authn.ErrBindingConflict and nothing is
// merged.
func (r *Resolver) upgradeBinding(ctx context.Context, id authn.AccountID, l authn.ExternalLink) (Result, error) {
	if err := r.externalAccount(ctx, id); err != nil {
		return Result{}, err
	}
	if err := r.admin.LinkExternal(ctx, id, l); err != nil {
		return Result{}, err
	}
	return Result{Account: id, Outcome: OutcomeUpgraded}, nil
}

// externalWithoutWrite resolves an external identity for a purpose other than
// login. A link asks whether the key is free for the flow's account: bound to
// another account is authn.ErrBindingConflict, and bound to this one or to none
// is an answer the caller links on. A step-up and an enrollment need the
// identity to be the flow's account's.
func (r *Resolver) externalWithoutWrite(ctx context.Context, req Request, l authn.ExternalLink) (Result, error) {
	a, found, err := r.getAccount(ctx, req.Account)
	switch {
	case err != nil:
		return Result{}, err
	case !found:
		return Result{}, fmt.Errorf("%w: resolve: the flow's account does not exist", authn.ErrAccountNotFound)
	case !canLogIn(a):
		return Result{}, fmt.Errorf("%w: resolve", authn.ErrAccountUnavailable)
	}
	owner, bound, err := r.binding(ctx, l.Provider, l.Subject)
	if err != nil {
		return Result{}, err
	}
	if req.Purpose == authn.PurposeLink {
		if bound && owner != req.Account {
			return Result{}, fmt.Errorf("resolve: %w", authn.ErrBindingConflict)
		}
		return Result{Account: req.Account, Outcome: OutcomeVerified}, nil
	}
	if !bound {
		alias, aerr := r.aliasOwner(ctx, req.Proof)
		if aerr != nil {
			return Result{}, aerr
		}
		owner, bound = alias, alias != ""
	}
	if !bound {
		return Result{}, credentialsFailure("the identity is bound to no account", nil)
	}
	return r.confirmed(req, owner)
}

// errNoSSO is the failure of an enterprise identity that arrives where no
// register of providers is wired.
func errNoSSO() error { return &seamFault{seam: "the SSO provider register"} }

// merged ends a resolution that bound an identity to an account that existed
// before, under A4 or A5. The obligations of A10 are the caller's; a login that
// lost the race for the binding to one that bound the same account carries none,
// because the winner's does.
func merged(id authn.AccountID, bound bool) Result {
	if !bound {
		return Result{Account: id, Outcome: OutcomeBound}
	}
	return Result{Account: id, Outcome: OutcomeMerged, Cleanup: mergeCleanup()}
}
