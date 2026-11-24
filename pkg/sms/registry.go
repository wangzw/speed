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
	// SourceConfig is a template the host wrote into the configuration; it is read
	// once at startup and is read-only for the life of the process.
	SourceConfig Source = "config"
	// SourceRuntime is a template this process created, registered or had the
	// vendor review. The process owns its whole lifecycle.
	SourceRuntime Source = "runtime"
)

// Kind is what a template is for, in the vocabulary the vendors share; each
// carrier maps it to its own spelling, and an empty kind is KindNotification.
type Kind string

const (
	// KindVerification is a message that carries a code the recipient proves.
	KindVerification Kind = "verification"
	// KindNotification is a message about the recipient's own account or order.
	KindNotification Kind = "notification"
	// KindMarketing is a message that promotes something.
	KindMarketing Kind = "marketing"
)

func (k Kind) known() bool {
	switch k {
	case KindVerification, KindNotification, KindMarketing:
		return true
	default:
		return false
	}
}

// ApprovalState is the vendor's review verdict in a vocabulary of this package's
// own, so a caller reads a meaning and never a raw code.
type ApprovalState string

const (
	// ApprovalUnknown is a state this process does not track: the zero value, carried
	// by a configured template. It is not ApprovalNotRequired.
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
	// ApprovalNotRequired is a template the vendor has no per-template review for, so
	// it is sendable. It is not ApprovalUnknown.
	ApprovalNotRequired ApprovalState = "not-required"
)

func sendable(state ApprovalState) bool {
	return state == ApprovalApproved || state == ApprovalNotRequired
}

// Approval is the vendor's verdict on one template, with the vendor's own words
// when it turned the template down.
type Approval struct {
	// State is the review verdict.
	State ApprovalState
	// Reason is the vendor's explanation, when it gave one. It is the vendor's
	// text about the template and never about a message, so it carries no message
	// code and no credential.
	Reason string
}

// Submission is a template offered to the vendor for review. Content is written
// with ${name} placeholders naming the parameters in Params; the carrier rewrites
// them into its vendor syntax.
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

// Record is one template the registry holds, as it is reported to an operator. It
// carries no vendor code.
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
	// Approvals is the vendor's verdict per language, keyed by locale; a configured
	// template carries none.
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
	// UpdatedAt is when the record was last written.
	UpdatedAt time.Time
}

// Registry is the template seam a registry module delivers: it holds the set of
// templates in force, decides which one a message goes out under, and manages the
// ones this process owns.
type Registry interface {
	// Declared reports the templates in force — the configured ones and the
	// enabled, approved runtime ones — in a stable order, without vendor codes.
	Declared() []Template
	// Resolve turns a message into what a carrier needs, refusing one this registry
	// may not send under. Caller defects are ErrRejected; a disabled or unapproved
	// template is ErrNotSendable; Originator is checked by the carrier in Send.
	Resolve(m Message) (Resolved, error)
	// List reports every template the registry holds, configured ones included,
	// in a stable order.
	List(ctx context.Context) ([]Record, error)
	// Register takes up a template whose vendor code the operator already has. Every
	// language starts pending or NotRequired and the template starts enabled; a held
	// language is ErrTemplateExists and an unacceptable shape ErrInvalidConfig.
	Register(ctx context.Context, name string, spec TemplateSpec) (Record, error)
	// Offer hands a template to the vendor for review and takes up the code, or records
	// the body locally when the carrier has no per-template review. A held language is
	// ErrTemplateExists and a new one merges.
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
	// configuration; only an edit to the configuration and a restart can change it.
	ErrImmutable = errors.New("sms: the template comes from the configuration and cannot be changed at runtime")
	// ErrNoTemplate reports a management call naming a template the registry does
	// not hold.
	ErrNoTemplate = errors.New("sms: the registry holds no such template")
	// ErrTemplateExists reports a call naming a template the registry already
	// holds.
	ErrTemplateExists = errors.New("sms: the registry already holds a template under that name")
)
