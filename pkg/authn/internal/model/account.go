package model

import (
	"errors"
	"strings"
)

// ErrInvalidAccountID reports an account identifier that breaks the format
// ParseAccountID states.
var ErrInvalidAccountID = errors.New("authn: account identifier is not well formed")

// StaticPrefix starts the identifier of every subject in a static credential
// table. It is what AccountID.IsStatic tests for.
const StaticPrefix = "static."

// maxAccountIDLen is the longest an account identifier may be, in bytes.
const maxAccountIDLen = 64

// AccountID names one account. It is opaque, immutable and never reused, 1 to
// 64 bytes long, drawn from [A-Za-z0-9._-], and compared case-sensitively.
//
// An account in the library carries a UUIDv7 text; a subject of the static
// table carries StaticPrefix and the subject's name; a host importing existing
// users may bring its own identifiers provided they fit this format and do not
// begin with StaticPrefix. The empty AccountID is no account.
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
// it a subject of the static credential table rather than an account in the
// library. The prefix is matched exactly: Static.bob is not one.
func (id AccountID) IsStatic() bool { return strings.HasPrefix(string(id), StaticPrefix) }

// SessionID is the public identifier of a login session, a UUIDv7 text. It is
// not a credential.
type SessionID string
