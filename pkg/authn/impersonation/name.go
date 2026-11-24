package impersonation

import (
	"context"
	"log/slog"
)

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

// tenantIDOf maps a tenant name to its identifier; a name no tenant carries is
// never read as an identifier.
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
