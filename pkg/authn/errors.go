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
// Two sentinels are declared beside the shape they belong to rather than here:
// ErrInvalidAccountID with AccountID, and ErrThrottled with Throttle. What is
// declared here is what has no shape of its own to sit beside, whether it is
// judged at startup or while serving: ErrBusy is raised by the password
// derivation governor, an internal package that imports this one and so cannot
// hold the declaration.
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

	// ErrClaimConflict reports two backends claiming the same carrier key:
	// one name reported by both, one authentication scheme or one cookie name
	// declared by both, or two non-empty account prefixes of which one begins
	// with the other. The failure names both backends and the key they
	// collide on. The fix is to change one of the two declarations.
	ErrClaimConflict = errors.New("authn: two backends claim the same name, scheme, cookie or account prefix")

	// ErrInvalidClaim reports a backend declaring something whose own shape is
	// wrong: an empty name or one not spelt [a-z0-9][a-z0-9.-]*, a scheme that
	// is not an RFC 9110 token, a cookie name that is not a cookie name, no
	// carrier at all, an account prefix a backend may not declare, or, for the
	// static credential table, an empty table, an entry name that is not one,
	// or an entry that cannot be read, which also wraps static.ErrMalformedHash
	// so that either sentinel matches with errors.Is. The failure names the
	// backend and what is wrong.
	ErrInvalidClaim = errors.New("authn: a backend's declaration is not well formed")

	// ErrInvalidConfig reports a configuration a module cannot run on: an
	// empty endpoint list, an endpoint named empty or twice, the wildcard "*"
	// written beside a named endpoint, a call budget or in-flight width that
	// is not positive, or an endpoint for authentication routes that the
	// tenant admission layer guards. The failure names the configuration item
	// to correct.
	ErrInvalidConfig = errors.New("authn: the configuration holds a value it cannot run on")

	// ErrUnknownEndpoint reports an endpoint name the entry point does not
	// have, written either into a configuration list or into a route
	// registration. The fix is to correct the name, or to declare that
	// endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("authn: endpoint name is not one the entry point declares")

	// ErrExemptionUnguarded reports a route registration, Exempt, Optional or
	// Impersonable, on an endpoint the gateway does not guard. Left
	// unreported the registration would be swallowed: the direction is
	// closed, but nothing would say so. The fix is to write the endpoint
	// into the gateway's endpoint list, or to move the registration.
	ErrExemptionUnguarded = errors.New("authn: route registration lands on an endpoint this gateway does not guard")

	// ErrAccessConflict reports one route, the same endpoint, method and
	// path, registered both Exempt and Optional, or both Exempt and
	// Impersonable. The fix is to drop one of the two.
	ErrAccessConflict = errors.New("authn: one route is registered with two access levels that exclude each other")

	// ErrRoutesUnconfigured reports a module that mounts routes finding no
	// endpoint to mount them on: none is configured and the entry point
	// declares more than one. The fix is to configure the mount endpoint.
	ErrRoutesUnconfigured = errors.New("authn: no endpoint is configured to mount routes on")

	// ErrRoutesUnguarded reports the endpoint authentication routes are
	// mounted on not being one the gateway guards. The fix is to write it
	// into the gateway's endpoint list.
	ErrRoutesUnguarded = errors.New("authn: the endpoint for authentication routes is not one the gateway guards")

	// ErrInsecureConfig reports a public base URL that is not https while
	// insecure use was not allowed, or a same-site setting that is none. The
	// fix is to change the address, or to allow insecure use explicitly,
	// which is for development.
	ErrInsecureConfig = errors.New("authn: the configuration weakens a protection it has not been told to give up")

	// ErrBusy reports that a password derivation was refused because the
	// limit on concurrent derivations or on their memory was reached. The
	// attempt did not verify anything. A gateway that finds it inside the
	// cause of an Unavailable verdict answers 503 with Retry-After: 1, which
	// no other cause of a 503 carries.
	ErrBusy = errors.New("authn: password derivation capacity is in use")

	// ErrDrainTimeout reports that Close had waited the call budget for the
	// calls inside a backend and some were still there. The calls go on
	// running; what Close gives up is the wait, so that a backend which has
	// stopped answering cannot hold the whole shutdown open.
	ErrDrainTimeout = errors.New("authn: backend calls were still in flight after the call budget")
)
