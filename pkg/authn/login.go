package authn

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/step"
)

// Step is a login method's, or a second factor's, answer to the login engine. Its
// zero value reads as no answer, which the engine ends in a server error; WithSends
// sets the count rather than adding, and a negative count reads as zero.
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

// StepData answers with a JSON value for the client, such as a passkey's options;
// the value is held as it is, not changed after it is handed over.
func StepData(name string, private []byte, v any) Step { return step.Data(name, private, v) }

// StepProven answers that the first factor holds, with the facts proven.
func StepProven(p Proof) Step { return step.Proven(p) }

// StepRefused answers that the credential does not hold; the client is answered the
// same whatever the cause is, and the cause reaches the log.
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

// MethodKind is the class a client renders a login method by: how it is drawn, not
// how a flow advances. The set is closed.
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
// endpoint. BindingProviders are the provider values its link flow stores: an exact
// value or a namespace prefix, and empty means the bindings land under Name.
type MethodDescriptor struct {
	// Name is the method stable name.
	Name string
	// Kind is the rendering class.
	Kind MethodKind
	// Protocol is the wire protocol a redirect or enterprise method speaks.
	Protocol MethodProtocol
	// Label is the brand name, untranslated.
	Label string
	// Fields are the inputs the first step asks for.
	Fields []Field
	// OpensAccount says the method may open an account.
	OpensAccount bool
	// BindingProviders are the provider values its link flow stores.
	BindingProviders []string
}

// LoginMethod is one kind of first factor that a module delivers without claiming
// it. It answers with a Step and nothing else: no storage, HTTP or flow record;
// StepUnavailable (not StepRefused) when unreachable; a panic fails it alone with a 503.
type LoginMethod interface {
	// Name is the method's stable name, appearing in routes, flow records and metric
	// attributes; it never changes, and two methods with one name are ErrMethodConflict.
	Name() string
	// Describe reports how the method is drawn in the discovery document; it is a
	// constant that reads no store or configuration and carries no login value.
	Describe() MethodDescriptor
	// Begin starts a flow for a purpose.
	Begin(ctx context.Context, s Start) (Step, error)
	// Answer handles the client's answer to the previous step, or an external party's
	// redirect back: an OAuth code, a SAML assertion.
	Answer(ctx context.Context, f FlowState, in StepInput) (Step, error)
}

// LoginMethodSwitch reports the login methods the run-time switch has turned off;
// the password method is never among them, and a failed read is an error rather than
// the empty list, which would turn nothing off and let the method answer.
type LoginMethodSwitch interface {
	// DisabledMethods returns the names turned off now, password never among them.
	// A failed read is an error, never the empty list.
	DisabledMethods() ([]string, error)
}

// MethodAbandoner is the optional part of a method that keeps state of its own for
// a flow and releases it when the flow is abandoned. Abandon is idempotent, and the
// engine logs a failure and removes the flow record all the same.
type MethodAbandoner interface {
	// Abandon releases whatever the method kept for the flow.
	Abandon(ctx context.Context, f FlowState) error
}
