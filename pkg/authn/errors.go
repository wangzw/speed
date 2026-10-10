package authn

import "errors"

// The failures this capability directory judges for itself. A caller tells
// them apart with errors.Is, and every wrap on the way out uses %w so the
// chain stays whole.
//
// The set is closed over what this capability directory produces: a newly
// judged class of failure either joins one of these or earns another, and none
// of them is invented for a failure that merely passed through. A backend's own
// errors are its own to name; the gateway carries them out unchanged.
//
// Taking up the configuration reader is the exception that stays outside: a
// reader that is not there means the host never supplied one, which is core's
// ErrMissingProvider and is carried out as it stands. The fix is the host's,
// not a defect this capability directory judged.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register
	// on. The fix is to write the entry implementation's module name into
	// this command: a command that includes authentication and no entry
	// point has nothing to authenticate.
	ErrNoRouter = errors.New("authn: no HTTP entry point in this command")

	// ErrNoVerifier reports that Init took up no credential-verifying
	// backend. The fix is to write the chosen backend's module name into
	// this command: a backend that was named but cannot start has already
	// stopped the startup with its own failure, so reaching here means no
	// backend was named at all.
	ErrNoVerifier = errors.New("authn: no credential-verifying backend in this command")

	// ErrClaimConflict reports two backends owning the same exact key: one
	// authentication scheme claimed whole by both, one subject written out
	// by both, or one name reported by both — the name an identity's owner
	// qualification is written with. The fix is to change one of the two
	// claims.
	ErrClaimConflict = errors.New("authn: two backends claim the same name, subject or scheme")

	// ErrInvalidClaim reports a claim whose own shape is wrong: a subject
	// that is empty or blanks alone, a subject carrying Separator, an open
	// claim from a backend that does not implement Owner, a backend
	// reporting an empty name or one carrying Separator, or a claim of no
	// subject space at all.
	ErrInvalidClaim = errors.New("authn: a backend's claim is not well formed")

	// ErrInvalidConfig reports a gateway configuration the gateway cannot
	// run on: no endpoint to guard, an endpoint named empty or twice, or a
	// call budget or in-flight width that is not positive. The failure
	// names the configuration item to correct. An endpoint list that is
	// absent altogether wraps config.ErrMissingRequired as well.
	ErrInvalidConfig = errors.New("authn: the gateway's configuration holds a value it cannot run on")

	// ErrUnknownEndpoint reports an endpoint name the entry point does not
	// have, written either into the gateway's own endpoints list or into an
	// exemption. The fix is to correct the name, or to declare that
	// endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("authn: endpoint name is not one the entry point declares")

	// ErrExemptionUnguarded reports an exemption on an endpoint this
	// gateway does not guard. Left unreported the exemption would be
	// swallowed: the direction is closed, but nothing would say so.
	ErrExemptionUnguarded = errors.New("authn: exemption lands on an endpoint this gateway does not guard")

	// ErrDrainTimeout reports that Close had waited the call budget for the
	// calls inside a backend and some were still there. The calls go on
	// running; what Close gives up is the wait, so that a backend which has
	// stopped answering cannot hold the whole shutdown open.
	ErrDrainTimeout = errors.New("authn: backend calls were still in flight after the call budget")
)
