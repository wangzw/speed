// Package sms sends one templated message through a carrier.
//
// It is the sending capability on its own: a Message names a template, a
// language, the parameters the template substitutes, an optional originator and
// an optional lifetime, and a Sender hands that message to one vendor. It holds
// no challenge state, no queue, no budget and no retry; a consumer that needs
// those builds them over this package.
//
// A template is a first-class declaration, because the carriers this package
// serves take only approved templates: a domestic carrier sends under a
// pre-approved signature and template and only the parameters that template was
// approved with; a positional carrier requires those parameters in the approved
// order; and a carrier that sends by body sends the text the declaration
// rendered. A template carries its kind and either a vendor code per language, a
// locally rendered body per language, or both — which of the two a carrier can
// use is part of its CarrierModel.
//
// The template set is the Registry's, not a carrier's: a registry holds the
// templates in force, resolves a Message against them and reaches the vendor
// through the Provider seam. A carrier holds no template set at all — it lays out
// what a Registry already resolved, and its CarrierModel declares the shape of
// template it can carry. See registry.go for the registry seam and the record an
// operator manages.
//
// A carrier is a module: an implementation subpackage hands a Spec to NewModule,
// and a host enables at most one of them, for this package's Sender and Provider
// alike. There is one carrier per process and no routing: a Message carries no
// routing key, and neither Sender nor Provider is delivered twice.
package sms

import (
	"context"
	"errors"
	"time"
)

// Message is one templated message handed to a carrier.
//
// The parameter values it carries — a verification code among them — and any
// carrier credential reach no log, no error text and no metric. This package
// names the fields; keeping that is each carrier's.
type Message struct {
	// To is the destination, in E.164.
	To string
	// Template is the logical template name, chosen by the caller. The registry
	// maps it, with the language, to the vendor's code or the body the declaration
	// holds for it, so the same name means the same message shape to every
	// carrier.
	Template string
	// Params carries the substitutions. Its keys are the parameter names the
	// template declared; a declared parameter that is absent and a parameter that
	// was never declared are both caller defects, refused by Resolve.
	Params map[string]string
	// Locale is the language the message is written in. It is opaque here — this
	// package only indexes the template's language maps with it — and it must be
	// non-empty: an empty locale is a caller defect, refused with ErrRejected. A
	// consumer that lets an empty value mean a default normalizes it before it
	// builds the Message.
	Locale string
	// Originator names the sending identity to use, from the carrier's
	// CarrierModel.Identities. It is optional, and empty means the carrier's
	// default identity. Resolve does not check it, because a Declaration holds no
	// identities; the carrier checks it in Send and refuses an unknown name with
	// ErrRejected, an identity that cannot serve the destination with
	// ErrUnroutable.
	Originator string
	// ValidFor is how long the message stays worth delivering. Zero means the
	// vendor's own default. A carrier whose vendor takes no lifetime ignores it.
	ValidFor time.Duration
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
// ErrThrottled, ErrRejected, ErrUnroutable or ErrUncertain: a failure that fits
// none of the first three — a timeout and a network error included — is
// ErrUncertain.
//
// It takes a Resolved and not a Message: which template a message goes out under
// is decided by the template registry, against the set that is in force, and a
// carrier holds no template set of its own. The carrier's whole part in the
// decision is checking the originator and laying what it was handed out the way
// its vendor wants it: the vendor code or the rendered body, the parameters and
// the registration attributes.
type Sender interface {
	// Name reports the carrier this sender is.
	Name() string
	// Send hands one resolved message to the carrier, at most once per DedupeID.
	// An error is one of ErrThrottled, ErrRejected, ErrUnroutable or ErrUncertain,
	// wrapped so the detail is readable and errors.Is still finds the sentinel.
	Send(ctx context.Context, r Resolved) (Receipt, error)
}

// Provider is the template-management seam a carrier module delivers beside
// Sender: what the carrier can carry, and the operations a template's life at the
// vendor goes through — the only place this package creates, reads, reviews or
// removes vendor-side content.
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
	// Model reports what this carrier can carry: its content and review support,
	// its identities and their countries, the attribute keys it accepts, its body
	// bound and whether its receipts are asynchronous. The model is fixed for the
	// life of the process.
	//
	// It sits on Provider rather than on Product because the management surface
	// holds a Provider and not a Product: without it here, a caller holding the
	// management seam could not answer which kind of carrier it is managing.
	// Product is Sender plus Provider, so it carries Model transitively.
	Model() CarrierModel
	// Offer hands a template to the vendor for review and answers the vendor's
	// identifier for it. The vendor reviews asynchronously, so the identifier is
	// all that comes back; the state is read later through Approval. A carrier
	// whose model has no per-template review records the body locally, calls no
	// vendor, and answers the code it filed the template under.
	Offer(ctx context.Context, s Submission) (code string, err error)
	// Approval reads the vendor's review state for one template code. A carrier
	// whose model has no per-template review answers ApprovalNotRequired.
	Approval(ctx context.Context, code string) (Approval, error)
	// Retire removes one template at the vendor. It is not the same as disabling
	// a template locally: the vendor stops holding it. A carrier with no
	// per-template review holds nothing at the vendor to remove, so its Retire is a
	// no-op.
	Retire(ctx context.Context, code string) error
}

// ContentSupport says which template shape a carrier sends under.
type ContentSupport struct {
	// TemplateCode reports that the carrier sends by the vendor's template code.
	TemplateCode bool
	// TextBody reports that the carrier sends the body the declaration rendered.
	TextBody bool
}

