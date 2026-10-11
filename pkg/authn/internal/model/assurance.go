package model

import "time"

// Method is one way an authentication was done. The set is closed: Valid says
// whether a value is in it. Pwd, sms and otp are RFC 8176 amr values, the rest
// are this capability's own.
type Method string

const (
	MethodPassword     Method = "pwd"
	MethodSMS          Method = "sms"
	MethodTOTP         Method = "otp"
	MethodWebAuthn     Method = "webauthn"
	MethodOAuth        Method = "oauth" // social login
	MethodOIDC         Method = "oidc"  // enterprise OIDC
	MethodSAML         Method = "saml"
	MethodStatic       Method = "static"
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
// kinds of factor were proven. The zero value, AssuranceNone, reads as not
// stated: the gateway stamps it as a single factor, so an identity never
// carries it. The numbers 0, 1 and 2 are the text of a token's acr claim, so
// they are written to the wire and are not renumbered.
type Assurance uint8

const (
	AssuranceNone   Assurance = iota
	AssuranceSingle           // one factor
	AssuranceMulti            // two factors of different kinds, or a passkey with user verification
)

// Access is the level of access the gateway recorded for the route a request
// matched. The zero value, AccessUnknown, says the gateway made no decision
// about this request.
type Access uint8

const (
	AccessUnknown  Access = iota // an unguarded endpoint, no gateway, or a gateway not made by the gateway module
	AccessOpen                   // the route is exempt
	AccessOptional               // the route accepts a request with no credential
	AccessRequired               // the route is guarded and carries no registration
)

// Requirement is the least authentication strength a route accepts. A zero
// MaxAge asks for no freshness.
type Requirement struct {
	Assurance Assurance
	MaxAge    time.Duration // the most the time since AuthTime may be
	Local     bool          // when true, an assurance only an IdP declared does not count
}
