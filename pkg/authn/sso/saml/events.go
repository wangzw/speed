package saml

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// subscribe registers for authn.idp.changed, so that a change of a provider
// drops its cached service provider. A bus that is absent subscribes to nothing,
// and the cache then lives only by the provider's version and its lifetime.
func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventIDPChanged, p.onIDPChanged)
}

// onIDPChanged drops the cached service provider of the provider the event
// names. A payload it cannot read drops every entry, since the alternative is a
// stale provider being used; the handler is idempotent and the event carries no
// order.
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
