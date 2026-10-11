// Package step holds what a login method or a second factor answers to the
// login engine, and the reading of it.
//
// A Step is one of a closed set of answers. It cannot be written as a literal:
// its fields are unexported and the constructors are the only way to make one,
// because the zero value has to have one settled reading — it reads as
// KindUnanswered, which the engine ends in a server error and records, the same
// criterion as the zero verdict. The type is a package of its own, apart from
// the value types in internal/model, because that property holds per type.
//
// The readers are functions rather than methods. A method would travel with the
// type through the alias in the root package and be callable from every method
// author, while only the engine, which sits in this capability directory, has
// reason to take an answer apart. WithSends is the one method: it is how a
// method or factor adds to an answer, so it belongs on the surface the authors
// see.
//
// Every reader that returns a payload returns the zero value of it for a Step
// of any other kind, so a reader called on the wrong kind finds nothing: a
// Proof with no source fails account resolution, and a zero Pass carries no
// method. The engine switches on KindOf first. Slices, maps and pointers a
// reader returns are copies, and those the constructors take are copied on the
// way in.
package step

import (
	"slices"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// Kind is which of the closed set of answers a Step is.
type Kind uint8

const (
	// KindUnanswered is the zero value: no constructor produced this Step. The
	// engine ends the request in a server error and records the method as
	// defective.
	KindUnanswered Kind = iota
	// KindPrompt asks the client for fields.
	KindPrompt
	// KindRedirect sends the user agent to an external party.
	KindRedirect
	// KindData returns a JSON value to the client.
	KindData
	// KindProven is the first factor holding.
	KindProven
	// KindRefused is the credential not holding.
	KindRefused
	// KindThrottled is the method declining to check for now.
	KindThrottled
	// KindUnavailable is the method not being able to say right now.
	KindUnavailable
	// KindPassed is a second factor holding.
	KindPassed
	// KindEnrolled is a factor having been registered.
	KindEnrolled
)

// String names the kind the way a log line refers to it.
func (k Kind) String() string {
	switch k {
	case KindUnanswered:
		return "unanswered"
	case KindPrompt:
		return "prompt"
	case KindRedirect:
		return "redirect"
	case KindData:
		return "data"
	case KindProven:
		return "proven"
	case KindRefused:
		return "refused"
	case KindThrottled:
		return "throttled"
	case KindUnavailable:
		return "unavailable"
	case KindPassed:
		return "passed"
	case KindEnrolled:
		return "enrolled"
	}
	return "unknown"
}

// Step is a method's answer to the engine.
type Step struct {
	kind       Kind
	name       string
	private    []byte
	fields     []model.Field
	to         string
	data       any
	proof      model.Proof
	pass       model.Pass
	record     model.FactorRecord
	cause      error
	retryAfter time.Duration
	sends      int
}

// Prompt answers that the client is to be asked for these fields. Name is
// the method's own name for the step the answer moves to, and private is the
// method's opaque state, kept by the engine as it stands.
func Prompt(name string, private []byte, fields ...model.Field) Step {
	return Step{kind: KindPrompt, name: name, private: slices.Clone(private), fields: slices.Clone(fields)}
}

// Redirect answers that the user agent is to leave for an external party at
// to. The engine requires to to be an https URL; this constructor does not
// judge it.
func Redirect(name string, private []byte, to string) Step {
	return Step{kind: KindRedirect, name: name, private: slices.Clone(private), to: to}
}

// Data answers with a JSON value for the client, such as a passkey's
// options. The value is held as it is: it cannot be copied in general, so the
// method does not change it after handing it over.
func Data(name string, private []byte, v any) Step {
	return Step{kind: KindData, name: name, private: slices.Clone(private), data: v}
}

// Proven answers that the first factor holds, with the facts proven.
func Proven(p model.Proof) Step { return Step{kind: KindProven, proof: p.Clone()} }

// Refused answers that the credential does not hold. The cause reaches the
// log and changes nothing: the client is answered the same whatever it is.
func Refused(cause error) Step { return Step{kind: KindRefused, cause: cause} }

// Throttled answers that the method declines to check for now. A retryAfter
// that is zero or negative reads as zero, which asks the engine to send no
// Retry-After.
func Throttled(retryAfter time.Duration) Step {
	return Step{kind: KindThrottled, retryAfter: max(retryAfter, 0)}
}

// Unavailable answers that the method cannot say right now.
func Unavailable(cause error) Step { return Step{kind: KindUnavailable, cause: cause} }

// Passed answers that a second factor holds.
func Passed(p model.Pass) Step { return Step{kind: KindPassed, pass: p.Clone()} }

// Enrolled answers that a factor has been registered.
func Enrolled(r model.FactorRecord) Step { return Step{kind: KindEnrolled, record: r} }

// WithSends reports how many SMS sends the call that produced this answer made
// successfully. It sets the count and does not add to an earlier one, so a
// caller composing answers carries the count over rather than doubling it. A
// negative count reads as zero, because a count the engine subtracts would
// widen the per-flow limit. The zero Step stays unanswered: a report cannot
// make an answer out of none.
func (s Step) WithSends(n int) Step {
	if s.kind == KindUnanswered {
		return s
	}
	s.sends = max(n, 0)
	return s
}

// KindOf reports which answer the step is. The zero Step reports KindUnanswered.
func KindOf(s Step) Kind { return s.kind }

// NameOf reports the method's name for the step a Prompt, Redirect or Data
// answer moves to. It is empty for every other kind.
func NameOf(s Step) string { return s.name }

// PrivateOf reports the method's opaque state a Prompt, Redirect or Data answer
// carries, as a copy. It is nil for every other kind and for an empty state.
func PrivateOf(s Step) []byte { return slices.Clone(s.private) }

// FieldsOf reports the fields a Prompt asks for, as a copy. It is nil for every
// other kind.
func FieldsOf(s Step) []model.Field { return slices.Clone(s.fields) }

// RedirectOf reports the target of a Redirect. It is empty for every other
// kind.
func RedirectOf(s Step) string { return s.to }

// DataOf reports the value of a Data answer. It is nil for every other kind.
func DataOf(s Step) any { return s.data }

// ProofOf reports the proof a Proven answer carries, as a copy. It is the zero
// Proof, which names no account, for every other kind.
func ProofOf(s Step) model.Proof { return s.proof.Clone() }

// CauseOf reports what a Refused or Unavailable answer carried. It reaches the
// log and decides nothing.
func CauseOf(s Step) error { return s.cause }

// RetryAfterOf reports how long a Throttled answer asks the client to wait. It
// is zero when the answer names no time and for every other kind.
func RetryAfterOf(s Step) time.Duration { return s.retryAfter }

// PassOf reports the pass a Passed answer carries, as a copy. It is the zero
// Pass, which carries no method, for every other kind.
func PassOf(s Step) model.Pass { return s.pass.Clone() }

// RecordOf reports the factor an Enrolled answer registered. It is the zero
// record for every other kind.
func RecordOf(s Step) model.FactorRecord { return s.record }

// SendsOf reports the SMS send count WithSends set, zero when none was.
func SendsOf(s Step) int { return s.sends }
