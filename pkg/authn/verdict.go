package authn

import "github.com/vislake/speed/pkg/authn/internal/verdict"

// Verdict is what one backend answers about one credential.
//
// A verdict cannot be written as a literal: its fields are unexported and the
// three constructors below are the only way to produce one. The criterion is
// that the zero value must have a settled reading, and it has one — it reads as
// said-nothing, which ends the request in a refusal and is counted apart from a
// refusal a backend meant. The shapes that were rejected, and why each was, are
// in ADR-authn-verdict-2026-10-10.
//
// Reading a verdict apart again is the gateway's work and is not on this
// surface: a backend answers, it does not inspect.
type Verdict = verdict.Verdict

// Authenticated answers that the credential holds, and carries the facts the
// backend claims about the caller.
//
// The principal is the backend's claim and not an identity: the gateway checks
// it item by item — the shape and bounds of each field, that the account and
// the actor fall inside the account namespace the backend declared in Accepts,
// and that it names no actor, impersonation scope or SSO provider unless the
// backend declared it may — and stamps an Identity from it only when all of
// them hold. The principal is copied, so changing it after returning changes
// nothing the gateway reads.
func Authenticated(p Principal) Verdict { return verdict.NewAuthenticated(p) }

// Denied answers that the credential does not hold.
//
// This is final: the request ends here, in a 401. The cause
// reaches the log and changes nothing, so a backend cannot turn a wrong
// password into an outage by choosing what to put in it.
func Denied(cause error) Verdict { return verdict.NewDenied(cause) }

// Unavailable answers that this backend cannot say right now.
//
// The request ends in a 503 and is never read as anonymous, not even on a route
// that accepts a request with no credential: reading an outage as no credential
// would let a caller who can make a backend fail walk past authentication.
func Unavailable(cause error) Verdict { return verdict.NewUnavailable(cause) }
