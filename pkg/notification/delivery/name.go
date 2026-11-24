package delivery

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/notification"
)

// tenantParam is the dispatch parameter that names the tenant a message
// is about.
const tenantParam = "tenant"

func (p *pipeline) nameOf(ctx context.Context, id string) (string, error) {
	if p.tenantNames == nil {
		return "", nil
	}
	name, err := p.tenantNames.TenantNameOf(ctx, id)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", err.Error()))
		return "", err
	}
	return name, nil
}

func (p *pipeline) outbound(ctx context.Context, d notification.Dispatch) notification.Dispatch {
	held, present := d.Params[tenantParam]
	if !present {
		return d
	}
	id, ok := held.(string)
	if !ok {
		return d
	}
	tenantName, _ := p.nameOf(ctx, id)
	out := d
	out.Params = make(map[string]any, len(d.Params))
	for name, value := range d.Params {
		out.Params[name] = value
	}
	out.Params[tenantParam] = tenantName
	return out
}
