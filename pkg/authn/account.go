package authn

import (
	"context"
	"time"
)

// Accounts is the read surface of the account store. The account module
// delivers it exclusively, and the modules and hosts that look an account up
// take it by this interface.
//
// Every method reports an account that does not exist, a deleted one included,
// as ErrAccountNotFound. A deleted account is a tombstone that holds no personal
// data. IdentifiersMany is the one exception: it leaves such an id out of its
// map instead, so one account gone from a page does not fail the page.
type Accounts interface {
	// Get reads one account.
	Get(ctx context.Context, id AccountID) (Account, error)
	// Lookup finds the account an identifier belongs to. value is normalized
	// first, and one that cannot be is ErrInvalidIdentifier.
	Lookup(ctx context.Context, kind IdentifierKind, value string) (AccountID, error)
	// ByExternal finds the account an external identity is bound to. The key
	// is the provider and the subject, never an email address.
	ByExternal(ctx context.Context, provider, subject string) (AccountID, error)
	// Identifiers lists the identifiers bound to an account.
	Identifiers(ctx context.Context, id AccountID) ([]Identifier, error)
	// IdentifiersMany lists the identifiers of several accounts in one read. The
	// map has an entry for every id that names a live account, each holding what
	// Identifiers returns for it; an id that names no account, or a tombstone,
	// has no entry rather than an error. An empty input is an empty map.
	IdentifiersMany(ctx context.Context, ids []AccountID) (map[AccountID][]Identifier, error)
	// Externals lists the external identities bound to an account.
	Externals(ctx context.Context, id AccountID) ([]ExternalBinding, error)
	// List returns one page of accounts and the cursor of the next, which is
	// empty when there is none. A deleted account is left out unless the query
	// asks for StatusDeleted. An identifier prefix is normalized and at least
	// three characters, so that a listing cannot scan the whole table.
	List(ctx context.Context, q AccountQuery) ([]Account, string, error)
	// ListServiceSecrets lists the secrets of a service account: identifier,
	// label, expiry and revocation, never a secret.
	ListServiceSecrets(ctx context.Context, id AccountID) ([]ServiceSecretInfo, error)
}

// AccountAdmin is the write surface of the account store, which the account
// module delivers exclusively. Its callers are this capability's own modules
// and the host's commands; the administration routes sit above it and ask a
// Decider first. A call made through this interface asks nobody: the host's
// command is responsible for its own access control.
type AccountAdmin interface {
	// Create writes an enabled account. An email and a phone must already be
	// verified, and the way each was comes in EmailVerifiedVia and
	// PhoneVerifiedVia; there is no row awaiting verification. An email address
	// also writes its contact reservation, verified now, in the same
	// transaction. Password and PasswordHash are alternatives, the first checked
	// against the password policy and the second a PHC or legacy hash for an
	// import. An empty ID generates one; a given one is for an import. An
	// identifier, a login name or a binding that breaks a uniqueness constraint
	// is ErrAccountExists, and a contact another account reserved or proved is
	// ErrContactTaken. Once the transaction has committed, every
	// AccountLifecycle.AccountCreated hook is called: a hook that fails makes the
	// call return the created account with ErrCleanupIncomplete, and repeating the
	// hook finishes the cleanup.
	Create(ctx context.Context, in CreateAccount) (Account, error)
	// SetStatus moves an account between disabled and enabled, and Delete ends
	// it. Both write the account's own tables in one transaction and then call
	// every AccountLifecycle hook: a hook that fails makes the call return
	// ErrCleanupIncomplete with the new state in force, and repeating the call
	// finishes the cleanup. reason is one of the Reason constants that
	// ValidPayloadReasonClass accepts, and any other is ErrInvalidArgument.
	SetStatus(ctx context.Context, id AccountID, s AccountStatus, reason string) error
	// Delete ends an account for good; see SetStatus.
	Delete(ctx context.Context, id AccountID, reason string) error
	// SetDisplayName sets the display name, at most 100 characters; a longer one
	// is ErrInvalidArgument.
	SetDisplayName(ctx context.Context, id AccountID, name string) error
	// SetAttributes replaces the account's attribute set, which is held to the
	// bounds of AccountAttributesMax and the constants beside it; a set that
	// breaks them is ErrInvalidArgument.
	SetAttributes(ctx context.Context, id AccountID, attrs map[string]string) error
	// SetProtected sets whether the account may be impersonated. A protected
	// account may not be, and no Decider overrules that.
	SetProtected(ctx context.Context, id AccountID, protected bool) error
	// SetMFARequired sets whether the account must register a factor.
	SetMFARequired(ctx context.Context, id AccountID, required bool) error
	// AddIdentifier binds an identifier, normalized first. via is one of the
	// forms Verification.Valid accepts, and anything else is ErrInvalidArgument.
	// An email address also writes its contact reservation, verified now: an
	// existing reservation of this account is brought up to date, and one
	// another account holds is ErrContactTaken.
	AddIdentifier(ctx context.Context, id AccountID, kind IdentifierKind, value string, via Verification) error
	// RemoveIdentifier unbinds one identifier by its identifier. Removing an
	// email address also releases the account's reservation of that contact.
	RemoveIdentifier(ctx context.Context, id AccountID, identifierID string) error
	// LinkExternal binds an external identity. One already bound to another
	// account is ErrBindingConflict.
	LinkExternal(ctx context.Context, id AccountID, l ExternalLink) error
	// UnlinkExternal removes a binding. When no other way to log in would remain
	// the call is refused with ErrLastLoginMethod.
	UnlinkExternal(ctx context.Context, id AccountID, externalID string) error
	// HasLoginWayOtherThanPasskey reports whether the account can log in by a
	// way that is not a factor: a password credential, a phone number or an
	// external identity. A passkey does not count.
	HasLoginWayOtherThanPasskey(ctx context.Context, id AccountID) (bool, error)
	// CreateServiceSecret makes a secret for a service account: 256 random bits
	// under the prefix spd_cs_, of which the store keeps only a SHA-256. The
	// secret is in the return value once and never again.
	CreateServiceSecret(ctx context.Context, id AccountID, label string, expires *time.Time) (secretID, secret string, err error)
	// RevokeServiceSecret revokes a secret and then calls
	// AccountLifecycle.ServiceSecretRevoked; a failing hook is
	// ErrCleanupIncomplete, as for SetStatus.
	RevokeServiceSecret(ctx context.Context, id AccountID, secretID string) error
	// Touch records the time of the account's last login. It is a raw write and
	// leaves no audit row.
	Touch(ctx context.Context, id AccountID, at time.Time) error
}

