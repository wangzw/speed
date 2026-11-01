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

// MethodKind is the class a client renders a login method by. It is how the
// method is drawn, not how a flow advances: a form and a redirect both start a
// flow, and a redirect and an enterprise entry both leave for an external
// party. The set is closed.
type MethodKind string

// The rendering classes of a login method.
const (
	// MethodKindForm renders a form and starts a flow with what it submits.
	MethodKindForm MethodKind = "form"
	// MethodKindRedirect renders a button that starts a flow and hands the user
	// agent the redirect step the method answers with.
	MethodKindRedirect MethodKind = "redirect"
	// MethodKindEnterprise renders an email-first entry that discovers the
	// identity provider before it leaves for it.
	MethodKindEnterprise MethodKind = "enterprise"
	// MethodKindWebAuthn renders a passkey ceremony button, whose start answers
	// the client a data step it completes before submitting.
	MethodKindWebAuthn MethodKind = "webauthn"
)

// MethodProtocol is the wire protocol a redirect or enterprise method speaks.
type MethodProtocol string

// The protocols a redirect or enterprise method speaks.
const (
	MethodProtocolOAuth2 MethodProtocol = "oauth2"
	MethodProtocolOIDC   MethodProtocol = "oidc"
	MethodProtocolSAML   MethodProtocol = "saml"
)

// MethodDescriptor is what a login method reports about itself for the discovery
// endpoint, so that a client renders the ways in without knowing their names.
//
// Name is the method's stable name. Kind is the class the client renders it by.
// Protocol is the wire protocol a redirect or enterprise method speaks, and is
// empty otherwise. Label is the method's brand name where it has one — a
// provider's own name, which is not translated — and is empty for a client to
// fall back to a name-keyed translation. Fields is the inputs the method's first
// step asks for, being what Begin answers for a start that carries none, and is
// empty for a method that does not open with a form. OpensAccount reports
// whether the method may open an account for an identifier it does not yet know.
//
// BindingProviders is the provider values the method's link flow stores for the
// identities it binds, so that a client can tell whether a method is already
// bound without knowing the storage naming. Each entry is either the exact
// value, as "google", or a namespace prefix the stored value continues after, as
// "oidc:", "saml:" and "wechat-openid:". A stored provider value belongs to the
// method when it equals an entry or begins with one. The field may be empty for
// a method whose bindings land under Name: empty means the bindings land under
// Name, and is also the value for a method that binds no external identity.
type MethodDescriptor struct {
	Name             string
	Kind             MethodKind
	Protocol         MethodProtocol
	Label            string
	Fields           []Field
	OpensAccount     bool
	BindingProviders []string
}

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
	// Describe reports how the method is drawn in the discovery document. It is
	// a constant of the method: it reads no store and no configuration, and it
	// carries no value a login would.
	Describe() MethodDescriptor
	// Begin starts a flow for a purpose.
	Begin(ctx context.Context, s Start) (Step, error)
	// Answer handles the client's answer to the previous step, or an external
	// party's redirect back: an OAuth code, a SAML assertion.
	Answer(ctx context.Context, f FlowState, in StepInput) (Step, error)
}

// MethodAbandoner is the optional part of a method -- a first-factor method or
// the multi-factor module -- that keeps state of its own for a flow and
// releases it when the flow is abandoned. The engine finds it with a type
// assertion on the collaborator the flow's current step belongs to and runs it
// before the flow record is removed. A method that keeps no state of its own
// does not implement it.
//
// Abandon is idempotent: the same flow may be released more than once, and a
// flow that never reached a step of the method's carries no private state. The
// engine logs a failure and removes the flow record all the same, so an
// implementation reports what it could not release and does not decide whether
// the flow ends.
type MethodAbandoner interface {
	Abandon(ctx context.Context, f FlowState) error
}
