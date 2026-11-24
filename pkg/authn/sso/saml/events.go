package saml

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventIDPChanged, ModuleName, p.onIDPChanged)
}

func (p *product) onIDPChanged(_ context.Context, evt event.Event) error {
	fields, ok := event.PayloadFields(evt.Payload)
	if !ok {
		p.cache.flush()
		return nil
	}
	id, _ := fields["idp_id"].(string)
	if id == "" {
		p.cache.flush()
		return nil
	}
	p.cache.drop(id)
	return nil
}
