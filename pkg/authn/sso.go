package authn

import "context"

// SSOProviders is the register of the enterprise IdPs of tenants, with their
// verified email domains. The SSO module delivers it exclusively. The login
// engine and the tenant admission layer take it as optional; the SAML and OIDC
// modules require it.
//
// What is read out of it never holds the client secret: Secret is the one way to
// it, and the OIDC module is its only caller. The write methods are exposed as
// HTTP routes only through the administration module, after a Decider has
// allowed the action authn.idp.manage; this module judges nobody's right to call
// them.
type SSOProviders interface {
	// Get reads one provider row. It holds no secret.
	Get(ctx context.Context, id string) (SSOProvider, error)
	// Secret reads the client secret of an OIDC provider, for the protocol module
	// only. Other kinds of provider have none.
	Secret(ctx context.Context, id string) (string, error)
	// List lists the providers of a tenant, the disabled ones included.
	List(ctx context.Context, tenant string) ([]SSOProvider, error)
	// Tenant answers which tenant a provider belongs to, so that the admission
	// layer can check that a session's provider is the one of the tenant the
	// request is in.
	Tenant(ctx context.Context, id string) (string, error)
	// Discover finds providers by an email domain, among the verified domains of
	// enabled providers only.
	Discover(ctx context.Context, emailDomain string) ([]SSOProvider, error)
	// Put creates or updates a provider. A nil secret keeps the stored one and a
	// non-nil value writes it. Config is validated on write and an unknown key is
	// refused; an update may not change Tenant; Provisioning has no default and a
	// value other than jit and preprovisioned is ErrInvalidConfig. Every write
	// raises Version by one.
	Put(ctx context.Context, p SSOProvider, secret *string) (SSOProvider, error)
	// Delete deletes a provider row and its domain claims in one transaction.
	Delete(ctx context.Context, id string) error
	// ClaimDomain records an unverified claim of an email domain for a provider
	// and returns the token to publish. A domain another provider has verified is
	// ErrDomainClaimed.
	ClaimDomain(ctx context.Context, idpID, domain string) (verifyToken string, err error)
	// VerifyDomain looks up the TXT record _speed-authn.<domain> and, when it is
	// speed-authn=<token>, marks the claim verified. A domain another provider
	// verified, or one lost to the uniqueness constraint in a race, is
	// ErrDomainClaimed. A failure changes nothing.
	VerifyDomain(ctx context.Context, idpID, domain string) error
	// RemoveDomain removes a claim, verified or not.
	RemoveDomain(ctx context.Context, idpID, domain string) error
	// ReverifyDomains looks up again every verified domain of a provider and
	// withdraws the verification of those no longer found. It does not run by
	// itself.
	ReverifyDomains(ctx context.Context, idpID string) error
}
