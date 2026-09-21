package authn

import "errors"

// The failures this release unit judges for itself. A caller tells them apart
// with errors.Is, and every wrap on the way out uses %w so the chain stays
// whole.
//
// The set is closed over what this release unit produces: a newly judged class
// of failure either joins one of these or earns another, and none of them is
// invented for a failure that merely passed through. A backend's own errors are
// its own to name; the gateway carries them out unchanged.
//
// Taking up the configuration reader is the exception that stays outside: a
// reader that is not there means the host never supplied one, which is core's
// ErrMissingProvider and is carried out as it stands. The fix is the host's,
// not a defect this unit judged.
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
	// authentication scheme claimed whole by both, or one subject written
	// out by both. The fix is to change one of the two claims.
	ErrClaimConflict = errors.New("authn: two backends claim the same subject or scheme")

	// ErrInvalidClaim reports a claim whose own shape is wrong: a subject
	// that is empty or blanks alone, a subject carrying Separator, an open
	// claim from a backend that does not implement Owner, or a backend
	// reporting an empty name.
	ErrInvalidClaim = errors.New("authn: a backend's claim is not well formed")

	// ErrUnknownEndpoint reports an exemption naming an endpoint the entry
	// point does not have. The fix is to correct the name, or to declare
	// that endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("authn: exemption names an endpoint that does not exist")

	// ErrExemptionUnguarded reports an exemption on an endpoint this
	// gateway does not guard. Left unreported the exemption would be
	// swallowed: the direction is closed, but nothing would say so.
	ErrExemptionUnguarded = errors.New("authn: exemption lands on an endpoint this gateway does not guard")

	// ErrUnmatchablePattern reports a pattern the matcher will not take.
	// The engine and the matcher are two parsers, and a pattern the engine
	// accepts is only known to be one the matcher accepts when the matcher
	// is built.
	ErrUnmatchablePattern = errors.New("authn: exemption pattern is not one the matcher accepts")
)
