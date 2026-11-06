package tenantadmin

import (
	"context"
	"log/slog"
)

// aliasOf answers the alias a tenant identifier is addressed by. The settings
// store keys a tenant layer by the identifier, and the identifier is internal;
// the audit detail carries the alias. With no resolver assembled it answers the
// empty string, so a detail names no tenant rather than exposing the identifier.
func (p *product) aliasOf(ctx context.Context, id string) (string, error) {
	if p.aliases == nil {
		return "", nil
	}
	alias, aliasErr := p.aliases.TenantAliasOf(ctx, id)
	if aliasErr != nil {
		p.logger.Error(ModuleName+": the tenant alias could not be read",
			slog.String("cause", aliasErr.Error()))
		return "", aliasErr
	}
	return alias, nil
}
