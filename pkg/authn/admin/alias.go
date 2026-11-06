package admin

import (
	"context"
	"log/slog"
)

// aliasOf answers the alias a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the alias.
//
// A resolver that fails fails the call: a view or an audit row that would name
// the wrong tenant is worse than one that could not be written, and the caller
// answers 503.
func (m *product) aliasOf(ctx context.Context, id string) (string, error) {
	if m.tenantAliases == nil {
		return "", nil
	}
	alias, aliasErr := m.tenantAliases.TenantAliasOf(ctx, id)
	if aliasErr != nil {
		m.logger.Error(ModuleName+": the tenant alias could not be read",
			slog.String("cause", aliasErr.Error()))
		return "", aliasErr
	}
	return alias, nil
}
