package authn

import (
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

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
// set; Methods, UserVerified, IDPAsserted, Provider, Aliases and PasswordChanged
// are facts added to it. A method reports facts and never a level: the assurance
// is composed in one place, by the multi-factor module.
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

// FreshFirstFactor is the requirement of an action that asks for a recently
// proven first factor: a single factor at least, proven within five minutes
// from any source. The five minutes are fixed and no setting. The login engine,
// the self-service routes, the factor routes and the admission layer use this
// one constructor and write neither the five minutes nor the flag themselves.
func FreshFirstFactor() Requirement { return model.FreshFirstFactor() }

// FreshLocalMulti is the requirement of an account-security action on an account
// that has factors: multi-factor, proven within window, and proven locally, so
// that an assurance only an enterprise IdP declared does not satisfy it. window
// is the authn.mfa.recent-window setting. A window that is not positive is
// outside the setting's domain and reads as a requirement no identity
// satisfies, never as one that asks for no freshness.
func FreshLocalMulti(window time.Duration) Requirement { return model.FreshLocalMulti(window) }

// The bounds of an identity's attribute set, which has two writers: the account
// attributes a host sets and the idp.* attributes the IdP mapping of one login
// puts into the session. Each writer is held to its own bound so that the two
// merged stay inside what the gateway stamps: 24 and 8 entries come to 32, and
// 2 KiB plus 8 keys of 32 bytes and values of 128 bytes come to 3328 bytes,
// below 4 KiB. The numbers are declared once, in the model package, and the
// account, session and login modules refer to them.
const (
	// AccountAttributesMax is the most attributes an account may carry.
	AccountAttributesMax = model.AccountAttributesMax
	// AccountAttributeBytesMax is the most the keys and values of an account's
	// attributes may come to together, in bytes.
	AccountAttributeBytesMax = model.AccountAttributeBytesMax
	// AttributeKeyMax is the longest an attribute key may be, in bytes.
	AttributeKeyMax = model.AttributeKeyMax
	// AccountAttributeValueMax is the longest an account attribute value may be,
	// in bytes.
	AccountAttributeValueMax = model.AccountAttributeValueMax
	// SessionIDPAttributesMax is the most idp.* attributes a session payload may
	// carry.
	SessionIDPAttributesMax = model.SessionIDPAttributesMax
	// SessionIDPKeyMax is the longest an idp.* key may be, prefix included, in
	// bytes.
	SessionIDPKeyMax = model.SessionIDPKeyMax
	// SessionIDPValueMax is the longest an idp.* value may be, in bytes; a longer
	// one is cut at a character boundary.
	SessionIDPValueMax = model.SessionIDPValueMax
	// ReservedPrefixIDP is the key prefix of the attributes an IdP mapping
	// writes. No account attribute may use it.
	ReservedPrefixIDP = model.ReservedPrefixIDP
	// ReservedPrefixAuthn is the key prefix reserved for this capability. No
	// account attribute may use it.
	ReservedPrefixAuthn = model.ReservedPrefixAuthn
)

// AccountKind is the kind of an account: KindUser or KindService. The set is
// closed and Valid says whether a value is in it.
type AccountKind = model.AccountKind

// The kinds of account.
const (
	KindUser    = model.KindUser
	KindService = model.KindService // a non-interactive API client
)

// AccountStatus is where an account stands. The zero value is StatusDisabled:
// an account nobody enabled cannot authenticate. StatusDeleted is final, and the
// identifier stays and is never reused. Its methods are Valid and String, which
// is the name an event payload writes.
type AccountStatus = model.AccountStatus

// The statuses of an account.
const (
	StatusDisabled = model.StatusDisabled // the zero value
	StatusActive   = model.StatusActive
	StatusDeleted  = model.StatusDeleted
)

// Account is one account as the account module reports it. Its Clone returns a
// copy that shares no map with it.
type Account = model.Account

// Verification says how the ownership of an identifier given at creation was
// established. Its zero value names no way and is not Valid. Valid is the closed
// set of forms VerifiedVia may take, and Proves is true for the two that prove
// ownership.
type Verification = model.Verification

// The ways an identifier's ownership was established. VerifiedViaSMSCode and
// VerifiedViaIDPPrefix followed by a provider identifier are proofs; the other
// two only record a fact an administrator or an import asserted, and are never
// grounds for automatic linking or an enterprise merge.
const (
	VerifiedViaSMSCode          = model.VerifiedViaSMSCode
	VerifiedViaIDPPrefix        = model.VerifiedViaIDPPrefix
	VerifiedViaAdmin            = model.VerifiedViaAdmin
	VerifiedViaImportUnverified = model.VerifiedViaImportUnverified
)

// CreateAccount is what creating an account takes. Its Clone returns a copy that
// shares no map with it.
type CreateAccount = model.CreateAccount

// AccountQuery selects accounts for Accounts.List. Its zero value asks for every
// account that is not deleted, the first page of the default size. Its Clone
// returns a copy that shares no pointer with it, and EffectiveLimit reads Limit.
type AccountQuery = model.AccountQuery

// ExternalBinding is one external identity bound to an account, as the account
// module keeps it. A listing and an unbinding use its ID. It is not External,
// which is what a login method hands the engine.
type ExternalBinding = model.ExternalBinding

// ServiceSecretInfo is what is known of one service-account secret after it was
// created. It never holds the secret.
type ServiceSecretInfo = model.ServiceSecretInfo

// Membership is the fact that an account belongs to a tenant, the tenant being
// the string form of a tenant identifier. Its zero value, with Active false, is
// suspended and reads as not a member, as does a membership that is not there.
type Membership = model.Membership

// The page size every listing serves when none is asked for, and the largest one.
const (
	DefaultPageLimit = model.DefaultPageLimit
	MaxPageLimit     = model.MaxPageLimit
)

// Page selects one page of a listing; its zero value is the first page of the
// default size. EffectiveLimit reads Limit as a page size, with the default for
// one that is not positive and MaxPageLimit as the ceiling.
type Page = model.Page

// VerifyResult is what a successful password check also reports.
type VerifyResult = model.VerifyResult

// SetPasswordOptions qualifies Passwords.Set.
type SetPasswordOptions = model.SetPasswordOptions

// SessionKind is the kind of a login session. The set is closed.
type SessionKind = model.SessionKind

// The kinds of session.
const (
	SessionBrowser = model.SessionBrowser // a cookie
	SessionToken   = model.SessionToken   // an access token with a refresh token
	SessionService = model.SessionService // a service account's client credential, one row per secret
)

// ClientInfo describes where a request came from, for logs and display. It is no
// ground for a judgement or a binding.
type ClientInfo = model.ClientInfo

// NewSession is what creating a session takes. Its Clone returns a copy that
// shares no slice or map with it.
type NewSession = model.NewSession

// Issued is the result of creating a session or renewing its token. Token is a
// secret and is handed over once.
type Issued = model.Issued

// SessionInfo is one session row as a listing shows it.
type SessionInfo = model.SessionInfo

// SessionStatus is what a verifying backend builds an identity from. The zero
// value, with Active false, is invalid and carries nothing a backend could use.
// Its Clone returns a copy that shares no slice or map with it.
type SessionStatus = model.SessionStatus

// Elevation is what a completed step-up changes on a session. Its Clone returns
// a copy that shares no slice with it.
type Elevation = model.Elevation

// The purposes a login flow may be started for. The set is closed.
const (
	PurposeLogin  = model.PurposeLogin
	PurposeLink   = model.PurposeLink
	PurposeStepUp = model.PurposeStepUp
	PurposeEnroll = model.PurposeEnroll
)

// The actions a client's answer to a flow step may carry: fields the client
// filled in, or an external party's redirect or POST back.
const (
	InputSubmit   = model.InputSubmit
	InputCallback = model.InputCallback
)

// Start is what the engine hands a login method to begin a flow. Its Clone
// returns a copy that shares no map with it.
type Start = model.Start

// FlowState is the view the engine hands a method of a flow, which the method
// may not change, with the method's own bytes. Its Clone returns a copy that
// shares no slice with it.
type FlowState = model.FlowState

// StepInput is a client's answer to the previous step, or an external party's
// redirect back. Its Clone returns a copy that shares no map or slice with it.
type StepInput = model.StepInput

// MFAOutcome is what the multi-factor policy decides for one login. The zero
// value, MFANone, reads as no second factor being asked for.
type MFAOutcome = model.MFAOutcome

// The outcomes of the multi-factor policy.
const (
	MFANone   = model.MFANone   // no second factor is asked for
	MFAFactor = model.MFAFactor // the account has a usable factor and must use one
	MFAEnroll = model.MFAEnroll // the policy requires a factor and the account has none
)

// MFAResult is the assurance one login reached, as the multi-factor module
// composes it.
type MFAResult = model.MFAResult

// ImpersonationRequest is what starting an impersonation takes.
type ImpersonationRequest = model.ImpersonationRequest

// ImpersonationGrant is one impersonation record.
type ImpersonationGrant = model.ImpersonationGrant

// GrantQuery selects impersonation records for Impersonation.List. Its zero
// value selects every record, the first page of the default size.
type GrantQuery = model.GrantQuery

// The reasons an impersonation record ends, as ImpersonationGrant.EndReason
// writes them. The set is closed.
const (
	EndExplicit          = model.EndExplicit
	EndRevoked           = model.EndRevoked
	EndExpired           = model.EndExpired
	EndSessionEnded      = model.EndSessionEnded
	EndActorDisabled     = model.EndActorDisabled
	EndTargetDisabled    = model.EndTargetDisabled
	EndMembershipRemoved = model.EndMembershipRemoved
	EndStartFailed       = model.EndStartFailed
)

// IssueToken carries every fact an access token is issued from. Its Clone
// returns a copy that shares no slice with it.
type IssueToken = model.IssueToken

// AccessToken is an issued access token. Value is a credential.
type AccessToken = model.AccessToken

// RefreshToken is an issued refresh token. Value is a credential, and ExpiresAt
// is the idle expiry of the token's row, not the session's absolute end.
type RefreshToken = model.RefreshToken

// The purposes a verification code may be sent for. The set is closed.
const (
	SMSPurposeLogin  = model.SMSPurposeLogin
	SMSPurposeMFA    = model.SMSPurposeMFA
	SMSPurposeEnroll = model.SMSPurposeEnroll
)

// SendCode is what asking for a verification code takes.
type SendCode = model.SendCode

// The protocols of an enterprise SSO provider, and the ways an account is
// created for a user who signs in through one. Both sets are closed.
const (
	SSOProtocolOIDC            = model.SSOProtocolOIDC
	SSOProtocolSAML            = model.SSOProtocolSAML
	ProvisioningJIT            = model.ProvisioningJIT
	ProvisioningPreprovisioned = model.ProvisioningPreprovisioned
)

// SSOProvider is one enterprise IdP of a tenant. What is read out of the store
// holds no client secret, and Config holds none either. Its Clone returns a copy
// that shares no slice with it.
type SSOProvider = model.SSOProvider
