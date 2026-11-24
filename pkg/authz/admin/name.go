package admin

import (
	"context"
	"log/slog"
)

func (p *product) nameOf(ctx context.Context, id string) (string, error) {
	if p.names == nil {
		return "", nil
	}
	name, nameErr := p.names.TenantNameOf(ctx, id)
	if nameErr != nil {
		p.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", nameErr.Error()))
		return "", nameErr
	}
	return name, nil
}
