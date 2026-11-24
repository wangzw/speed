package model

import (
	"maps"
	"strings"
	"time"
)

// AccountKind is the kind of an account. The set is closed and Valid says
// whether a value is in it; the empty kind is in no set and, in an AccountQuery,
// asks for no filter.
type AccountKind string

// The kinds of account.
const (
	KindUser    AccountKind = "user"
	KindService AccountKind = "service" // a non-interactive API client
)

// Valid reports whether k is one of the constants above.
func (k AccountKind) Valid() bool { return k == KindUser || k == KindService }

// AccountStatus is where an account stands. The zero value is StatusDisabled:
// an account nobody enabled cannot authenticate. StatusDeleted is final; the
// identifier stays and is never reused. The numbers are the status column's.
type AccountStatus uint8

// The statuses of an account.
const (
	StatusDisabled AccountStatus = iota // the zero value
	StatusActive
	StatusDeleted
)

// Valid reports whether s is one of the constants above.
func (s AccountStatus) Valid() bool { return s <= StatusDeleted }

// String is the status's name as an event payload writes it: disabled, active or
// deleted. A value outside the set reads unknown.
func (s AccountStatus) String() string {
	switch s {
	case StatusDisabled:
		return "disabled"
	case StatusActive:
		return "active"
	case StatusDeleted:
		return "deleted"
	}
	return "unknown"
}

// Account is one account as the account module reports it. It carries no
// credential and no personal field the capability does not keep.
type Account struct {
	// ID is the account's identifier.
	ID AccountID
	// Kind is user or service.
	Kind AccountKind
	// Status is where the account stands.
	Status AccountStatus
	// DisplayName is the account's display name.
	DisplayName string
	MFARequired bool // the account must register a factor
	Protected   bool // true: the account may not be impersonated, whatever a Decider says
	// PasswordSet reports whether a password credential exists.
	PasswordSet bool
	// Attributes is the host-writable attribute set.
	Attributes map[string]string
	ValidAfter time.Time // a session or token issued before this instant is void
	// CreatedAt is when the account was created.
	CreatedAt time.Time
}

// Clone returns a copy that shares no map with a.
func (a Account) Clone() Account {
	a.Attributes = maps.Clone(a.Attributes)
	return a
}

// The ways an identifier's ownership was established, as Verification.Via writes
// them: sms-code, email-code and idp:<provider> are proofs, while invite, admin,
// import-unverified and self only record a fact and are no grounds for linking.
const (
	VerifiedViaSMSCode          = "sms-code"
	VerifiedViaEmailCode        = "email-code"
	VerifiedViaIDPPrefix        = "idp:" // followed by the provider's identifier
	VerifiedViaInvite           = "invite"
	VerifiedViaAdmin            = "admin"
	VerifiedViaImportUnverified = "import-unverified"
	VerifiedViaSelf             = "self"
)

// Verification says how the ownership of an identifier at creation was
// established. The zero value names no way, which Valid refuses: there are no
// rows awaiting verification.
type Verification struct {
	Via string // written to verified_via
}

// Valid reports whether Via is one of sms-code, email-code, invite, admin,
// import-unverified and self, or idp: followed by a provider identifier of 1 to
// 64 bytes drawn from [A-Za-z0-9._-].
func (v Verification) Valid() bool {
	switch v.Via {
	case VerifiedViaSMSCode, VerifiedViaEmailCode, VerifiedViaInvite, VerifiedViaAdmin,
		VerifiedViaImportUnverified, VerifiedViaSelf:
		return true
	}
	id, ok := strings.CutPrefix(v.Via, VerifiedViaIDPPrefix)
	if !ok {
		return false
	}
	_, err := ParseAccountID(id)
	return err == nil
}

// Proves reports whether the verification is a proof of ownership: sms-code,
// email-code and idp:<provider>. Invite, admin, import-unverified and self, and
// any invalid value, read false.
func (v Verification) Proves() bool {
	return v.Valid() && v.Via != VerifiedViaInvite && v.Via != VerifiedViaAdmin &&
		v.Via != VerifiedViaImportUnverified && v.Via != VerifiedViaSelf
}

