package model

import "time"

// IdentifierKind is the kind of a handle an account is found by.
type IdentifierKind string

const (
	IdentifierUsername IdentifierKind = "username"
	IdentifierEmail    IdentifierKind = "email"
	IdentifierPhone    IdentifierKind = "phone"
)

// Identifier is one handle bound to an account.
type Identifier struct {
	ID          string
	Kind        IdentifierKind
	Value       string    // normalized
	VerifiedAt  time.Time // non-zero
	VerifiedVia string    // sms-code, idp:<provider id> (proofs); admin, import-unverified (assertions, never grounds for merging)
}
