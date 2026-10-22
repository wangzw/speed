package authn

import (
	"context"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// SignupState is how far the self-registration of an account has got. It is
// independent of AccountStatus: a pending registration is an enabled account
// that has not proved an email address yet. The zero value is SignupNone, and
// Valid reports whether a value is one of the three.
type SignupState = model.SignupState

// The states of a self-registration.
const (
	SignupNone     = model.SignupNone     // the zero value: not a self-registration
	SignupPending  = model.SignupPending  // self-registered, no email address proved yet
	SignupVerified = model.SignupVerified // an email address has been proved
)

// SignupStatus is the self-registration state as a request reads it: State,
// the Email of that state, and Since, when the account first reached Verified.
type SignupStatus = model.SignupStatus

// RegisterAccount is the input of an anonymous self-registration. The email
// address is only a contact awaiting verification; it is not a login name.
type RegisterAccount = model.RegisterAccount

// Signups opens accounts for anonymous self-registration. The account module
// delivers it exclusively.
type Signups interface {
	// Register writes one enabled account in one transaction: a username
	// identifier written through self, a password credential, and an email
	// contact awaiting verification. The password-signup setting is read here
	// and is the authority: any value but open is ErrSignupClosed. The username
	// and the email address are normalized first, and a pair that cannot be is
	// ErrInvalidIdentifier; the password is held to the policy and a violation
	// is ErrPasswordPolicy. A username already taken is ErrAccountExists, and
	// the pair is counted against the signup-username back-off, which is read
	// before the uniqueness check and answered while it holds as ErrThrottled.
	// Deriving the password uses the signup-concurrency capacity of its own, so
	// a full capacity is ErrBusy and never takes a place from a password login.
	// No email is sent, no session is created and no identifier awaits
	// verification. The account.created event carries via signup.
	Register(ctx context.Context, in RegisterAccount) (Account, error)
}

// SignupVerification is the read surface of the self-registration state. The
// account module delivers it exclusively. Every method reads the database on
// the request: the state is in no account snapshot, session or token.
type SignupVerification interface {
	// SignupStateOf reports how far the account's self-registration has got.
	// State is SignupNone, SignupPending or SignupVerified; Email is the
	// earliest-proved address for Verified (the identifier first verified, ties
	// broken by identifier), the pending contact for Pending, and empty when
	// there is none; Since is the first instant the account reached Verified,
	// the zero time while it never did. An account that does not exist is
	// ErrAccountNotFound, and an unavailable database is ErrUnavailable: a read
	// failure is never answered as SignupNone.
	SignupStateOf(ctx context.Context, id AccountID) (SignupStatus, error)
	// PendingAccountByContact finds the one account a pending contact belongs
	// to. kind is email or phone, value is normalized first (one that cannot be
	// is ErrInvalidIdentifier), and ok is false when no account lists the
	// contact or more than one does: a caller never guesses an owner. Only
	// owners that are still pending are reported. The caller is the invitation
	// acceptance path, whose token has already proved control of the address.
	PendingAccountByContact(ctx context.Context, kind string, value string) (accountID string, ok bool, err error)
}
