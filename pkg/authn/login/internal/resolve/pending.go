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

// The sources.
const (
	// SourceSocial is a social provider's first login.
	SourceSocial Source = "social"
	// SourcePhone is an SMS code's first login.
	SourcePhone Source = "phone"
	// SourceEnterprise is an enterprise provider's just-in-time first login.
	SourceEnterprise Source = "enterprise"
)

// Pending is the account a first login opens once its person has chosen a
// username. It is what a resolution decided before any row was written: which
// path decided it, the external identity or the proved phone, the tenant an
// enterprise account enters, the display name and attributes the provider gave,
// the identity's earlier keys, the address a social provider declared verified
// and the creation the step will perform. It carries no username; the
// pending-signup step collects one and hands it to Create.
//
// It is the flow record's pending member and nothing else. While a flow stands
// at signup, no account, binding, identifier or membership row exists for it,
// and the account module writes all of them when the username finally arrives.
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
	// Email is the address a social provider declared verified and no account
	// holds, written to the new account as a contact and never as a sign-in
	// way. It is empty for a provider that declared no verified address, an SMS
	// login and an enterprise one.
	Email string `json:"email,omitempty"`
	// EmailVerifiedVia is the way Email was proved, idp: followed by the social
	// provider's identifier. It is empty exactly when Email is.
	EmailVerifiedVia string `json:"email_verified_via,omitempty"`
	// Provider is the enterprise provider's identifier and Tenant is its tenant,
	// which the account joins through a membership. Both are empty for a social
	// or phone login.
	Provider string `json:"provider,omitempty"`
	Tenant   string `json:"tenant,omitempty"`
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

// Create opens the account a pending signup carries, with the username its
// person chose, the password a social or phone first login also asked for, and
// the display name a phone first login may have carried. It is the pending
// step's only write: the account and its username identifier are written in one
// transaction, and the binding, the proved phone or the membership is written
// beside it by the same rules the immediate first-login paths follow.
//
// A username that is empty or does not normalize is authn.ErrInvalidIdentifier,
// one the system keeps for itself is authn.ErrUsernameReserved, and one another
// account holds is authn.ErrAccountExists. A password the account store's policy
// refuses, including the absence of the one a social or phone first login must
// carry, is authn.ErrPasswordPolicy. None of them writes anything. An absent
// display name is allowed; the account is named after the username in its place.
func (r *Resolver) Create(ctx context.Context, p Pending, username, password, name string) (Result, error) {
	switch p.Source {
	case SourceSocial, SourcePhone, SourceEnterprise:
	default:
		return Result{}, fmt.Errorf("%w: resolve: the pending signup names no known source", authn.ErrInvalidArgument)
	}
	// An empty name has no normalization to fail on, and no account may hold
	// one: it is the shape check's own failure, so the answer names the field.
	if username == "" {
		return Result{}, authn.UsernameShape(fmt.Errorf("%w: resolve: the pending signup has no username", authn.ErrInvalidIdentifier))
	}
	// The unique constraint of the creation transaction arbitrates a race for
	// the name; this read only tells a name another account holds apart from one
	// that does not normalize, which the answer route renders as two refusals.
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

// pendingPhone is the pending signup of a phone an SMS code just proved.
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

// enterprisePending is the account facts a just-in-time first login decided,
// before a username is added. Both strategies use it: the user-confirm strategy
// parks it at the pending signup until the person answers, and the claim
// strategy opens the account immediately with the username its claim named.
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

// cloneLinks copies the earlier keys of an external identity.
func cloneLinks(links []authn.ExternalLink) []authn.ExternalLink {
	if len(links) == 0 {
		return nil
	}
	return append([]authn.ExternalLink(nil), links...)
}

// cloneAttributes copies a provider's attributes.
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

// linkOf is the key a pending signup's external identity is bound under.
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
