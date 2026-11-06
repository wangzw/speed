package admin

import (
	"context"
	"log/slog"
)

// aliasOf answers the alias a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the alias.
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
