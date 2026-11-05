package authn

import (
	"context"
	"time"
)

// InviteNotifier is the optional channel an invitation's notice goes out over.
// It is the sending half only: the invitation module issues the invitation,
// decides the deadline and hands the token out once, and this seam renders the
// notice and delivers it. It is a host-provided capability like EmailCodeSender:
// the repository's invitemail module adapts it to a mail carrier, while a
// deployment that wants no mail leaves the seam absent and records the delivery
// as not configured.
//
// The notice is the one value of this seam that carries the acceptance token,
// and the token is the whole credential of acceptance. An implementation must
// keep it out of logs, traces, events and error text.
type InviteNotifier interface {
	// Notify delivers a notice about the invitation described by notice, in the
	// language notice.Locale names. It reports the delivery outcome together
	// with the cause of a failure: InviteSent with a nil error when the carrier
	// accepted the message, InviteFailed with the carrier's error when it did
	// not or its outcome is unknown, and InviteNotConfigured with a nil error
	// when the deployment chose to send no mail. A failed delivery does not undo
	// the invitation: the caller logs it and still answers the issue with the
	// one-time link.
	Notify(ctx context.Context, notice InviteNotice) (InviteDelivery, error)
}

// InviteNotice is what one invitation notice is rendered from. It is the whole
// of what the notice says, and it carries the acceptance token because the link
// the recipient follows is built from it.
type InviteNotice struct {
	// Tenant is the string form of the tenant identifier the invitation grants
	// a membership in.
	Tenant string
	// Email is the invited address in the normalized form the invitation stores:
	// trimmed and lower-cased. It is the recipient.
	Email string
	// Token is the acceptance token, handed to the invitee once. It is the
	// credential of acceptance; an implementation must not write it to a log, a
	// trace, an event or an error text.
	Token string
	// ExpiresAt is the invitation's absolute deadline, so the notice can say
	// when the link stops working. The zero time means the caller does not know
	// it.
	ExpiresAt time.Time
	// Locale is the BCP 47 tag the request named, or empty when it named none.
	// An implementation falls back through its catalog; the empty string is not
	// a refusal.
	Locale string
}

// InviteDelivery is the outcome of one invitation notice. Its zero value is
// InviteNotConfigured, which is the honest answer for a deployment that sends no
// mail and for a value that was never set.
type InviteDelivery uint8

// The delivery outcomes of an invitation notice.
const (
	InviteNotConfigured InviteDelivery = iota // no channel was configured; nothing was sent
	InviteSent                                // the carrier accepted the message
	InviteFailed                              // the carrier refused it or left the outcome unknown
)

// Valid reports whether d is one of the delivery outcomes.
func (d InviteDelivery) Valid() bool { return d <= InviteFailed }

// String is the outcome's name as a response and a log line write it: sent,
// failed or not_configured. A value outside the set reads unknown.
func (d InviteDelivery) String() string {
	switch d {
	case InviteSent:
		return "sent"
	case InviteFailed:
		return "failed"
	case InviteNotConfigured:
		return "not_configured"
	}
	return "unknown"
}
