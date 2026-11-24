package model

import "time"

// Method is one way an authentication was done. The set is closed: Valid says
// whether a value is in it. Pwd, sms and otp are RFC 8176 amr values, the rest
// are this capability's own.
type Method string

const (
	// MethodPassword is a password.
	MethodPassword Method = "pwd"
	// MethodSMS is an SMS verification code.
	MethodSMS Method = "sms"
	// MethodTOTP is a time-based one-time password.
	MethodTOTP Method = "otp"
	// MethodWebAuthn is a passkey assertion.
	MethodWebAuthn Method = "webauthn"
	// MethodOAuth is a social login.
	MethodOAuth Method = "oauth"
	// MethodOIDC is an enterprise OIDC login.
	MethodOIDC Method = "oidc"
	// MethodSAML is an enterprise SAML assertion.
	MethodSAML Method = "saml"
	// MethodStatic is a static credential table entry.
	MethodStatic Method = "static"
	// MethodClientSecret is a service account's client credential.
	MethodClientSecret Method = "client-secret"
)

// Valid reports whether m is one of the constants above.
func (m Method) Valid() bool {
	switch m {
	case MethodPassword, MethodSMS, MethodTOTP, MethodWebAuthn, MethodOAuth,
		MethodOIDC, MethodSAML, MethodStatic, MethodClientSecret:
		return true
	}
	return false
}

// Assurance is the strength an authentication reached, derived from how many
// kinds of factor were proven. Its numbers 0, 1 and 2 are the text of a token's
// acr claim and are never renumbered; the zero value is stamped single by the gateway.
type Assurance uint8

const (
	// AssuranceNone is the zero level: nobody stated an assurance.
	AssuranceNone Assurance = iota
	// AssuranceSingle is one factor.
	AssuranceSingle
	// AssuranceMulti is two factors of different kinds, or a passkey with user
	// verification.
	AssuranceMulti
)

// Access is the level of access the gateway recorded for the route a request
// matched. The zero value, AccessUnknown, says the gateway made no decision
// about this request.
type Access uint8

const (
	// AccessUnknown is an unguarded endpoint, no gateway, or a gateway not made
	// by the gateway module.
	AccessUnknown Access = iota
	// AccessOpen is a route that is exempt.
	AccessOpen
	// AccessOptional is a route that accepts a request with no credential.
	AccessOptional
	// AccessRequired is a route that is guarded and carries no registration.
	AccessRequired
)

// Requirement is the least authentication strength a route accepts. A zero
// MaxAge asks for no freshness.
type Requirement struct {
	// Assurance is the lowest level the requirement accepts.
	Assurance Assurance
	MaxAge    time.Duration // the most the time since AuthTime may be
	Local     bool          // when true, an assurance only an IdP declared does not count
}

// firstFreshness is how recent a first factor must be for an action that asks
// for a fresh one. It is fixed and no setting.
const firstFreshness = 5 * time.Minute

// FreshFirstFactor is the requirement of an action that wants a recently proven
// first factor: at least a single factor, proven within five minutes, from any
// source.
func FreshFirstFactor() Requirement {
	return Requirement{Assurance: AssuranceSingle, MaxAge: firstFreshness}
}

// FreshLocalMulti requires multi-factor, proven within window and proven here, so
// that an IdP-declared assurance does not count; a window that is not positive
// reads as a requirement nobody satisfies, never as one asking for no freshness.
func FreshLocalMulti(window time.Duration) Requirement {
	if window <= 0 {
		window = -1
	}
	return Requirement{Assurance: AssuranceMulti, MaxAge: window, Local: true}
}
