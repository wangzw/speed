package admin

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
