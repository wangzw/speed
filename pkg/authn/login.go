package authn

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/step"
)

// Step is a login method's, or a second factor's, answer to the login engine.
//
// Its fields are unexported and only the constructors below and WithSends
// produce one. The zero value reads as no answer, which the engine ends in a
// server error and records, the same criterion as the zero Verdict. The kinds of
// answer are the closed set the constructors give. Reading an answer apart is
// the engine's work and is not on this surface: a method answers, it does not
// inspect.
//
// WithSends reports how many SMS sends the call that produced the answer made
// successfully. It sets the count rather than adding to an earlier one, a
// negative count reads as zero, and the zero Step stays unanswered.
type Step = step.Step

// StepPrompt answers that the client is to be asked for fields. Step names the
// method's own next step, and private is the method's opaque state, kept by the
// engine as it stands.
func StepPrompt(name string, private []byte, fields ...Field) Step {
	return step.Prompt(name, private, fields...)
}

// StepRedirect answers that the user agent is to leave for an external party at
// to, which the engine requires to be an https URL.
func StepRedirect(name string, private []byte, to string) Step {
	return step.Redirect(name, private, to)
}

// StepData answers with a JSON value for the client, such as a passkey's
// options. The value is held as it is: the method does not change it after
// handing it over.
func StepData(name string, private []byte, v any) Step { return step.Data(name, private, v) }

// StepProven answers that the first factor holds, with the facts proven.
func StepProven(p Proof) Step { return step.Proven(p) }

// StepRefused answers that the credential does not hold. The client is answered
// the same whatever the cause is; the cause reaches the log.
func StepRefused(cause error) Step { return step.Refused(cause) }

// StepThrottled answers that the method declines to check for now. A retryAfter
// that is zero or negative asks the engine to send no Retry-After.
func StepThrottled(retryAfter time.Duration) Step { return step.Throttled(retryAfter) }

// StepUnavailable answers that the method cannot say right now. It is not a
// refusal: the two are counted apart.
func StepUnavailable(cause error) Step { return step.Unavailable(cause) }

// StepPassed answers that a second factor holds. Only a factor uses it.
func StepPassed(p Pass) Step { return step.Passed(p) }

// StepEnrolled answers that a factor has been registered. Only a factor uses it.
func StepEnrolled(r FactorRecord) Step { return step.Enrolled(r) }

// LoginMethod is one kind of first factor: a password, an SMS code, a social or
// enterprise redirect, a passkey. A module delivers it without claiming it, so
// several stand side by side, and only the login engine takes them, with
// core.ResolveAll in its Init. A third party's method is held to the same rules
// as this repository's.
//
// A method answers with a Step and does nothing else. It answers within ctx,
// because the engine sets a budget per call that covers an external provider's
// round trips. When what it reads from cannot be reached it answers
// StepUnavailable and not StepRefused, so that a 503 and a 401 are counted
// apart. It does not create an account, merge, make a session, touch HTTP, gin,
// a session or a token, and it does not read or write the flow record, whose
// compare-and-swap belongs to the engine. Failing must not differ in time or in
// the shape of the answer by whether an identifier exists. A method that sends an
// SMS checks SendsLeft before every send and reports the sends it made with
// Step.WithSends. A panic is recovered by the engine and ends in a 503 for that
// method alone, so one method's defect reaches no other.
//
// A method depends on neither HTTP nor storage, and its tests build a Start, a
// FlowState and a StepInput and read the kind of the answer.
type LoginMethod interface {
	// Name is the method's stable name: it appears in routes, flow records and
	// metric attributes, and a published name never changes. It is unique across
	// the assembly, and two methods with one name are ErrMethodConflict.
	Name() string
	// Begin starts a flow for a purpose.
	Begin(ctx context.Context, s Start) (Step, error)
	// Answer handles the client's answer to the previous step, or an external
	// party's redirect back: an OAuth code, a SAML assertion.
	Answer(ctx context.Context, f FlowState, in StepInput) (Step, error)
}
