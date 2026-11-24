package sms

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

func (p *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventAccountDeleted, ModuleName, p.onAccountDeleted)
	bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
}

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
