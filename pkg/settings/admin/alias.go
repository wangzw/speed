package admin

import (
	"context"
	"log/slog"
)

// tenantIDOf answers the identifier the tenant alias names. The query string
// names a tenant by its alias, the only external name a tenant has, and the
// settings layer keys by the identifier. An assembly with no roster has no
// aliases to resolve, so the name is refused: the internal identifier is never
// a value a request may carry, and reading the query as one would accept it.
func (p *product) tenantIDOf(ctx context.Context, alias string) (string, bool, error) {
	if p.directory == nil {
		return "", false, nil
	}
	id, found, err := p.directory.TenantIDOf(ctx, alias)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant identifier could not be read",
			slog.String("cause", err.Error()))
		return "", false, err
	}
	return id, found, nil
}
