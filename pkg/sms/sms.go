// Package sms sends one templated message through a carrier. A template is a
// first-class declaration held by the Registry; a carrier lays out what the
// Registry resolved and declares what it can carry in its CarrierModel.
package sms

import (
	"context"
	"errors"
	"time"
)

// Message is one templated message handed to a carrier. Its parameter values and
// any carrier credential reach no log, no error text and no metric.
type Message struct {
	// To is the destination, in E.164.
	To string
	// Template is the logical template name, chosen by the caller; the registry maps
	// it, with the language, to a vendor code or a body.
	Template string
	// Params carries the substitutions. Its keys are the parameter names the
	// template declared; a declared parameter that is absent and a parameter that
	// was never declared are both caller defects, refused by Resolve.
	Params map[string]string
	// Locale is the language the message is written in. It is opaque here and must be
	// non-empty; an empty locale is a caller defect refused with ErrRejected.
	Locale string
	// Originator names the sending identity, empty for the carrier's default. Resolve
	// does not check it; the carrier refuses an unknown name with ErrRejected.
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

// Sender is the sending seam a carrier module delivers exclusively. It answers its
// name, calls one vendor without retrying, and classifies the answer into the
// sending sentinels. It takes a Resolved because a carrier holds no template set.
type Sender interface {
	// Name reports the carrier this sender is.
	Name() string
	// Send hands one resolved message to the carrier, at most once per DedupeID.
	// An error is one of ErrThrottled, ErrRejected, ErrUnroutable or ErrUncertain,
	// wrapped so the detail is readable and errors.Is still finds the sentinel.
	Send(ctx context.Context, r Resolved) (Receipt, error)
}

// Provider is the template-management seam a carrier delivers beside Sender: what
// the carrier can carry and the operations a template goes through at the vendor.
// Every error is wrapped so errors.Is finds the sentinel.
type Provider interface {
	// Name reports the carrier this provider is, the same name Sender answers.
	Name() string
	// Model reports what this carrier can carry, fixed for the life of the process. It
	// sits on Provider so the management surface can answer which carrier it holds.
	Model() CarrierModel
	// Offer hands a template to the vendor for review and answers its identifier. A
	// carrier with no per-template review records the body locally.
	Offer(ctx context.Context, s Submission) (code string, err error)
	// Approval reads the vendor's review state for one template code. A carrier
	// whose model has no per-template review answers ApprovalNotRequired.
	Approval(ctx context.Context, code string) (Approval, error)
	// Retire removes one template at the vendor, unlike disabling it locally. A carrier
	// with no per-template review has nothing to remove, so its Retire is a no-op.
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

// Identity is one sending identity this deployment has. It declares the identity
// and the countries it can serve, never its registration state.
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

// CarrierModel is a carrier's declaration of what it can carry, fixed for the life
// of the process.
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
	// ErrRejected reports a message a carrier refused, and also the caller defects
	// this package catches before the carrier is reached.
	ErrRejected = errors.New("sms: the carrier rejected the message")
	// ErrUnroutable reports a message this carrier cannot carry to this destination
	// under this deployment. The message is not defective and the outcome is definite.
	ErrUnroutable = errors.New("sms: the carrier cannot carry this message to this destination")
	// ErrUncertain reports a send whose delivery is not known, because the call
	// failed in a way that leaves it open whether the message went out.
	ErrUncertain = errors.New("sms: whether the message was delivered is not known")
	// ErrInvalidConfig reports a configuration a carrier cannot run on.
	ErrInvalidConfig = errors.New("sms: the configuration holds a value it cannot run on")
	// ErrNotSendable reports a template this process holds but must not send under:
	// it is disabled or its vendor review has not come back approved.
	ErrNotSendable = errors.New("sms: the template is not in a state a message may be sent under")
)
