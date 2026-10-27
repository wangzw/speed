package authn

import "context"

// Factor is one kind of second factor. A module delivers it without claiming it,
// so several stand side by side, and only the multi-factor module takes them,
// with core.ResolveAll in its Init. One module has one Kind, and two factors of
// one kind are ErrFactorConflict.
//
// A factor reports through Step like a login method: StepPassed when a second
// factor holds and StepEnrolled when one is registered. It touches no HTTP, no
// session and no flow record, and it throttles nothing and writes no audit row,
// because the multi-factor module does those around every call it makes.
type Factor interface {
	// Kind is the kind of factor this module is.
	Kind() FactorKind
	// Enroll registers a factor, one step at a time.
	Enroll(ctx context.Context, f FlowState, in StepInput) (Step, error)
	// Challenge starts a verification for a registered factor: it sends a code or
	// makes a passkey's options. A TOTP has nothing to do.
	Challenge(ctx context.Context, f FlowState, rec FactorRecord) (Step, error)
	// Verify checks a second factor. A TOTP counter or a passkey signature count
	// that replays is ErrReplay, and a factor still pending is
	// ErrFactorNotEnrolled.
	Verify(ctx context.Context, f FlowState, rec FactorRecord, in StepInput) (Step, error)
	// Remove deletes the kind-specific data of a factor, a TOTP seed or a passkey's
	// public key, and is idempotent.
	Remove(ctx context.Context, account AccountID, rec FactorRecord) error
}

// MFA is the multi-factor module: the one place the policy is decided, factors
// are registered, verified and removed, and the assurance of a login is
// composed. It is delivered exclusively. The login engine requires it: an
// assembly with factors registered and no module here would let a login skip the
// second factor without a word.
type MFA interface {
	// Factors lists the account's factors with their status, and nothing of
	// theirs that is secret.
	Factors(ctx context.Context, id AccountID) ([]FactorRecord, error)
	// Require decides the outcome for a login whose first factor is proven. The
	// requirement account is the one whose factors and policy decide whether a
	// second factor is needed; the proof account is the one whose factors may be
	// offered. They are the same account for an ordinary login, and apart when an
	// impersonating identity proves its own factors for the impersonated account.
	// For MFAFactor it returns the usable factors of the proof account: active
	// ones whose methods do not intersect first.Methods. The list may be empty, in
	// which case the login cannot be completed.
	Require(ctx context.Context, proof, requirement AccountID, first Proof) (MFAOutcome, []FactorRecord, error)
	// Challenge starts a verification on the chosen factor, after checking the
	// factor's throttle: a factor in back-off is sent nothing.
	Challenge(ctx context.Context, f FlowState, factorID string) (Step, error)
	// Verify checks the factor's throttle, calls Factor.Verify, then counts the
	// failure or clears the key, and writes the audit row and the event.
	Verify(ctx context.Context, f FlowState, factorID string, in StepInput) (Step, error)
	// Enroll is the only way a factor is registered: it checks the limit per kind,
	// writes a pending row, calls Factor.Enroll and makes the row active on the
	// last step.
	Enroll(ctx context.Context, f FlowState, kind FactorKind, in StepInput) (Step, error)
	// Assurance composes the assurance level of a login from the first factor's
	// proof and the second factor's pass, which is nil when there was none. It is
	// the one place a level is computed. It reads no store.
	Assurance(first Proof, second *Pass) MFAResult
	// Remove removes a factor's kind-specific data and then its row. It ends no
	// session and promises nothing about the account's sessions: a caller that
	// wants the other devices signed out does it after a successful removal of an
	// active factor, keeping the session that asked. The last factor of an
	// account that requires one is ErrLastFactor.
	Remove(ctx context.Context, id AccountID, factorID string) error
	// Reset disables the account's factors and returns how many. Authorizing it
	// and checking the strength of the administrator are the handler's work.
	Reset(ctx context.Context, id AccountID, reason string) (removed int, err error)
	// Seasoned reports whether the account has an active factor of a kind other
	// than SMS that has been active for the sensitive-min-age setting.
	Seasoned(ctx context.Context, id AccountID) (bool, error)
	// Kinds lists the kinds of factor taken in, for the startup diagnostic and for
	// telling a factor that is not available at login.
	Kinds() []FactorKind
}
