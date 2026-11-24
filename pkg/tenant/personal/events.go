package personal

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventAccountStatusChanged, ModuleName, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, ModuleName, p.once(p.onAccountDeleted))
}

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

// onAccountStatusChanged reruns the hook matching the account's current state;
// the payload's target is not trusted because delivery guarantees neither order nor count.
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
