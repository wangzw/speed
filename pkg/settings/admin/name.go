package admin

import (
	"context"
	"log/slog"
)

func (p *product) tenantIDOf(ctx context.Context, name string) (string, bool, error) {
	if p.directory == nil {
		return "", false, nil
	}
	id, found, err := p.directory.TenantIDOf(ctx, name)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant identifier could not be read",
			slog.String("cause", err.Error()))
		return "", false, err
	}
	return id, found, nil
}
