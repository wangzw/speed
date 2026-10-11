package authn

import "github.com/vislake/speed/pkg/authn/internal/model"

// AccountID names one account. It is opaque, immutable and never reused, 1 to
// 64 bytes long, drawn from [A-Za-z0-9._-], and compared case-sensitively. The
// empty AccountID is no account.
//
// An account in the library carries a UUIDv7 text; a subject of the static
// credential table carries StaticPrefix and the subject's name; a host
// importing existing users may bring its own identifiers provided they fit this
// format and do not begin with StaticPrefix. Its methods are String and
// IsStatic, which reports whether the identifier begins with StaticPrefix: such
// an account is a subject of the static table rather than an account in the
// library, and it reads no membership.
type AccountID = model.AccountID

// SessionID is the public identifier of a login session, a UUIDv7 text. It is
// not a credential.
type SessionID = model.SessionID

// ErrInvalidAccountID reports an account identifier that breaks the format
// AccountID states. The failure does not echo the input.
var ErrInvalidAccountID = model.ErrInvalidAccountID

// StaticPrefix starts the identifier of every subject in a static credential
// table.
const StaticPrefix = model.StaticPrefix

// ParseAccountID checks the format AccountID states and returns the identifier,
// or ErrInvalidAccountID.
func ParseAccountID(s string) (AccountID, error) { return model.ParseAccountID(s) }

// Method is one way an authentication was done. The set is closed and Valid
// says whether a value is in it. MethodPassword, MethodSMS and MethodTOTP are
// RFC 8176 amr values; the rest are this capability's own.
type Method = model.Method

// The authentication methods.
const (
	MethodPassword     = model.MethodPassword
	MethodSMS          = model.MethodSMS
	MethodTOTP         = model.MethodTOTP
	MethodWebAuthn     = model.MethodWebAuthn
	MethodOAuth        = model.MethodOAuth // social login
	MethodOIDC         = model.MethodOIDC  // enterprise OIDC
	MethodSAML         = model.MethodSAML
	MethodStatic       = model.MethodStatic
	MethodClientSecret = model.MethodClientSecret
)

// Assurance is the strength an authentication reached, derived from how many
// kinds of factor were proven. The zero value, AssuranceNone, reads as not
// stated: the gateway stamps it as a single factor, so an Identity never carries
// it. The levels are numbered 0, 1 and 2, and the number is the text of a
// token's acr claim.
type Assurance = model.Assurance

// The assurance levels.
const (
	AssuranceNone   = model.AssuranceNone
	AssuranceSingle = model.AssuranceSingle // one factor
	AssuranceMulti  = model.AssuranceMulti  // two factors of different kinds, or a passkey with user verification
)

// Access is the level of access the gateway recorded for the route a request
// matched. The zero value, AccessUnknown, says the gateway made no decision
// about this request.
type Access = model.Access

// The access levels.
const (
	AccessUnknown  = model.AccessUnknown  // an unguarded endpoint, no gateway, or a gateway not made by the gateway module
	AccessOpen     = model.AccessOpen     // the route is exempt
	AccessOptional = model.AccessOptional // the route accepts a request with no credential
	AccessRequired = model.AccessRequired // the route is guarded and carries no registration
)

// Requirement is the least authentication strength a route accepts, as
// Identity.Satisfies reads it: Assurance is the lowest level accepted, MaxAge
// the most the time since the identity's AuthTime may be (zero asks for no
// freshness), and Local, when true, refuses an assurance only an IdP declared.
type Requirement = model.Requirement

// Principal is everything a verifying backend hands over when a credential
// holds. Its fields are the backend's claims, not an identity: the gateway
// checks them one by one before stamping an Identity from them.
//
// A Principal may be written as a literal, and a malformed one compiles and is
// refused at run time. The fields are Account; Actor, which is zero for the same
// account and otherwise differs from Account while Session and Scope are set;
// Session, empty for a backend with no login session; Methods, non-empty with
// every entry in the closed set, no repeats and at most 8; Assurance;
// IDPAssured, which says the multi-factor level is an enterprise IdP's
// declaration and requires Assurance to be multi-factor and Provider to be set;
// AuthTime, when the last interactive authentication happened; Scope, the
// tenant an impersonation is confined to; Provider, the enterprise SSO provider
// as UUID text; and Attributes, bounded by the gateway.
type Principal = model.Principal

// Proof is everything a login method hands over when a first factor holds. The
// engine resolves the account from it and trusts no claim the method makes about
// whether an account exists. Exactly one of Account, Identifier and External is
// set; Methods, UserVerified, IDPAsserted, Provider and Aliases are facts added
// to it. A method reports facts and never a level: the assurance is composed in
// one place, by the multi-factor module.
type Proof = model.Proof

// Pass is the conclusion that one second factor held: the methods it proved,
// and whether a passkey assertion carried user verification.
type Pass = model.Pass

// Field is one input a login method asks the client for. Kind is one of
// FieldText, FieldPassword, FieldCode and FieldPhone.
type Field = model.Field

// The kinds a Field may ask for. The set is closed.
const (
	FieldText     = model.FieldText
	FieldPassword = model.FieldPassword
	FieldCode     = model.FieldCode
	FieldPhone    = model.FieldPhone
)

// FactorKind is the kind of a second factor.
type FactorKind = model.FactorKind

// The kinds of second factor.
const (
	FactorTOTP     = model.FactorTOTP
	FactorSMS      = model.FactorSMS
	FactorWebAuthn = model.FactorWebAuthn
)

// FactorStatus is where a factor stands in its life. The zero value is
// FactorPending, which is not to be used for verification.
type FactorStatus = model.FactorStatus

// The statuses of a factor.
const (
	FactorPending  = model.FactorPending
	FactorActive   = model.FactorActive
	FactorDisabled = model.FactorDisabled
)

// FactorRecord is one factor an account has registered.
type FactorRecord = model.FactorRecord

// IdentifierKind is the kind of a handle an account is found by.
type IdentifierKind = model.IdentifierKind

// The kinds of identifier.
const (
	IdentifierUsername = model.IdentifierUsername
	IdentifierEmail    = model.IdentifierEmail
	IdentifierPhone    = model.IdentifierPhone
)

// Identifier is one handle bound to an account.
type Identifier = model.Identifier

// External is an external identity a login method hands to the engine, which is
// the one to land it on an account.
type External = model.External

// ExternalLink is the key an external identity may have been bound under
// before, which lets a provider's key be upgraded in place.
type ExternalLink = model.ExternalLink
