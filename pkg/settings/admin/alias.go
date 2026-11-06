package admin

import (
	"context"
	"log/slog"
)

// tenantIDOf answers the identifier the tenant alias names. The query string
// names a tenant by its alias, the only external name a tenant has, and the
// settings layer keys by the identifier. An assembly with no roster has no
// aliases and reads the value as the identifier itself, which is the only name
// such an assembly has.
func (p *product) tenantIDOf(ctx context.Context, alias string) (string, bool, error) {
	if p.directory == nil {
		return alias, true, nil
	}
	id, found, err := p.directory.TenantIDOf(ctx, alias)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant identifier could not be read",
			slog.String("cause", err.Error()))
		return "", false, err
	}
	return id, found, nil
}
