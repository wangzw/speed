package admin

import (
	"context"
	"log/slog"
)

// nameOf answers the name a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the name.
//
// A resolver that fails fails the call: a view or an audit row that would name
// the wrong tenant is worse than one that could not be written, and the caller
// answers 503.
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
