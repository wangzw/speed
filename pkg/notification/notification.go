// Package notification turns one business event into a message a
// recipient can read, and hands that message to the recipient.
package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/jobs"
)

// Channel is one channel a message may travel.
type Channel string

const (
	// ChannelInApp is the in-app inbox: a row this capability writes in its own
	// database. It is always available.
	ChannelInApp Channel = "in_app"
	// ChannelEmail is email, sent through the mail capability's sender seam.
	ChannelEmail Channel = "email"
	// ChannelSMS is SMS, sent through the sms capability's registry and sender
	// seams.
	ChannelSMS Channel = "sms"
)

// KnownChannel reports whether a value is one of the closed set of channels.
func KnownChannel(c Channel) bool {
	switch c {
	case ChannelInApp, ChannelEmail, ChannelSMS:
		return true
	}
	return false
}

// TypeSpec is one notification type a business module declares to the
// platform.
type TypeSpec struct {
	// Key is the globally unique type name, e.g. billing.invoice_paid.
	Key string
	// Group is free text for filtering an inbox by group. This capability does
	// not interpret its value.
	Group string
	// DefaultChannels are the channels this type goes out under when no
	// preference overrides them. Each is a member of the Channel set.
	DefaultChannels []Channel
	// Unsubscribable reports whether a recipient may turn a channel off. When it
	// is false the type cannot be unsubscribed — a verification code goes to the
	// necessary channels and is not turned off by a preference.
	Unsubscribable bool
	// RecipientVisibleParams, when non-nil, is a whitelist: only the listed
	// parameter names reach the payload, and the rest are refused. When it is
	// nil all parameters are visible.
	RecipientVisibleParams []string
}

// ValidateTypeSpec reports why a type declaration is not self-consistent;
// a nil answer says it is.
func ValidateTypeSpec(spec TypeSpec) error {
	if strings.TrimSpace(spec.Key) == "" {
		return errors.New("the type key is empty")
	}
	if len(spec.DefaultChannels) == 0 {
		return errors.New("DefaultChannels is empty")
	}
	for _, channel := range spec.DefaultChannels {
		if !KnownChannel(channel) {
			return fmt.Errorf("the default channel %q is not one of the known channels", channel)
		}
	}
	channels := make(map[Channel]struct{}, len(spec.DefaultChannels))
	for _, channel := range spec.DefaultChannels {
		if _, twice := channels[channel]; twice {
			return fmt.Errorf("the default channel %q is listed twice", channel)
		}
		channels[channel] = struct{}{}
	}
	params := make(map[string]struct{}, len(spec.RecipientVisibleParams))
	for _, name := range spec.RecipientVisibleParams {
		if _, twice := params[name]; twice {
			return fmt.Errorf("the recipient-visible parameter %q is listed twice", name)
		}
		params[name] = struct{}{}
	}
	return nil
}

// RecipientClass is the kind of a recipient, a closed set.
type RecipientClass string

const (
	// RecipientUser is a system account.
	RecipientUser RecipientClass = "user"

	// RecipientExternal is a recipient outside this system's accounts. No producer
	// exists yet, and a dispatch to one is refused.
	RecipientExternal RecipientClass = "external"
)

// Recipient is the recipient of one message.
type Recipient struct {
	// Class is the kind of the recipient.
	Class RecipientClass
	// UserID is the system account identifier under the user class.
	UserID string
}

// Dispatch is one dispatch: which type to send, to whom, in what language, with
// what parameters.
type Dispatch struct {
	// TypeKey is the Key of a declared type.
	TypeKey string
	// Recipient is the recipient of the dispatch.
	Recipient Recipient
	// Locale is a BCP 47 tag and must be non-empty. It decides the language
	// the message is rendered in and takes part in the dedupe key.
	Locale string
	// Params carries the parameters used for rendering and delivery.
	Params map[string]any
	// OccurrenceID is the identity of one logical event. A resend of the same
	// logical event must carry the same value; it tells the dedupe key "two sends
	// of one thing" from "two things".
	OccurrenceID string
}

// Preference is one recipient's channel preference for one type.
//
// It carries no recipient field: the recipient is given by the interface method
// that reads or writes it.
type Preference struct {
	// TypeKey is the Key of the type the preference is for.
	TypeKey string
	// Channels is the set of channels the recipient selected for the type.
	Channels []Channel
}

