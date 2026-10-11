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
	IP        string // the peer address; behind a proxy, the proxy's
	UserAgent string // cut to 200 characters
}

// NewSession is what creating a session takes.
type NewSession struct {
	Account     AccountID
	Kind        SessionKind // browser or token; a service session is made only by ForCredential
	Methods     []Method
	Assurance   Assurance
	IDPAssured  bool // the multi-factor level is an enterprise IdP's declaration
	AuthTime    time.Time
	Provider    string            // the SSO provider
	Attributes  map[string]string // the idp.* snapshot the attribute mapping produced
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
	ID        SessionID
	Token     string // the cookie's value for a browser session, empty for the others
	ExpiresAt time.Time
}

// SessionInfo is one session row as a listing shows it.
type SessionInfo struct {
	ID              SessionID
	Account         AccountID
	Kind            SessionKind
	Assurance       Assurance
	AuthTime        time.Time
	ImpersonationID string
	CredentialID    string // a service session: the secret it stands for
	CreatedAt       time.Time
	LastSeenAt      time.Time
	ExpiresAt       time.Time // a service session may carry the zero time: it follows its secret
	Revoked         bool
}

// SessionStatus is what a verifying backend needs to build an identity: whether
// the session is still valid and the facts the identity is made from. The zero
// value, with Active false, is invalid and carries nothing a backend could use.
type SessionStatus struct {
	Active          bool
	Account         AccountID
	Kind            SessionKind
	Methods         []Method
	Assurance       Assurance
	IDPAssured      bool
	AuthTime        time.Time
	Provider        string
	Attributes      map[string]string
	ImpersonationID string
	CredentialID    string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// Clone returns a copy that shares no slice or map with s.
func (s SessionStatus) Clone() SessionStatus {
	s.Methods = slices.Clone(s.Methods)
	s.Attributes = maps.Clone(s.Attributes)
	return s
}

// Elevation is what a completed step-up changes on a session.
type Elevation struct {
	Methods    []Method
	Assurance  Assurance
	IDPAssured bool // false after local factors completed the step-up
	AuthTime   time.Time
}

// Clone returns a copy that shares no slice with e.
func (e Elevation) Clone() Elevation {
	e.Methods = slices.Clone(e.Methods)
	return e
}