// ReviewSupport says how a vendor reviews a template and whether a send may pick
// its identity.
type ReviewSupport struct {
	// PerTemplate reports that the vendor reviews each template, so a template is
	// only sendable once the review has come back approved. False means the vendor
	// has no such review and Approval answers ApprovalNotRequired.
	PerTemplate bool
	// Originator reports that a send may name the identity it goes out from. False
	// means the carrier has one fixed sending identity and Message.Originator has
	// no effect.
	Originator bool
}

// IdentityKind is what an identity is, in a vocabulary the carriers share. It is
// deliberately not a vendor's own naming: the carrier maps the vendor's spelling
// to one of these.
type IdentityKind string

const (
	// IdentitySignName is an approved signature a message is sent under.
	IdentitySignName IdentityKind = "sign-name"
	// IdentitySenderID is a sender identifier a message is sent under.
	IdentitySenderID IdentityKind = "sender-id"
	// IdentityLongCode is a ten-digit phone number used as the sender.
	IdentityLongCode IdentityKind = "long-code"
	// IdentityShortCode is a short number used as the sender.
	IdentityShortCode IdentityKind = "short-code"
	// IdentityTollFree is a toll-free number used as the sender.
	IdentityTollFree IdentityKind = "toll-free"
	// IdentityPool is a pool of numbers a message is sent from.
	IdentityPool IdentityKind = "pool"
	// IdentityMessagingService is the vendor's messaging service that owns a pool
	// of senders.
	IdentityMessagingService IdentityKind = "messaging-service"
)

// Identity is one sending identity this deployment has.
//
// It declares the identity and the countries it can serve; it says nothing about
// whether that identity is registered in a country. Registration — a US 10DLC
// brand or campaign, an India DLT entity, toll-free verification, a short code,
// a per-country sender ID — stays with the vendor's portal and the contract, and
// this package never manages, tracks or claims it.
type Identity struct {
	// Name is what Message.Originator names it by.
	Name string
	// Kind is what sort of identity it is.
	Kind IdentityKind
	// Value is the vendor's own value for it: a signature, a number, a service
	// identifier.
	Value string
	// Countries are the countries it can serve, in the same form as
	// CarrierModel.Countries. Empty means it serves every country the carrier
	// reaches.
	Countries []string
	// Attributes carries vendor-specific values the identity needs, such as a
	// sender address that lives beside a messaging service.
	Attributes map[string]string
}

// CarrierModel is a carrier's declaration of what it can carry, fixed for the
// life of the process.
//
// The template registry and the management surface read it to decide whether a
// template fits this carrier and how a send is shaped; neither reaches the vendor
// to learn it. It is the whole of what a carrier says about itself beyond its
// name.
type CarrierModel struct {
	// Content says whether it sends by vendor code, by rendered body, or both.
	Content ContentSupport
	// Review says whether the vendor reviews each template and whether a send may
	// pick its identity.
	Review ReviewSupport
	// Identities are the sending identities this deployment has. Empty for a
	// carrier that sends from one fixed account.
	Identities []Identity
	// Countries are the countries this carrier can reach. Empty means it declares
	// no restriction of its own.
	Countries []string
	// AttributeKeys are the template attribute keys this carrier accepts: the
	// vendor registration identifiers a template may carry, such as a DLT template
	// identifier. An attribute under any other key is ErrInvalidConfig.
	AttributeKeys []string
	// MaxBodyRunes bounds a rendered body, in runes. Zero means this package sets
	// no bound of its own.
	MaxBodyRunes int
	// AsyncReceipt reports that the vendor's final delivery state arrives
	// asynchronously: a synchronous answer carries only the vendor's message
	// identifier, and acceptance is not delivery.
	AsyncReceipt bool
}

var (
	// ErrThrottled reports a carrier that limited the call.
	ErrThrottled = errors.New("sms: the carrier limited the call")
	// ErrRejected reports a message a carrier refused: a template, a language, a
	// parameter, an originator or a signature it would not send under. It is also
	// the sentinel for a caller defect this package catches before the carrier is
	// reached — an empty locale, an undeclared template, a language the template
	// has neither a code nor a body for, a missing or surplus parameter, an
	// originator this deployment does not have — because neither reached the
	// carrier and neither is uncertain.
	ErrRejected = errors.New("sms: the carrier rejected the message")
	// ErrUnroutable reports a message this carrier cannot carry to this
	// destination under this deployment: a permission it lacks, a resource that
	// does not exist, a quota that is exhausted, or an identity that cannot serve
	// the destination's country. It is distinct from ErrRejected because the
	// message itself is not defective, and from ErrUncertain because the outcome
	// is definite — only another deployment would change it.
	ErrUnroutable = errors.New("sms: the carrier cannot carry this message to this destination")
	// ErrUncertain reports a send whose delivery is not known, because the call
	// failed in a way that leaves it open whether the message went out.
	ErrUncertain = errors.New("sms: whether the message was delivered is not known")
	// ErrInvalidConfig reports a configuration a carrier cannot run on.
	ErrInvalidConfig = errors.New("sms: the configuration holds a value it cannot run on")
	// ErrNotSendable reports a template this process holds but must not send
	// under: one that is disabled, or one whose vendor review has not come back
	// approved though the vendor reviews it. It is told apart from ErrRejected
	// because the remedy differs — a rejected message is the caller's to fix, while
	// this one is the template's state, and nothing the caller writes into a
	// message changes it.
	ErrNotSendable = errors.New("sms: the template is not in a state a message may be sent under")
)
