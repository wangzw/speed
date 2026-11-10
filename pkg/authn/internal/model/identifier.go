package model

import "time"

// IdentifierKind is the kind of a handle an account is found by.
type IdentifierKind string

const (
	IdentifierUsername IdentifierKind = "username"
	IdentifierEmail    IdentifierKind = "email"
	IdentifierPhone    IdentifierKind = "phone"
)

// Identifier is one handle bound to an account. SignIn and Contact are two
// independent axes of the same handle, and DefaultContact is the email address
// an account's notifications go to.
type Identifier struct {
	ID             string
	Kind           IdentifierKind
	Value          string    // normalized
	VerifiedAt     time.Time // non-zero
	VerifiedVia    string    // sms-code, email-code, idp:<provider id> (proofs); invite, admin, import-unverified, self (assertions, never grounds for merging)
	SignIn         bool      // the handle may be used to sign in
	Contact        bool      // the handle is a contact of the account
	DefaultContact bool      // the proved email address notifications go to
}
