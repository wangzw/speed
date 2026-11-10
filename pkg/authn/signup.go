package authn

import (
	"context"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// SignupState is how far an account's email verification has got. It is
// independent of AccountStatus: a pending account is an enabled account that
// holds an unproved email address. The zero value is SignupNone, which means
// the account has no email address to verify, and Valid reports whether a value
// is one of the three.
type SignupState = model.SignupState

// The states of an account's email verification.
const (
	SignupNone     = model.SignupNone     // the zero value: no email address to verify
	SignupPending  = model.SignupPending  // holds an email address that has not been proved
	SignupVerified = model.SignupVerified // holds an email address whose ownership was proved
)

// SignupStatus is the email-verification state as a request reads it: State,
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
	// The email address is reserved by inserting its contact row in the same
	// transaction, and that insert is the atomic arbiter: an address another
	// account has reserved or proved is ErrContactTaken. An unproved reservation
	// is accepted, and it lasts until the address is proved or the account is
	// deleted.
	// Deriving the password uses the signup-concurrency capacity of its own, so
	// a full capacity is ErrBusy and never takes a place from a password login.
	// No email is sent, no session is created and no identifier awaits
	// verification. The account.created event carries via signup. Once the
	// transaction has committed, every AccountLifecycle.AccountCreated hook is
	// called: a hook that fails makes the call return the created account with
	// ErrCleanupIncomplete, and repeating the hook finishes the cleanup.
	Register(ctx context.Context, in RegisterAccount) (Account, error)
}

// SignupVerification is the read surface of an account's email verification.
// The account module delivers it exclusively. Every method reads the database on
// the request: the state is in no account snapshot, session or token.
type SignupVerification interface {
	// SignupStateOf reports how far the account's email verification has got.
	// State is SignupVerified when the account holds an email identifier whose
	// way of verification proves ownership, SignupPending when it holds an email
	// address that has not been proved (an identifier written through admin,
	// import-unverified or self, or a reservation awaiting verification), and
	// SignupNone when it has no email address at all. A kind other than user
	// reads SignupNone. Email is the earliest-proved address for Verified (ties
	// broken by identifier), the earliest unproved address for Pending (ties
	// broken by identifier), and empty for None. Since is the first instant the
	// account reached Verified, the zero time while it never did, and the zero
	// time for None. An account that does not exist is ErrAccountNotFound, and
	// an unavailable database is ErrUnavailable: a read failure is never
	// answered as SignupNone.
	SignupStateOf(ctx context.Context, id AccountID) (SignupStatus, error)
	// PendingAccountByContact finds the one account an unproved contact
	// reservation belongs to. kind is email or phone, value is normalized first
	// (one that cannot be is ErrInvalidIdentifier), and ok is false when no
	// account reserved the contact or more than one does: a caller never guesses
	// an owner. Only owners that are still pending are reported, and a row whose
	// verified_at is set is never one: that contact is held by the identifier
	// table. The caller must have established control of the address through a
	// proof of its own; an invitation token is not one, so the invitation module
	// does not call this method.
	PendingAccountByContact(ctx context.Context, kind string, value string) (accountID string, ok bool, err error)
}
