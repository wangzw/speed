package model

import (
	"maps"
	"slices"
	"time"
)

// SessionKind is the kind of a login session. The set is closed.
type SessionKind string

// The kinds of session.
const (
	SessionBrowser SessionKind = "browser" // a cookie
	SessionToken   SessionKind = "token"   // an access token and a refresh token
	SessionService SessionKind = "service" // a service account's client credential, one row per secret
)

// Valid reports whether k is one of the constants above.
func (k SessionKind) Valid() bool {
	return k == SessionBrowser || k == SessionToken || k == SessionService
}

// ClientInfo describes where a request came from, for logs and for showing to
// the user. It is no ground for a judgement or a binding.
type ClientInfo struct {
	IP        string // the address the entry point reported; behind a proxy that is not configured as trusted, the proxy's
	UserAgent string // cut to 200 characters
}

// NewSession is what creating a session takes.
type NewSession struct {
	// Account is the account the session is for.
	Account AccountID
	Kind    SessionKind // browser or token; a service session is made only by ForCredential
	// Methods are the authentication methods completed.
	Methods []Method
	// Assurance is the level the authentication reached.
	Assurance  Assurance
	IDPAssured bool // the multi-factor level is an enterprise IdP's declaration
	// AuthTime is when the last interactive authentication happened.
	AuthTime   time.Time
	Provider   string            // the SSO provider
	Attributes map[string]string // the idp.* snapshot the attribute mapping produced
	// Client describes where the request came from.
	Client      ClientInfo
	AbsoluteTTL time.Duration // zero: the setting for the kind; non-zero may not exceed it
}

// Clone returns a copy that shares no slice or map with n.
func (n NewSession) Clone() NewSession {
	n.Methods = slices.Clone(n.Methods)
	n.Attributes = maps.Clone(n.Attributes)
	return n
}

// Issued is the result of creating a session or of renewing its token. Token is
// a secret and is handed over once.
type Issued struct {
	// ID is the public session identifier.
	ID    SessionID
	Token string // the cookie's value for a browser session, empty for the others
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time
}

// SessionInfo is one session row as a listing shows it.
type SessionInfo struct {
	// ID is the public session identifier.
	ID SessionID
	// Account is the session account.
	Account AccountID
	// Kind is the session kind.
	Kind SessionKind
	// Assurance is the level the session carries.
	Assurance Assurance
	// AuthTime is when the last interactive authentication happened.
	AuthTime time.Time
	// ImpersonationID is the attached impersonation record, empty when none.
	ImpersonationID string
	CredentialID    string // a service session: the secret it stands for
	// CreatedAt is when the session was created.
	CreatedAt time.Time
	// LastSeenAt is the last activity the row was written for.
	LastSeenAt time.Time
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time // a service session may carry the zero time: it follows its secret
	// Revoked says the session has been revoked.
	Revoked bool
}

// SessionStatus is what a verifying backend needs to build an identity: whether
// the session is still valid and the facts the identity is made from. The zero
// value, with Active false, is invalid and carries nothing a backend could use.
type SessionStatus struct {
	// Active says the session is still valid.
	Active bool
	// Account is the account the session belongs to.
	Account AccountID
	// Kind is the session kind.
	Kind SessionKind
	// Methods are the authentication methods completed.
	Methods []Method
	// Assurance is the level the session carries.
	Assurance Assurance
	// IDPAssured says the multi-factor level is an IdP declaration.
	IDPAssured bool
	// AuthTime is when the last interactive authentication happened.
	AuthTime time.Time
	// Provider is the enterprise SSO provider, empty otherwise.
	Provider string
	// Attributes is the idp.* snapshot.
	Attributes map[string]string
	// ImpersonationID is the attached impersonation record, empty when none.
	ImpersonationID string
	// CredentialID is the service secret the session stands for.
	CredentialID string
	// CreatedAt is when the session was created.
	CreatedAt time.Time
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time
}

// Clone returns a copy that shares no slice or map with s.
func (s SessionStatus) Clone() SessionStatus {
	s.Methods = slices.Clone(s.Methods)
	s.Attributes = maps.Clone(s.Attributes)
	return s
}

// Elevation is what a completed step-up changes on a session.
type Elevation struct {
	// Methods are the methods the step-up proved.
	Methods []Method
	// Assurance is the level after the step-up.
	Assurance  Assurance
	IDPAssured bool // false after local factors completed the step-up
	// AuthTime is when the step-up happened.
	AuthTime time.Time
}

// Clone returns a copy that shares no slice with e.
func (e Elevation) Clone() Elevation {
	e.Methods = slices.Clone(e.Methods)
	return e
}
