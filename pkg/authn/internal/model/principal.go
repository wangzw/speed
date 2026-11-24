package model

import (
	"maps"
	"slices"
	"time"
)

// Principal is everything a verifying backend hands over when a credential
// holds: the backend's claims, not an identity, which the gateway checks before
// stamping an Identity. A malformed one compiles and is refused at run time.
type Principal struct {
	// Account is the account the credential was verified for.
	Account AccountID
	Actor   AccountID // zero means the same as Account; when set it differs from Account, and Session and Scope are set
	Session SessionID // empty for a backend with no login session
	Methods []Method  // non-empty, each in the closed set, no repeats, at most 8
	// Assurance is the level the verification reached.
	Assurance Assurance
	// IDPAssured: the multi-factor level is an enterprise IdP's declaration, not
	// locally verified factors. When true, Assurance is multi-factor and Provider
	// is set.
	IDPAssured bool
	AuthTime   time.Time         // when the last interactive authentication happened
	Scope      string            // the tenant an impersonation is confined to; empty otherwise
	Provider   string            // the enterprise SSO provider, a UUID text; empty otherwise
	Attributes map[string]string // bounded by the gateway
}

// Clone returns a copy that shares no slice or map with p.
func (p Principal) Clone() Principal {
	p.Methods = slices.Clone(p.Methods)
	p.Attributes = maps.Clone(p.Attributes)
	return p
}