// SignupState is how far an account's email verification has got. It is
// independent of AccountStatus: a pending account is an enabled account that
// holds an unproved email address. The numbers are the signup_state column's.
type SignupState uint8

// The states of an account's email verification.
const (
	SignupNone     SignupState = iota // the zero value: no email address to verify
	SignupPending                     // holds an email address that has not been proved
	SignupVerified                    // holds an email address whose ownership was proved
)

// Valid reports whether s is one of the constants above.
func (s SignupState) Valid() bool { return s <= SignupVerified }

// SignupStatus is the email-verification state as a request reads it: State is
// None, Pending or Verified, Email is the address of that state and Since is when
// the account first reached Verified, the zero time while it never did.
type SignupStatus struct {
	// State is how far verification has got.
	State SignupState
	// Email is the address of that state.
	Email string
	// Since is when the account first reached verified.
	Since time.Time
}

// RegisterAccount is the input of an anonymous self-registration. The email
// address is only a contact awaiting verification; it is not a login name.
type RegisterAccount struct {
	// Username is the login name.
	Username string
	Email    string // required; written to authn_pending_contacts once normalized
	// Password is the account password, held to the policy.
	Password string
	// DisplayName is the display name the account starts with.
	DisplayName string
	Locale      string // written to the i18n.locale attribute
	TimeZone    string // written to the i18n.tz attribute
}

// IdentifierFlags is the two axes of an email address or a phone number: whether
// it is a contact, which a registered address always is, and whether it may sign
// in, which the holder chooses; neither rewrites the other.
type IdentifierFlags struct {
	// Contact says the handle is a contact of the account.
	Contact bool
	// SignIn says the handle may be used to sign in.
	SignIn bool
}

// CreateAccount is what creating an account takes. An email and a phone must
// already be verified, so each carries the way it was; there is no row awaiting
// verification. Password and PasswordHash are alternatives.
type CreateAccount struct {
	ID AccountID // for an import only; empty generates one
	// Kind is user or service.
	Kind AccountKind
	// DisplayName is the display name the account starts with.
	DisplayName string
	// Username is the login name.
	Username            string
	UsernameVerifiedVia Verification // the way the username came about; the zero value is admin
	Email               string       // must have been verified
	// EmailVerifiedVia is how the email address was proved.
	EmailVerifiedVia Verification
	Phone            string // must have been verified
	// PhoneVerifiedVia is how the phone number was proved.
	PhoneVerifiedVia Verification
	EmailFlags       IdentifierFlags // an import only: the source record's sign-in axis of Email; the address is a contact regardless
	PhoneFlags       IdentifierFlags // an import only: the source record's sign-in axis of Phone; the number is a contact regardless
	DefaultContact   bool            // an import only: the source record names Email its default contact; true only for a proved email
	Password         string          // an alternative to PasswordHash
	PasswordHash     string          // PHC or legacy form, for an import
	// MustChangePassword marks a credential that must be changed at first use.
	MustChangePassword bool
	// MFARequired asks the account to register a factor.
	MFARequired bool
	// Attributes is the initial host-writable attribute set.
	Attributes map[string]string
	Via        string // admin, jit, signup or import: written into the event payload
}

// Clone returns a copy that shares no map with c.
func (c CreateAccount) Clone() CreateAccount {
	c.Attributes = maps.Clone(c.Attributes)
	return c
}

// AccountQuery selects accounts for Accounts.List. Its zero value asks for
// every account that is not deleted, the first page of the default size.
type AccountQuery struct {
	Status            *AccountStatus // nil: no filter on status, and a deleted account is left out
	Kind              AccountKind    // empty: no filter
	IdentifierKind    IdentifierKind // given with IdentifierPrefix, filters by an identifier prefix
	IdentifierPrefix  string         // normalized, at least 3 characters
	DisplayNamePrefix string         // the beginning of the display name, as written, not normalized
	After             string         // the next cursor of the previous page
	Limit             int            // see Page.EffectiveLimit
}

