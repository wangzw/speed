package sms

import (
	"context"
	"errors"
	"time"
)

// Source says where a template came from, which is also what decides whether the
// process may change it.
type Source string

const (
	// SourceConfig is a template the host wrote into the configuration. It is
	// read once at startup and is read-only for the life of the process: the
	// operator who wrote it has already had the vendor approve it, so this
	// process neither manages it nor tracks its review state.
	SourceConfig Source = "config"
	// SourceRuntime is a template this process created, registered or had the
	// vendor review. The process owns its whole lifecycle.
	SourceRuntime Source = "runtime"
)

// Kind is what a template is for, in the vocabulary the vendors share. Each
// vendor spells these its own way — a number here, a string there — so the
// mapping belongs to the carrier, which is the only layer that knows its own
// vendor's spelling. A declaration that names none gets KindNotification.
type Kind string

const (
	// KindVerification is a message that carries a code the recipient proves.
	KindVerification Kind = "verification"
	// KindNotification is a message about the recipient's own account or order.
	KindNotification Kind = "notification"
	// KindMarketing is a message that promotes something.
	KindMarketing Kind = "marketing"
)

// known reports whether the kind is one of the declared three.
//
// The set is closed because a carrier maps it to a vendor field that changes
// what may be sent and how it is billed: a kind the declaration does not name
// would fall through to a carrier's default, which for a marketing message is
// the transactional spelling. A declaration is refused instead, at the same
// point every other self-consistency defect is.
func (k Kind) known() bool {
	switch k {
	case KindVerification, KindNotification, KindMarketing:
		return true
	default:
		return false
	}
}

// ApprovalState is the vendor's review verdict, in a vocabulary of this
// package's own.
//
// It is deliberately not the vendor's numbering. The two vendors disagree about
// what the numbers mean — one of them answers 0 for a template that is approved
// and in force, and reserves the sequence's low values for states the other
// vendor does not have at all — so a caller reading a state through this package
// reads a meaning and never a raw code. The carrier maps; this is the result.
type ApprovalState string

const (
	// ApprovalUnknown is a state this process does not track. It is the zero
	// value, and it is what a template declared in the configuration carries: the
	// operator who wrote it attested to it, and its review is not this process's
	// business. It is not ApprovalNotRequired: here a review exists and this
	// process does not follow it, while there the vendor has no such review at
	// all. A message is sent under neither; only ApprovalApproved and
	// ApprovalNotRequired are sendable.
	ApprovalUnknown ApprovalState = ""
	// ApprovalPending is a template the vendor has not finished reviewing, or has
	// approved but not yet brought into force. Neither may a message go out under.
	ApprovalPending ApprovalState = "pending"
	// ApprovalApproved is a template the vendor will accept a send under.
	ApprovalApproved ApprovalState = "approved"
	// ApprovalRejected is a template the vendor turned down.
	ApprovalRejected ApprovalState = "rejected"
	// ApprovalWithdrawn is a template whose review the vendor cancelled, or that
	// the vendor no longer holds.
	ApprovalWithdrawn ApprovalState = "withdrawn"
	// ApprovalNotRequired is a template the vendor has no per-template review for,
	// so there is nothing to wait on and it is sendable. It belongs to a carrier
	// whose CarrierModel.Review.PerTemplate is false, and it is not
	// ApprovalUnknown — that one says a review exists and this process does not
	// track it.
	ApprovalNotRequired ApprovalState = "not-required"
)

// sendable reports whether a message may go out under a template in this state.
// Only an approved review or the absence of any review qualifies; every state
// that says the vendor has not settled the template, or that this process does
// not follow it, does not.
func sendable(state ApprovalState) bool {
	return state == ApprovalApproved || state == ApprovalNotRequired
}

// Approval is the vendor's verdict on one template, with the vendor's own words
// when it turned the template down.
type Approval struct {
	State ApprovalState
	// Reason is the vendor's explanation, when it gave one. It is the vendor's
	// text about the template and never about a message, so it carries no message
	// code and no credential.
	Reason string
}

// Submission is a template offered to the vendor for review.
//
// Content is written with named placeholders in the form ${name}, naming the
// parameters in Params. A vendor that takes its placeholders positionally is the
// carrier's problem, not the caller's: the carrier renders ${name} into whatever
// its vendor's approved syntax is, in the order Params gives, so an operator
// writes one submission and not one per vendor. A carrier with no per-template
// review records the body locally instead of creating anything at the vendor.
type Submission struct {
	// Name is the name the vendor shows the template under.
	Name string
	// Content is the template body, with ${name} placeholders.
	Content string
	// Kind is what the template is for.
	Kind Kind
	// Locale is the language this submission is for. A vendor approves a template
	// per language, so one submission yields one code for one language.
	Locale string
	// Params are the parameter names Content substitutes, in the order the
	// template expects them.
	Params []string
	// Attributes are the vendor registration identifiers the template carries,
	// bounded by the carrier's CarrierModel.AttributeKeys.
	Attributes map[string]string
	// Remark is a note for the vendor's reviewer.
	Remark string
}

