// Package sms sends one templated message through a carrier.
//
// It is the sending capability on its own: a Message names a template, a
// language and the parameters the template substitutes, and a Sender hands that
// message to one vendor. It holds no challenge state, no queue, no budget and
// no retry; a consumer that needs those builds them over this package.
//
// A template is a first-class declaration, because the carriers this package
// serves take only approved templates: a domestic carrier sends under a
// pre-approved signature and template and only the parameters that template was
// approved with, and a positional carrier requires those parameters in the
// approved order.
//
// The template set is the Registry's, not a carrier's: a registry holds the
// templates in force, resolves a Message against them and reaches the vendor
// through the Provider seam. A carrier holds no template set at all — it lays
// out the parameters a Registry already resolved. See registry.go for the
// registry seam and the record an operator manages.
//
// A carrier is a module: an implementation subpackage hands a Spec to NewModule,
// and a host enables at most one of them, for this package's Sender and
// Provider alike.
package sms

import (
	"context"
	"errors"
)

// Message is one templated message handed to a carrier.
//
// The code it carries and any carrier credential reach no log, no error text and
// no metric. This package names the fields; keeping that is each carrier's.
type Message struct {
	// To is the destination, in E.164.
	To string
	// Template is the logical template name, chosen by the caller. A carrier
	// maps it, with the language, to a template number of its own, so the same
	// name means the same message shape to every carrier.
	Template string
	// Params carries the substitutions. Its keys are the parameter names the
	// template declared; a declared parameter that is absent and a parameter that
	// was never declared are both caller defects, refused by Resolve.
	Params map[string]string
	// Locale is the language the message is written in. It is opaque here — this
	// package only indexes the template's language-to-code mapping with it — and
	// it must be non-empty: an empty locale is a caller defect, refused with
	// ErrRejected. A consumer that lets an empty value mean a default normalizes
	// it before it builds the Message.
	Locale string
	// DedupeID is a stable identifier; a carrier sends at most one message per
	// identifier. An empty identifier has nothing to guard by and the message is
	// delivered directly.
	DedupeID string
}

// Receipt is what a carrier answers with once it has taken a message.
type Receipt struct {
	// ProviderID is the carrier's own identifier for the send, when the carrier
	// gave one.
	ProviderID string
}

// Sender is the sending seam a carrier module delivers.
//
// A carrier module delivers it exclusively, so a command names one carrier and
// two at once fail in resolution. A carrier answers its own name, calls one
// vendor and never retries inside the call, and classifies what comes back into
// ErrThrottled, ErrRejected or ErrUncertain: an error that is neither of the
// first two, a timeout and a network error are all ErrUncertain.
//
// It takes a Resolved and not a Message: which template a message goes out under
// is decided by the template registry, against the set that is in force, and a
// carrier holds no template set of its own. The carrier's whole part in the
// decision is laying the already-ordered parameters out the way its vendor wants
// them.
type Sender interface {
	// Name reports the carrier this sender is.
	Name() string
	// Send hands one resolved message to the carrier, at most once per DedupeID.
	// An error is one of ErrThrottled, ErrRejected or ErrUncertain, wrapped so the
	// detail is readable and errors.Is still finds the sentinel.
	Send(ctx context.Context, r Resolved) (Receipt, error)
}

// Provider is the template-management seam a carrier module delivers beside
// Sender: the operations that only the vendor's own API can perform, and the
// only place the vendor's numbering is ever read or written.
//
// It is a separate seam from Sender because it is a separate kind of work: a
// send is one call the caller waits on, while a template outlives the process, is
// reviewed by the vendor on the vendor's own schedule, and is reached by an
// operator rather than by a flow. A carrier module delivers both, exclusively,
// so a command still names exactly one carrier.
//
// A carrier answers its own Name, and every error it returns is wrapped so
// errors.Is finds the sentinel. A vendor error the carrier cannot place is
// ErrUncertain, on the same reasoning as a send: a call that fell over may still
// have landed, and offering a template twice is a template nobody approved.
type Provider interface {
	// Name reports the carrier this provider is, the same name Sender answers.
	Name() string
	// Offer hands a template to the vendor for review and answers the vendor's
	// identifier for it. The vendor reviews asynchronously, so the identifier is
	// all that comes back; the state is read later through Approval.
	Offer(ctx context.Context, s Submission) (code string, err error)
	// Approval reads the vendor's review state for one template code.
	Approval(ctx context.Context, code string) (Approval, error)
	// Retire removes one template at the vendor. It is not the same as disabling
	// a template locally: the vendor stops holding it.
	Retire(ctx context.Context, code string) error
}

var (
	// ErrThrottled reports a carrier that limited the call.
	ErrThrottled = errors.New("sms: the carrier limited the call")
	// ErrRejected reports a message a carrier refused: a template, a language, a
	// parameter or a signature it would not send under. It is also the sentinel
	// for a caller defect this package catches before the carrier is reached —
	// an empty locale, an undeclared template, a missing or surplus parameter —
	// because neither reached the carrier and neither is uncertain.
	ErrRejected = errors.New("sms: the carrier rejected the message")
	// ErrUncertain reports a send whose delivery is not known, because the call
	// failed in a way that leaves it open whether the message went out.
	ErrUncertain = errors.New("sms: whether the message was delivered is not known")
	// ErrInvalidConfig reports a configuration a carrier cannot run on.
	ErrInvalidConfig = errors.New("sms: the configuration holds a value it cannot run on")
	// ErrNotSendable reports a template this process holds but must not send
	// under: one that is disabled, or one whose vendor review has not come back
	// approved. It is told apart from ErrRejected because the remedy differs — a
	// rejected message is the caller's to fix, while this one is the template's
	// state, and nothing the caller writes into a message changes it.
	ErrNotSendable = errors.New("sms: the template is not in a state a message may be sent under")
)
