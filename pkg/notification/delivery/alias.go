package delivery

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/notification"
)

// tenantParam is the dispatch parameter that names the tenant a message is
// about. The submitting module knows the tenant's internal identifier; every
// text a recipient can read names the alias instead, so the pipeline resolves
// this one parameter before any channel renders or copies it.
const tenantParam = "tenant"

// aliasOf answers the alias a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string, and a resolver that fails is
// logged and also answers empty: the identifier is internal and must not stand
// in for the alias.
func (p *pipeline) aliasOf(ctx context.Context, id string) (string, error) {
	if p.tenantAliases == nil {
		return "", nil
	}
	alias, err := p.tenantAliases.TenantAliasOf(ctx, id)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant alias could not be read",
			slog.String("cause", err.Error()))
		return "", err
	}
	return alias, nil
}

// outbound returns the dispatch as its channels render and expose it. A
// parameter named tenant carries the tenant's internal identifier; every text a
// recipient can read — the in-app title and body, an email subject and bodies,
// the sms template variables and the in-app payload's parameters — names the
// alias instead. With no resolver, or one that fails, the parameter resolves to
// empty rather than to the identifier.
//
// The returned dispatch is a copy: the parameter map is the one the channels
// hand out, so the submitted value is never mutated.
func (p *pipeline) outbound(ctx context.Context, d notification.Dispatch) notification.Dispatch {
	held, present := d.Params[tenantParam]
	if !present {
		return d
	}
	id, ok := held.(string)
	if !ok {
		return d
	}
	alias, _ := p.aliasOf(ctx, id)
	out := d
	out.Params = make(map[string]any, len(d.Params))
	for name, value := range d.Params {
		out.Params[name] = value
	}
	out.Params[tenantParam] = alias
	return out
}
