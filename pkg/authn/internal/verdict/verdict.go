// Package verdict holds what a credential-verifying backend answers, and the
// reading of it.
//
// The type lives here rather than in the interface package so that answering
// and reading can be separated. A backend outside this capability directory
// answers through the constructors the interface package re-exports; it cannot
// reach the readers below, because Go admits an internal package to the subtree
// it sits in and to nothing outside. The gateway, which is in that subtree,
// reads.
//
// The readers are functions rather than methods for the same reason: a method
// would travel with the type through the alias in the interface package and be
// callable from everywhere.
package verdict

// Verdict is what one backend answers about one credential.
//
// The fields are unexported and only the constructors set them, because the
// zero value has to have one settled reading. It reads as Unspoken: a backend
// that returns before it answers, or that builds a Verdict some way its author
// did not intend, ends the request in a refusal rather than in a pass.
type Verdict struct {
	outcome Outcome
	subject string
	cause   error
}

// Outcome is which of the four things a verdict is.
type Outcome int

const (
	// Unspoken is the zero value: no constructor produced this verdict. It
	// is counted apart from a refusal — a refusal says the credential does
	// not hold, this says the backend is defective — and both end the
	// request the same way.
	Unspoken Outcome = iota
	// Authenticated is the credential holding for the subject carried.
	Authenticated
	// Denied is the credential not holding. It is final.
	Denied
	// Unavailable is the backend not being able to say right now.
	Unavailable
)

// String names the outcome the way a log line refers to it. These are the
// counting buckets a dashboard reads, so they are fixed here.
func (o Outcome) String() string {
	switch o {
	case Unspoken:
		return "unspoken"
	case Authenticated:
		return "authenticated"
	case Denied:
		return "denied"
	case Unavailable:
		return "unavailable"
	}
	return "unknown"
}

// NewAuthenticated answers that the credential holds for this subject.
func NewAuthenticated(subject string) Verdict {
	return Verdict{outcome: Authenticated, subject: subject}
}

// NewDenied answers that the credential does not hold. The cause reaches the
// log and changes nothing, so a backend cannot turn a wrong password into an
// outage by choosing what to put in it.
func NewDenied(cause error) Verdict {
	return Verdict{outcome: Denied, cause: cause}
}

// NewUnavailable answers that this backend cannot say right now.
func NewUnavailable(cause error) Verdict {
	return Verdict{outcome: Unavailable, cause: cause}
}

// OutcomeOf reports which of the four things the verdict is. The zero Verdict
// reports Unspoken.
func OutcomeOf(v Verdict) Outcome { return v.outcome }

// SubjectOf reports the subject an authenticated verdict carries. It is empty
// for every other outcome.
func SubjectOf(v Verdict) string { return v.subject }

// CauseOf reports what a refusal or an unavailability carried. It reaches the
// log and decides nothing.
func CauseOf(v Verdict) error { return v.cause }
