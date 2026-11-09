package impersonation

import (
	"context"
	"log/slog"
)

// nameOf answers the name a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the name.
func (m *product) nameOf(ctx context.Context, id string) (string, error) {
	if m.tenantNames == nil {
		return "", nil
	}
	name, nameErr := m.tenantNames.TenantNameOf(ctx, id)
	if nameErr != nil {
		m.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", nameErr.Error()))
		return "", nameErr
	}
	return name, nil
}

// tenantIDOf answers the identifier the name names. The start request carries
// the tenant as the name a client reads; the membership store and the record
// key their rows by the identifier. A name no tenant carries answers false,
// which the start answers as the restricted refusal a target outside the tenant
// gets, and an absent directory answers false too: a name that cannot be
// resolved is never read as an identifier.
func (m *product) tenantIDOf(ctx context.Context, name string) (string, bool, error) {
	if m.tenantDirectory == nil {
		return "", false, nil
	}
	id, found, err := m.tenantDirectory.TenantIDOf(ctx, name)
	if err != nil {
		m.logger.Error(ModuleName+": the tenant identifier could not be read",
			slog.String("cause", err.Error()))
		return "", false, err
	}
	return id, found, nil
}