// Clone returns a copy that shares no pointer with q.
func (q AccountQuery) Clone() AccountQuery {
	if q.Status != nil {
		s := *q.Status
		q.Status = &s
	}
	return q
}

// EffectiveLimit is the page size the query asks for: DefaultPageLimit when
// Limit is not positive and at most MaxPageLimit otherwise.
func (q AccountQuery) EffectiveLimit() int { return pageLimit(q.Limit) }

// The page size every listing reads: the default when none is asked for, and the
// largest one served.
const (
	DefaultPageLimit = 100
	MaxPageLimit     = 1000
)

func pageLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultPageLimit
	case n > MaxPageLimit:
		return MaxPageLimit
	}
	return n
}

// Page selects one page of a listing. Its zero value is the first page of the
// default size.
type Page struct {
	After string // the next cursor of the previous page
	Limit int    // see EffectiveLimit
}

// EffectiveLimit is the page size the page asks for: DefaultPageLimit when Limit
// is not positive, MaxPageLimit when it is larger than that, and Limit
// otherwise.
func (p Page) EffectiveLimit() int { return pageLimit(p.Limit) }

// MemberQuery selects one page of a tenant's members, optionally narrowed to
// accounts whose display name or login identifiers begin with Query. An empty
// Query is no filter; the query is lower-cased before matching identifiers.
type MemberQuery struct {
	Page
	// Query narrows the listing to display names and login identifiers beginning with it.
	Query string
}

// ExternalBinding is one external identity bound to an account, as the account
// module keeps it: a listing and an unbinding use its ID, and the subject is
// carried only as the key it is stored under. It is not External, a login input.
type ExternalBinding struct {
	// ID is the binding row identifier.
	ID string
	// Provider is the external namespace the subject belongs to.
	Provider string
	// Subject is the stable identifier inside that namespace.
	Subject string
	Email   string // the last address declared; for display only, it takes part in no decision
	// EmailVerified says the provider proved the address.
	EmailVerified bool
	// CreatedAt is when the binding was written.
	CreatedAt   time.Time
	LastLoginAt time.Time // the zero time: never
}

// ServiceSecretInfo is what is known of one service-account secret after it was
// created. It never holds the secret.
type ServiceSecretInfo struct {
	// ID is the secret row identifier.
	ID string
	// Label is the human-readable label.
	Label string
	// CreatedAt is when the secret was created.
	CreatedAt  time.Time
	ExpiresAt  time.Time // the zero time: does not expire
	RevokedAt  time.Time // the zero time: not revoked
	LastUsedAt time.Time // the zero time: never
}

// Membership is the fact that an account belongs to a tenant, whose identifier is
// its string form. The zero value, with Active false, is suspended and reads as
// not a member, as does a membership that is not there.
type Membership struct {
	// Account is the member.
	Account AccountID
	// Tenant is the tenant the account belongs to.
	Tenant    string
	Active    bool   // the zero value, suspended, reads as not a member
	Source    string // admin, initial, invite, import or jit:<provider>
	SSOExempt bool   // an emergency pass while the tenant enforces SSO
	// CreatedAt is when the membership was written.
	CreatedAt time.Time
}

// VerifyResult is what a successful password check also reports.
type VerifyResult struct {
	MustChange bool // the credential carries must_change: the method asks for a new password first
}

// SetPasswordOptions qualifies Passwords.Set.
type SetPasswordOptions struct {
	Current           string // the current password, for a change the account makes itself
	MustChange        bool   // sets must_change; an administrator's reset defaults to true
	By                string // event attribution: self, admin or reset; empty is inferred from Current
	Channel           string // event channel: email or sms; empty outside a reset
	RequireActive     bool   // the write transaction requires the account to be enabled
	RequireExisting   bool   // the write transaction requires a credential row, and creates none
	RequireIdentifier string // the write transaction requires this identifier row to belong to the account
	AdvanceValidAfter bool   // the write transaction moves valid_after past the current second
}
