package tenantadmin

import (
	"context"
	"log/slog"
)

// nameOf answers the name a tenant identifier is addressed by. The settings
// store keys a tenant layer by the identifier, and the identifier is internal;
// the audit detail carries the name. With no resolver assembled it answers the
// empty string, so a detail names no tenant rather than exposing the identifier.
func (p *product) nameOf(ctx context.Context, id string) (string, error) {
	if p.names == nil {
		return "", nil
	}
	name, nameErr := p.names.TenantNameOf(ctx, id)
	if nameErr != nil {
		p.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", nameErr.Error()))
		return "", nameErr
	}
	return name, nil
}
