package model

import (
	"maps"
	"slices"
)

// The kinds a Field may ask for. The set is closed.
const (
	FieldText     = "text"
	FieldPassword = "password"
	FieldCode     = "code"
	FieldPhone    = "phone"
)

// Field is one input a login method asks the client for.
type Field struct {
	// Name is the input's name. Kind is one of the Field constants.
	Name, Kind string
	// Required says the client must fill the field. Secret says the client must
	// not display or echo the value.
	Required, Secret bool
}

// Proof is everything a login method hands over when a first factor holds. The
// engine resolves the account from it and trusts no claim the method makes
// about whether an account exists. Exactly one of Account, Identifier and
// External is set.
//
// A method reports facts and never a level: the assurance is composed in one
// place, by the multi-factor module.
type Proof struct {
	Account    AccountID   // the method already determined the account (password, passkey)
	Identifier *Identifier // the method proved ownership of an identifier (a phone verified by SMS)
	External   *External   // an external identity
	// Methods are the authentication methods the proof established.
	Methods      []Method
	UserVerified bool // a passkey assertion carried user verification, which makes the factor count as two
	// IDPAsserted: an enterprise IdP declared multi-factor and the provider's
	// trust settings matched.
	IDPAsserted bool
	Provider    string         // the enterprise SSO provider
	Aliases     []ExternalLink // keys the same external identity may earlier have been bound under
	// PasswordChanged: the method replaced the account's password while proving
	// it, which is what a credential carrying must_change makes it do. The engine
	// revokes the account's other sessions once the new session exists.
	PasswordChanged bool
}

// Clone returns a copy that shares no slice, map or pointer with p.
func (p Proof) Clone() Proof {
	if p.Identifier != nil {
		id := *p.Identifier
		p.Identifier = &id
	}
	if p.External != nil {
		ext := p.External.Clone()
		p.External = &ext
	}
	p.Methods = slices.Clone(p.Methods)
	p.Aliases = slices.Clone(p.Aliases)
	return p
}

// Pass is the conclusion that one second factor held.
type Pass struct {
	Methods      []Method // otp for TOTP, sms for SMS, webauthn for a passkey
	UserVerified bool     // a passkey assertion carried user verification
}

// Clone returns a copy that shares no slice with p.
func (p Pass) Clone() Pass {
	p.Methods = slices.Clone(p.Methods)
	return p
}

// External is an external identity a login method hands to the engine, which
// is the one to land it on an account.
type External struct {
	Provider      string // google, github, wechat, wechat-openid:<appid>, oidc:<IdP id>, saml:<IdP id>
	Subject       string // the stable identifier inside that namespace
	Email         string // normalized; may be empty; a hint only
	EmailVerified bool   // the provider claims it verified the address; whether to believe it depends on the source
	// DisplayName is the name the provider gave, for display only.
	DisplayName string
	// PreferredUsername is the standard preferred_username claim the provider
	// asserted, read by a claim-strategy just-in-time login and empty when the
	// provider did not assert one.
	PreferredUsername string
	Tenant            string            // the tenant an enterprise IdP belongs to; empty for social login
	Attributes        map[string]string // from the IdP's attribute mapping, keys carrying the idp. prefix
}

// Clone returns a copy that shares no map with e.
func (e External) Clone() External {
	e.Attributes = maps.Clone(e.Attributes)
	return e
}

// ExternalLink is the key an external identity may have been bound under
// before, which lets a provider's key be upgraded in place.
type ExternalLink struct {
	// Provider is the external namespace. Subject is the stable identifier
	// inside it. Email is the normalized address, which may be empty and is
	// only a hint.
	Provider, Subject, Email string
	// EmailVerified says the provider claims it verified the address.
	EmailVerified bool
}