// Passwords checks and writes password-class credentials: the passwords of
// accounts and the secrets of service accounts. The account module delivers it
// exclusively.
type Passwords interface {
	// Verify checks a password. identifier is normalized first: one containing
	// an @ is an email address, any other a username, and a phone number is no
	// login name. An unknown identifier, a disabled account and an account with
	// no password cost one decoy derivation at the current parameters and are all
	// ErrInvalidCredentials, as is a wrong password; the account-level back-off
	// counts silently and past its threshold still derives and answers
	// ErrInvalidCredentials, never a throttle. VerifyResult.MustChange says the
	// credential carries must_change.
	Verify(ctx context.Context, identifier, password string) (AccountID, VerifyResult, error)
	// Set writes a password after the policy: a violation is ErrPasswordPolicy,
	// and so is a new password equal to the current one. The options attribute
	// the change to self, admin or reset and name a channel on a reset; they can
	// also require, inside the write transaction, an enabled account, an existing
	// credential and an identifier that still belongs to the account, any of
	// which unsatisfied is ErrAccountUnavailable, and move valid_after in that
	// same transaction. Set revokes no session: the handler that changed the
	// password calls Sessions.RevokeAccount once it succeeded, which is why this
	// interface does not depend on the session module.
	Set(ctx context.Context, id AccountID, password string, opt SetPasswordOptions) error
	// ImportHash writes a PHC or legacy entry as the account's password
	// credential. A legacy entry is converted without loss, the salt and the key
	// being the same bytes under another wrapper.
	ImportHash(ctx context.Context, id AccountID, encoded string, mustChange bool) error
	// VerifyServiceSecret checks a service account's client credential. The row
	// is found by the SHA-256 of secret and must be neither revoked nor expired;
	// its account must equal clientID, compared in constant time, and be an
	// enabled service account. It returns the account, the secret's identifier
	// and the expiry, where the zero time means it does not expire.
	VerifyServiceSecret(ctx context.Context, clientID, secret string) (account AccountID, secretID string, expires time.Time, err error)
	// Clear deletes the account's password credential, so that the account has
	// none and must set one again. It is idempotent: an account with no
	// credential is nil. The login engine calls it after an automatic merge into
	// an existing account, which must not leave that account's old password in
	// the hands of whoever arrived by the merge.
	Clear(ctx context.Context, id AccountID) error
}
