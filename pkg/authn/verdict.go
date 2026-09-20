package authn

import "github.com/vislake/speed/pkg/authn/internal/verdict"

// Verdict is what one backend answers about one credential.
//
// A verdict cannot be written as a literal: its fields are unexported and the
// three constructors below are the only way to produce one. The criterion is
// that the zero value must have a settled reading, and it has one — it reads as
// said-nothing, which ends the request in a refusal and is counted apart from a
// refusal a backend meant. The shapes that were rejected, and why each was, are
// in ADR-authn-verdict-2026-09-20.
//
// Reading a verdict apart again is the gateway's work and is not on this
// surface: a backend answers, it does not inspect.
type Verdict = verdict.Verdict

// Authenticated answers that the credential holds for this subject.
//
// The subject has to be one this backend owns. The gateway checks that against
// the ownership table it built and refuses the request otherwise, because a
// backend owning a small corner of the subject space could else mint any
// identity at all.
func Authenticated(subject string) Verdict { return verdict.NewAuthenticated(subject) }

// Denied answers that the credential does not hold.
//
// This is final: the request ends here and no other backend is asked. The cause
// reaches the log and changes nothing, so a backend cannot turn a wrong
// password into an outage by choosing what to put in it.
func Denied(cause error) Verdict { return verdict.NewDenied(cause) }

// Unavailable answers that this backend cannot say right now.
//
// The subjects it owns are not handed to anyone else while it is in this state.
// Taking them over would let the store that knows least decide, which is the
// reading an account disabled in one place and stale in another turns on.
func Unavailable(cause error) Verdict { return verdict.NewUnavailable(cause) }
