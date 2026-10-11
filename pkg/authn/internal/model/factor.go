package model

import "time"

// FactorKind is the kind of a second factor.
type FactorKind string

const (
	FactorTOTP     FactorKind = "totp"
	FactorSMS      FactorKind = "sms"
	FactorWebAuthn FactorKind = "webauthn"
)

// FactorStatus is where a factor stands in its life.
type FactorStatus uint8

const (
	FactorPending FactorStatus = iota // the zero value: awaiting confirmation, not to be used for verification
	FactorActive
	FactorDisabled
)

// FactorRecord is one factor an account has registered.
type FactorRecord struct {
	ID          string
	Account     AccountID
	Kind        FactorKind
	Label       string
	Status      FactorStatus
	CreatedAt   time.Time
	ActivatedAt time.Time
	LastUsedAt  time.Time
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
	Assurance  Assurance
	IDPAssured bool // the multi-factor level is an enterprise IdP's declaration, not locally verified factors
}
