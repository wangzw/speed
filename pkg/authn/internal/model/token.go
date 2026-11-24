package model

import (
	"slices"
	"time"
)

// IssueToken carries every fact an access token is issued from. Issuing reads
// nothing from the request's context.
type IssueToken struct {
	// Account is the account the token is issued for.
	Account AccountID
	Actor   AccountID // the impersonator, for an impersonation token
	// Session is the login session the token names.
	Session SessionID
	// Methods are the authentication methods the token attests.
	Methods []Method
	// Assurance is the level the token attests.
	Assurance Assurance
	// AuthTime is when the last interactive authentication happened.
	AuthTime        time.Time
	ImpersonationID string        // an impersonation token
	Scope           string        // an impersonation token: the record's tenant, written as the imp_tenant claim
	ClientID        string        // a service account's client identifier; a token issued for a session takes the configured one
	TTL             time.Duration // zero: the access-ttl setting
}

// Clone returns a copy that shares no slice with t.
func (t IssueToken) Clone() IssueToken {
	t.Methods = slices.Clone(t.Methods)
	return t
}

// AccessToken is an issued access token. Value is a credential.
type AccessToken struct {
	// Value is the issued token, a credential.
	Value string
	// ExpiresAt is when the token stops being valid.
	ExpiresAt time.Time
}

// RefreshToken is an issued refresh token. Value is a credential, and ExpiresAt
// is the idle expiry of the token's row, not the session's absolute end.
type RefreshToken struct {
	// Value is the issued token, a credential.
	Value string
	// ExpiresAt is the idle expiry of the token row.
	ExpiresAt time.Time
}
