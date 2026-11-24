package model

import (
	"errors"
	"strings"
)

// ErrInvalidAccountID reports an account identifier that breaks the format
// ParseAccountID states.
var ErrInvalidAccountID = errors.New("authn: account identifier is not well formed")

// StaticPrefix starts the identifier of every subject in a static credential
// table.
const StaticPrefix = "static."

const maxAccountIDLen = 64

// AccountID names one account: opaque, immutable, never reused, 1 to 64 bytes
// of [A-Za-z0-9._-], compared case-sensitively. A static-table subject carries
// StaticPrefix; the empty AccountID is no account.
type AccountID string

// ParseAccountID checks the format and returns the identifier, or
// ErrInvalidAccountID. The failure does not echo the input.
func ParseAccountID(s string) (AccountID, error) {
	if len(s) == 0 || len(s) > maxAccountIDLen {
		return "", ErrInvalidAccountID
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '-':
		default:
			return "", ErrInvalidAccountID
		}
	}
	return AccountID(s), nil
}

// String is the identifier's text.
func (id AccountID) String() string { return string(id) }

// IsStatic reports whether the identifier begins with StaticPrefix, which makes
// it a subject of the static credential table; the prefix is case-sensitive.
func (id AccountID) IsStatic() bool { return strings.HasPrefix(string(id), StaticPrefix) }

// SessionID is the public identifier of a login session, a UUIDv7 text. It is
// not a credential.
type SessionID string
