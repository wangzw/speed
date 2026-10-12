package sms

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// subscribe registers the two compensations: the account deletion, which repeats
// the cleanup the synchronous hook does, and the settings change, which makes a
// relaxing one visible after the fact. It runs in Init, where the bus accepts
// subscriptions.
func (p *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventAccountDeleted, p.onAccountDeleted)
	bus.Subscribe(settings.ChangedEventType, p.onSettingsChanged)
}

// onAccountDeleted is the handler of the compensation. It cleans the account's
// challenges in the event and marks the event done only once the cleaning
// succeeded, so a delivery that failed is tried again by the next one. An event
// that cannot be read is dropped, which is what a subscriber does with what it
// cannot read, and a handler that returns an error leaves the redelivery to the
// bus.
func (p *product) onAccountDeleted(ctx context.Context, evt event.Event) error {
	if evt.ID != "" && p.seen.has(evt.ID) {
		return nil
	}
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		p.logger.Warn(ModuleName+": an account-deleted event could not be read", slog.String("cause", err.Error()))
		return nil
	}
	account, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		p.logger.Warn(ModuleName + ": an account-deleted event names an identifier that is not one")
		return nil
	}
	if err := p.AccountDeleted(ctx, account); err != nil {
		return fmt.Errorf("cleaning the challenges of a deleted account: %w", err)
	}
	if evt.ID != "" {
		p.seen.mark(evt.ID)
	}
	return nil
}
