package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// Source names the first-login path that decided to open a pending account. The
// set is closed.
type Source string

const (
	// SourceSocial is a social provider's first login.
	SourceSocial Source = "social"
	// SourcePhone is an SMS code's first login.
	SourcePhone Source = "phone"
	// SourceEnterprise is an enterprise provider's just-in-time first login.
	SourceEnterprise Source = "enterprise"
)

// Pending is the account a first login opens once its person has chosen a username: what a
// resolution decided before any row was written. It is the flow record's pending member and
// nothing else; the username arrives before Create writes any row.
type Pending struct {
	// Source is the path that decided to open the account.
	Source Source `json:"source"`
	// Via is the CreateAccount.Via of the account: signup for a social or SMS
	// first login, jit for an enterprise one.
	Via string `json:"via"`
	// External is the identity a social or enterprise login proved. Its subject
	// is the key the binding is made under; its tenant is empty for a social
	// login, whose account enters no tenant.
	External *authn.External `json:"external,omitempty"`
	// Aliases are the identity's earlier keys, as the proof carried them. They
	// took part in the resolution that decided the account, and are kept for the
	// record's account of what the method handed over.
	Aliases []authn.ExternalLink `json:"aliases,omitempty"`
	// Phone is the number an SMS first login proved, empty for an external
	// login. It is written under sms-code and is a sign-in way from the start.
	Phone string `json:"phone,omitempty"`
	// Email is the address a social provider declared verified and no account holds, written to
	// the new account as a contact and never as a sign-in way. It is empty for an SMS or enterprise
	// login, or a provider that declared no verified address.
	Email string `json:"email,omitempty"`
	// EmailVerifiedVia is the way Email was proved, idp: followed by the social
	// provider's identifier. It is empty exactly when Email is.
	EmailVerifiedVia string `json:"email_verified_via,omitempty"`
	// Provider is the enterprise provider's identifier and Tenant is its tenant,
	// which the account joins through a membership. Both are empty for a social
	// or phone login.
	Provider string `json:"provider,omitempty"`
	// Tenant is the enterprise provider's tenant, which the account joins
	// through a membership; empty for a social or phone login.
	Tenant string `json:"tenant,omitempty"`
	// DisplayName is the provider's name for the person, made fit to store. A
	// social first login carries it back to the client, read-only, on the
	// pending-signup step.
	DisplayName string `json:"display_name,omitempty"`
	// Attributes are the provider's attributes, as the proof carried them.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Clone returns a copy that shares no slice or map with p.
func (p Pending) Clone() Pending {
	if p.External != nil {
		external := p.External.Clone()
		p.External = &external
	}
	p.Aliases = cloneLinks(p.Aliases)
	p.Attributes = cloneAttributes(p.Attributes)
	return p
}

// Create opens the account a pending signup carries: the chosen username, the password a social
// or phone first login also asked for, and the display name, in one transaction with the
// username identifier and the binding, phone or membership. None of the failures writes anything.
func (r *Resolver) Create(ctx context.Context, p Pending, username, password, name string) (Result, error) {
	switch p.Source {
	case SourceSocial, SourcePhone, SourceEnterprise:
	default:
		return Result{}, fmt.Errorf("%w: resolve: the pending signup names no known source", authn.ErrInvalidArgument)
	}
	if username == "" {
		return Result{}, authn.UsernameShape(fmt.Errorf("%w: resolve: the pending signup has no username", authn.ErrInvalidIdentifier))
	}
	_, err := r.accounts.Lookup(ctx, authn.IdentifierUsername, username)
	switch {
	case err == nil:
		return Result{}, fmt.Errorf("%w: resolve: the username is taken", authn.ErrAccountExists)
	case !errors.Is(err, authn.ErrAccountNotFound):
		return Result{}, err
	}
	switch p.Source {
	case SourcePhone:
		return r.createPhone(ctx, p, username, password, name)
	case SourceEnterprise:
		return r.createEnterprise(ctx, p, username, authn.Verification{Via: authn.VerifiedViaSelf})
	}
	return r.createSocial(ctx, p, username, password)
}

// pendingSocial is the pending signup of a social first login. It carries the
// provider's profile and the address the provider declared verified, when there
// is one no account holds: the new account takes it as a contact.
func pendingSocial(ext *authn.External, aliases []authn.ExternalLink, email string) Result {
	p := Pending{
		Source:      SourceSocial,
		Via:         viaSignup,
		External:    externalOf(ext),
		Aliases:     cloneLinks(aliases),
		DisplayName: displayNameOrDefault(ext.DisplayName, ""),
		Attributes:  cloneAttributes(ext.Attributes),
	}
	if email != "" {
		p.Email = email
		p.EmailVerifiedVia = authn.VerifiedViaIDPPrefix + ext.Provider
	}
	return Result{Pending: &p}
}

func pendingPhone(phone string) Result {
	return Result{Pending: &Pending{
		Source: SourcePhone,
		Via:    viaSignup,
		Phone:  phone,
	}}
}

// pendingEnterprise is the pending signup of an enterprise provider's
// just-in-time first login. The account carries the provider's display name and
// the membership of its tenant, and no address.
func pendingEnterprise(prov authn.SSOProvider, ext *authn.External, aliases []authn.ExternalLink) Result {
	p := enterprisePending(prov, ext, aliases)
	return Result{Pending: &p}
}

// enterprisePending is the account facts a just-in-time first login decided before a username
// is added; both strategies use it, user-confirm parking it and claim opening the account
// immediately.
func enterprisePending(prov authn.SSOProvider, ext *authn.External, aliases []authn.ExternalLink) Pending {
	return Pending{
		Source:      SourceEnterprise,
		Via:         viaJIT,
		External:    externalOf(ext),
		Aliases:     cloneLinks(aliases),
		Provider:    prov.ID,
		Tenant:      prov.Tenant,
		DisplayName: displayNameOrDefault(ext.DisplayName, ""),
		Attributes:  cloneAttributes(ext.Attributes),
	}
}

// externalOf copies an external identity for a pending signup; a nil one stays
// nil, which only a phone login's pending state has.
func externalOf(ext *authn.External) *authn.External {
	if ext == nil {
		return nil
	}
	copied := ext.Clone()
	return &copied
}

func cloneLinks(links []authn.ExternalLink) []authn.ExternalLink {
	if len(links) == 0 {
		return nil
	}
	return append([]authn.ExternalLink(nil), links...)
}

func cloneAttributes(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func linkOf(p Pending) authn.ExternalLink {
	if p.External == nil {
		return authn.ExternalLink{}
	}
	return authn.ExternalLink{
		Provider:      p.External.Provider,
		Subject:       p.External.Subject,
		Email:         p.External.Email,
		EmailVerified: p.External.EmailVerified,
	}
}
