package authn

import "context"

// AccountLifecycle is the hook a module that holds data keyed by an account
// implements, so that the account module can have that data cleaned after the
// account changes state. The session, factor, SMS and policy modules implement
// it; the rule for who does is holding data keyed by an account, and the list is
// examples, not a closed set. Implementations stand side by side and none is
// exclusive. The account module takes all of them with core.ResolveAll in its
// Init and does not list them in Requires, so an assembly without one of those
// modules still runs.
//
// The hooks are called after the account's transaction has committed and not in
// it. Every method is idempotent: an account already disabled, already enabled,
// already deleted or already clean is nil. An implementation does not read the
// account row, which holds nothing but a tombstone once the account is deleted. A
// hook that fails makes the account call return ErrCleanupIncomplete with the new
// state in force, and a repeated call finishes the cleanup; the same cleanup is
// also driven by the account events, where an outbox is present.
type AccountLifecycle interface {
	// AccountDisabled revokes the account's browser and token sessions and clears
	// their cache. Service sessions lapse with the account's status and its
	// valid-after instant, and come back through Sessions.ForCredential after the
	// account is enabled again. Factors and SMS challenges stay with the account.
	AccountDisabled(ctx context.Context, id AccountID) error
	// AccountEnabled gives back what the module wound down on a disable. A module
	// with nothing to restore returns nil: the sessions of before stay void,
	// because the disabling moved the account's valid-after instant on and
	// enabling does not move it back, and factors and challenges stay where they
	// were.
	AccountEnabled(ctx context.Context, id AccountID) error
	// AccountDeleted erases what the module holds for the account: its session
	// rows, its factors and their subordinate data, its SMS challenge rows.
	AccountDeleted(ctx context.Context, id AccountID) error
	// ServiceSecretRevoked revokes the service session of the revoked secret.
	ServiceSecretRevoked(ctx context.Context, id AccountID, credentialID string) error
}
