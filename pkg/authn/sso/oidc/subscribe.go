package oidc

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

func (p *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventIDPChanged, ModuleName, p.onIDPChanged)
}

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
