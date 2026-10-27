package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// publishTimeout bounds how long an event published after a commit may take.
const publishTimeout = 5 * time.Second

// batch holds the events of one operation that go out once its transaction has
// committed.
type batch struct {
	after []event.Event
}

// emit hands an event over. One that goes through the outbox is staged in the
// transaction when the assembly has an outbox, so that it is published once the
// transaction commits and never if it rolls back, and a failure to stage fails
// the operation. Every other event, and one that goes through the outbox in an
// assembly without it, is kept in the batch to be published after the commit,
// and a failure to publish it never fails the operation.
func (p *product) emit(tx *gorm.DB, b *batch, evt event.Event, viaOutbox bool) error {
	if viaOutbox && p.outbox != nil {
		if err := p.outbox.Stage(tx, evt); err != nil {
			return fmt.Errorf("%w: staging an event: %w", authn.ErrUnavailable, err)
		}
		return nil
	}
	b.after = append(b.after, evt)
	return nil
}

// publish sends the events of a batch to the bus once the operation's
// transaction has committed. The write stands whatever becomes of the caller's
// context and of the bus, so the publication does not take its cancellation, and
// an event the bus refuses is a Warn and nothing more.
func (p *product) publish(ctx context.Context, b *batch) {
	if p.bus == nil || len(b.after) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	for _, evt := range b.after {
		if err := p.bus.Publish(ctx, evt); err != nil {
			p.logger.Warn(ModuleName+": an event could not be published; it is a hint and the write stands",
				slog.String("event", evt.Type), slog.String("cause", describe(err)))
		}
	}
}

// describe is the text of an error for a log line: the class of the failure and
// the dependency's own words, which name no value of a caller.
func describe(err error) string {
	if errors.Is(err, context.Canceled) {
		return "the call was cancelled"
	}
	return err.Error()
}

// The closed sets of the payload fields this module fills.
const (
	changeAdded            = "added"
	changeRemoved          = "removed"
	changeSuspended        = "suspended"
	changeResumed          = "resumed"
	changeSSOExemptSet     = "sso-exempt-set"
	changeSSOExemptCleared = "sso-exempt-cleared"

	byAdmin  = "admin"
	bySelf   = "self"
	byImport = "import"
	byReset  = "reset"
	bySignup = "signup"

	channelEmail = "email"
	channelSMS   = "sms"
)

func accountCreated(id authn.AccountID, kind authn.AccountKind, via string) event.Event {
	return event.Event{Type: authn.EventAccountCreated, Payload: authn.AccountCreated{
		AccountID: id.String(), Kind: string(kind), Via: via,
	}}
}

func statusChanged(id authn.AccountID, from, to authn.AccountStatus, reason string) event.Event {
	return event.Event{Type: authn.EventAccountStatusChanged, Payload: authn.AccountStatusChanged{
		AccountID: id.String(), From: from.String(), To: to.String(), ReasonClass: reason,
	}}
}

func accountDeleted(id authn.AccountID) event.Event {
	return event.Event{Type: authn.EventAccountDeleted, Payload: authn.AccountDeleted{AccountID: id.String()}}
}

func identifierChanged(id authn.AccountID, kind authn.IdentifierKind, change string) event.Event {
	return event.Event{Type: authn.EventIdentifierChanged, Payload: authn.IdentifierChanged{
		AccountID: id.String(), Kind: string(kind), Change: change,
	}}
}

// membershipChanged is the event of one change to a membership. The tenant is in
// the envelope and never in the payload.
func membershipChanged(id authn.AccountID, tenant, change, source string) event.Event {
	return event.Event{Type: authn.EventMembershipChanged, TenantID: tenant, Payload: authn.MembershipChanged{
		AccountID: id.String(), Change: change, Source: source,
	}}
}

func passwordChanged(id authn.AccountID, by, channel string) event.Event {
	return event.Event{Type: authn.EventPasswordChanged, Payload: authn.PasswordChanged{AccountID: id.String(), By: by, Channel: channel}}
}

func throttleEngaged(scope authn.ThrottleScope, id authn.AccountID) event.Event {
	payload := authn.ThrottleEngaged{Scope: string(scope)}
	if scope == authn.ScopeAccount || scope == authn.ScopeFactor {
		payload.AccountID = id.String()
	}
	return event.Event{Type: authn.EventThrottleEngaged, Payload: payload}
}

// slogCause is the attribute that carries the text of an error a line is about.
func slogCause(err error) slog.Attr { return slog.String("cause", describe(err)) }