// Record is one template the registry holds, as it is reported to an operator.
//
// It carries no vendor code: a code is the registry's own bookkeeping, and an
// operator reading which templates are in force needs to know that one is, not
// what number the vendor filed it under.
type Record struct {
	// Name is the logical template name a caller sends under.
	Name string
	// Carrier is the carrier this record belongs to. A database serves one
	// carrier, and every record in it names that one.
	Carrier string
	// Kind is what the template is for.
	Kind Kind
	// Source says whether the template came from the configuration or was created
	// at runtime, which is also what decides whether it may be changed.
	Source Source
	// Params are the parameter names the template substitutes, in order.
	Params []string
	// Locales are the effective language set: the languages the template has a
	// code for and the languages it has a body for, together.
	Locales []string
	// Bodies are the locally rendered bodies, keyed by locale. A template that
	// sends only by vendor code has none.
	Bodies map[string]string
	// Attributes are the vendor registration identifiers the template carries.
	Attributes map[string]string
	// Approvals is the vendor's verdict per language, keyed by locale: a vendor
	// approves a template per language, so one record carries one verdict per
	// language it holds a code for. A configured template carries none: this
	// process does not track its review. A carrier with no per-template review
	// carries ApprovalNotRequired per language.
	Approvals map[string]Approval
	// Enabled reports whether a message may be sent under it. A configured
	// template is always enabled.
	Enabled bool
	// Version counts the writes this record has taken. A configured template has
	// no version.
	Version int64
	// CreatedAt and UpdatedAt are when the record was first written and last
	// written. Both are zero for a configured template.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Registry is the template seam a registry module delivers: it holds the set of
// templates in force, decides which one a message goes out under, and manages
// the ones this process owns.
//
// It is a separate capability from Sender because the two answer different
// questions. A Sender knows one vendor's wire format; a Registry knows what is
// approved, what is in force and what a message resolves to, and reaches the
// vendor only through the Provider seam. A consumer takes both: Resolve, then
// Send.
type Registry interface {
	// Declared reports the templates in force — the configured ones and the
	// enabled, approved runtime ones — in a stable order, without vendor codes.
	Declared() []Template
	// Resolve turns a message into what a carrier needs, refusing one this
	// registry may not send under. An empty locale, an unknown template, a
	// language the template has neither a code nor a body for, a missing declared
	// parameter and a surplus parameter are ErrRejected. A runtime template that
	// is disabled, or whose vendor review has not come back approved for the
	// message's own language while the vendor reviews per template, is
	// ErrNotSendable: the review is per language, so a template approved in one
	// language and still waiting in another sends in the approved one. Approved
	// and NotRequired are the sendable states. Originator is not checked here: a
	// declaration holds no identities, and the carrier checks the name in Send.
	Resolve(m Message) (Resolved, error)
	// List reports every template the registry holds, configured ones included,
	// in a stable order.
	List(ctx context.Context) ([]Record, error)
	// Register takes up a template whose vendor code the operator already has,
	// with the kind it is for, the parameter names it substitutes, the code and
	// the body for each language it has one for, and the vendor registration
	// identifiers it carries. Every language it names starts pending — or
	// NotRequired when the carrier does not review per template — and the template
	// starts enabled: nothing is known to be wrong with a template the operator
	// vouches for, and the review state of each language is read from the vendor
	// from then on. Registering a language a name already holds is
	// ErrTemplateExists; registering a new language for a name the process already
	// holds merges it in, leaving the languages already there untouched. A shape
	// the mounted carrier's CarrierModel does not accept is ErrInvalidConfig.
	Register(ctx context.Context, name string, spec TemplateSpec) (Record, error)
	// Offer hands a template to the vendor for review, under the vendor's own
	// API, and takes up the code it answers with. A carrier with no per-template
	// review records the body locally and calls no vendor. Every language it names
	// starts pending — or NotRequired when the carrier does not review per
	// template. Offering a language a name already holds is ErrTemplateExists;
	// offering a new language for a name the process already holds merges it in.
	Offer(ctx context.Context, s Submission) (Record, error)
	// SetEnabled turns a message's ability to go out under a runtime template on
	// or off. It is orthogonal to the vendor's review: a template may be enabled
	// before its review comes back, and a send still waits for the review.
	SetEnabled(ctx context.Context, name string, enabled bool) error
	// Remove drops a runtime template from this process. It does not remove it at
	// the vendor.
	Remove(ctx context.Context, name string) error
}

var (
	// ErrImmutable reports a change asked of a template that came from the
	// configuration. Such a template is the operator's and the process's to read
	// and not to change: removing it, disabling it, or re-registering its name is
	// something only an edit to the configuration and a restart can do.
	ErrImmutable = errors.New("sms: the template comes from the configuration and cannot be changed at runtime")
	// ErrNoTemplate reports a management call naming a template the registry does
	// not hold.
	ErrNoTemplate = errors.New("sms: the registry holds no such template")
	// ErrTemplateExists reports a call naming a template the registry already
	// holds.
	ErrTemplateExists = errors.New("sms: the registry already holds a template under that name")
)