// Message is one rendered in-app message; the inbox reads and writes through
// it.
type Message struct {
	// ID is the message identifier.
	ID string
	// TypeKey is the Key of the type the message was dispatched under.
	TypeKey string
	// Group is the type's group.
	Group string
	// Title is the rendered title, not a template.
	Title string
	// Body is the rendered body, not a template, with a time segment written as
	// a marker exactly as Title writes one.
	Body string
	// Params is the parameter values the message carries, for the client to
	// reuse; a time parameter crosses as an RFC 3339 string.
	Params map[string]any
	// Link is an optional link the client may open.
	Link string
	// ReadAt is when the message was first marked read; nil means unread.
	ReadAt *time.Time
	// CreatedAt is when the message was written.
	CreatedAt time.Time
}

// ListQuery is the filter and pagination of an inbox listing.
type ListQuery struct {
	// Group, when non-empty, filters by group; empty means every group.
	Group string
	// Limit is the page size.
	Limit int
	// Offset is the number of items to skip.
	Offset int
}

// MessagePage is one page of an inbox listing.
type MessagePage struct {
	// Items is the page of messages.
	Items []Message
	// Total is the total number of messages the query matches.
	Total int
}

// Inbox is the in-app inbox seam, delivered exclusively by
// notification.inbox.
type Inbox interface {
	// Write writes one message for the recipient and is idempotent on the
	// dedupe key: the same dedupeKey lands one row, and a repeat returns the
	// existing message without writing or publishing anything.
	Write(ctx context.Context, recipient string, m Message, dedupeKey string) (Message, error)
	// List reads one page of the recipient's messages.
	List(ctx context.Context, recipient string, q ListQuery) (MessagePage, error)
	// UnreadCount counts the recipient's unread, unexpired messages.
	UnreadCount(ctx context.Context, recipient string) (int, error)
	// MarkRead marks one message read.
	MarkRead(ctx context.Context, recipient, messageID string) error
	// ReadAll marks every unread message of the recipient read and returns the
	// number marked. Repeating it matches no row and returns zero.
	ReadAll(ctx context.Context, recipient string) (int, error)
}

// Preferences is the per-type channel matrix and the type-taxonomy consumption
// point, delivered exclusively by notification.preferences.
type Preferences interface {
	// Types returns a copy of the taxonomy; changing it does not change the
	// module's own.
	Types() []TypeSpec
	// Type reports whether a type is declared and, if so, its spec.
	Type(key string) (TypeSpec, bool)
	// Effective reports the channels a recipient's dispatches of a type would
	// go out under: the channel set the recipient has stored for the type, or
	// the type's default channels when the recipient has stored none.
	Effective(ctx context.Context, recipient, typeKey string) ([]Channel, error)
	// Set replaces a recipient's channel set for a type and returns the effective
	// set written. The tenant comes from the context. It does not decide whether
	// the caller may change the recipient's preferences; that is the host's.
	Set(ctx context.Context, recipient, typeKey string, channels []Channel) ([]Channel, error)
}

// Dispatcher is the submission seam a business module calls to dispatch
// one notification, delivered exclusively by notification.delivery.
type Dispatcher interface {
	// Dispatch validates, holds a destination to the recipient's own
	// identifiers, charges the delivery budgets and enqueues one dispatch and
	// returns its job identifier. The tenant comes from the context.
	Dispatch(ctx context.Context, d Dispatch) (jobs.JobID, error)
}

var (
	// ErrUnknownType reports a type that is not in the declared taxonomy.
	ErrUnknownType = errors.New("notification: the type is not in the declared taxonomy")
	// ErrUnknownChannel reports a channel that is not in the closed set.
	ErrUnknownChannel = errors.New("notification: the channel is not in the closed set")
	// ErrNotUnsubscribable reports an attempt to unsubscribe a type whose
	// Unsubscribable is false.
	ErrNotUnsubscribable = errors.New("notification: the type is not unsubscribable")
	// ErrParamsNotAllowed reports a payload carrying a parameter outside the
	// type's whitelist.
	ErrParamsNotAllowed = errors.New("notification: the payload carries a parameter outside the type's whitelist")
	// ErrMessageNotFound reports an inbox call naming a message this recipient
	// cannot see — unknown, another's, or another tenant's, all told apart from
	// nothing.
	ErrMessageNotFound = errors.New("notification: the message is not one this recipient can see")
	// ErrInvalidConfig reports a type declaration that is not self-consistent —
	// the refusals ValidateTypeSpec names — or one key that two modules declare.
	// It ends the startup.
	ErrInvalidConfig = errors.New("notification: a type declaration is not self-consistent, or two modules declare one key")
	// ErrInvalidRequest reports a request shape that cannot be read: a missing
	// required item, a pagination value out of range, an unparsable body, or a
	// parameter value that cannot be stringified.
	ErrInvalidRequest = errors.New("notification: the request shape is unreadable")
)
