package model

import "time"

// FactorKind is the kind of a second factor.
type FactorKind string

const (
	// FactorTOTP is a time-based one-time password factor.
	FactorTOTP FactorKind = "totp"
	// FactorSMS is an SMS second factor.
	FactorSMS FactorKind = "sms"
	// FactorWebAuthn is a passkey factor.
	FactorWebAuthn FactorKind = "webauthn"
)

// FactorStatus is where a factor stands in its life.
type FactorStatus uint8

const (
	// FactorPending is the zero value: awaiting confirmation, not to be used for
	// verification.
	FactorPending FactorStatus = iota
	// FactorActive is a factor that may be used.
	FactorActive
	// FactorDisabled is a factor that may not be used.
	FactorDisabled
)

// FactorRecord is one factor an account has registered.
type FactorRecord struct {
	// ID is the factor row identifier.
	ID string
	// Account is the account the factor belongs to.
	Account AccountID
	// Kind is the factor kind.
	Kind FactorKind
	// Label is the human-readable label.
	Label string
	// Status is where the factor stands.
	Status FactorStatus
	// CreatedAt is when the factor was registered.
	CreatedAt time.Time
	// ActivatedAt is when the last enrollment step completed.
	ActivatedAt time.Time
	// LastUsedAt is the last successful verification.
	LastUsedAt time.Time
}

// MFAOutcome is what the multi-factor policy decides for one login. The zero
// value, MFANone, is the reading of no second factor being asked for.
type MFAOutcome uint8

// The outcomes of the policy.
const (
	MFANone   MFAOutcome = iota // no second factor is asked for
	MFAFactor                   // the account has a usable factor and must use one
	MFAEnroll                   // the policy requires a factor and the account has none
)

// MFAResult is the assurance one login reached, as the multi-factor module
// composes it from the first factor's proof and the second factor's pass.
type MFAResult struct {
	// Assurance is the level the login reached.
	Assurance  Assurance
	IDPAssured bool // the multi-factor level is an enterprise IdP's declaration, not locally verified factors
}
