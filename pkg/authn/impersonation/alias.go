package impersonation

import (
	"context"
	"log/slog"
)

// aliasOf answers the alias a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the alias.
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

// tenantIDOf answers the identifier the alias names. The start request carries
// the tenant as the name a client reads; the membership store and the record
// key their rows by the identifier. An alias no tenant carries answers false,
// which the start answers as the restricted refusal a target outside the tenant
// gets, and an absent directory answers false too: a name that cannot be
// resolved is never read as an identifier.
func (m *product) tenantIDOf(ctx context.Context, alias string) (string, bool, error) {
	if m.tenantDirectory == nil {
		return "", false, nil
	}
	id, found, err := m.tenantDirectory.TenantIDOf(ctx, alias)
	if err != nil {
		m.logger.Error(ModuleName+": the tenant identifier could not be read",
			slog.String("cause", err.Error()))
		return "", false, err
	}
	return id, found, nil
}
