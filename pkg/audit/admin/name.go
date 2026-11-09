package admin

import (
	"context"
	"log/slog"

	"github.com/vislake/speed/pkg/tenant"
)

// nameOf answers the name a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the name.
func (p *product) nameOf(ctx context.Context, id string) (string, error) {
	if p.names == nil {
		return "", nil
	}
	name, nameErr := p.names.NameOf(ctx, tenant.ID(id))
	if nameErr != nil {
		p.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", nameErr.Error()))
		return "", nameErr
	}
	return name, nil
}
