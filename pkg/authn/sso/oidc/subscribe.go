package oidc

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// subscribe registers the handler that drops a cached provider when the SSO
// module publishes that an identity provider changed. It runs in Init, where
// the bus accepts subscriptions.
func (p *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventIDPChanged, ModuleName, p.onIDPChanged)
}

// onIDPChanged drops the cached provider and OAuth client of the identity
// provider the event names. The row is the authority, so the change is read as
// a hint and nothing more: an event that cannot be read is dropped, which is
// what a subscriber does with what it cannot read. Dropping is idempotent, so
// the bus's at-least-once delivery needs no de-duplication here.
func (p *product) onIDPChanged(_ context.Context, evt event.Event) error {
	var payload authn.IDPChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		p.logger.Warn(ModuleName+": an idp-changed event could not be read", slog.String("cause", err.Error()))
		return nil
	}
	if payload.IDPID == "" {
		return nil
	}
	p.cache.drop(payload.IDPID)
	return nil
}
