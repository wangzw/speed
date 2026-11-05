package personal

// The event compensation for the account lifecycle hooks. The account module
// calls the hooks after its own transaction commits, and stages the account
// events in the same transaction through its outbox. A process that stops
// between the commit and the hooks would otherwise leave the personal tenant as
// it was, so the module also subscribes the account events and runs the same
// idempotent hooks again when a delivery arrives. The event is a hint and never
// the authority: the handler of a status change reads the account's current
// state first, so a late or reordered event cannot apply a state the account has
// already left.

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// subscribe registers the compensation for the two account events. It runs in
// Init, where the bus accepts subscriptions.
func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventAccountStatusChanged, ModuleName, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, ModuleName, p.once(p.onAccountDeleted))
}

// once wraps a handler so that one event is acted on once.
func (p *product) once(h event.Handler) event.Handler {
	return func(ctx context.Context, evt event.Event) error {
		if evt.ID != "" && p.seen != nil && p.seen.has(evt.ID) {
			return nil
		}
		if err := h(ctx, evt); err != nil {
			return err
		}
		if evt.ID != "" && p.seen != nil {
			p.seen.add(evt.ID)
		}
		return nil
	}
}

// onAccountStatusChanged reruns the status hook that matches the account's
// current state. The payload's target is not trusted on its own: delivery
// guarantees neither order nor count.
func (p *product) onAccountStatusChanged(ctx context.Context, evt event.Event) error {
	var payload authn.AccountStatusChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", ModuleName, evt.Type, err)
	}
	status, err := p.accountStatus(ctx, id, payload)
	if err != nil {
		return err
	}
	switch status {
	case authn.StatusActive:
		return p.AccountEnabled(ctx, id)
	case authn.StatusDeleted:
		return p.AccountDeleted(ctx, id)
	default:
		return p.AccountDisabled(ctx, id)
	}
}

// onAccountDeleted reruns the deletion hook.
func (p *product) onAccountDeleted(ctx context.Context, evt event.Event) error {
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", ModuleName, evt.Type, err)
	}
	return p.AccountDeleted(ctx, id)
}

// accountStatus answers the account's current state for a status-changed event.
func (p *product) accountStatus(ctx context.Context, id authn.AccountID,
	payload authn.AccountStatusChanged,
) (authn.AccountStatus, error) {
	if p.accounts != nil {
		account, err := p.accounts.Get(ctx, id)
		switch {
		case errors.Is(err, authn.ErrAccountNotFound):
			return authn.StatusDeleted, nil
		case err != nil:
			return authn.StatusDisabled, fmt.Errorf("%s: reading the account %s names: %w",
				ModuleName, id, err)
		}
		return account.Status, nil
	}
	switch payload.To {
	case authn.StatusActive.String():
		return authn.StatusActive, nil
	case authn.StatusDeleted.String():
		return authn.StatusDeleted, nil
	default:
		return authn.StatusDisabled, nil
	}
}
