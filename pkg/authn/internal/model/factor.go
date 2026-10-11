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
