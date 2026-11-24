package admission

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/settings"
)

// RequiresMFA implements authn.TenantMFARequirement: it reports whether any tenant
// the account is an active member of requires multi-factor, by the platform row OR
// the tenant's row. With no settings store it reports false; a failed read is
// returned as an error and never read as absent.
func (l *layer) RequiresMFA(ctx context.Context, id authn.AccountID) (bool, error) {
	if l.store == nil {
		return false, nil
	}
	platform, err := settings.Get[bool](l.store, settings.Global(), requireMFAKey)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", requireMFAKey, err)
	}
	memberships, err := l.memberships.Tenants(ctx, id)
	if err != nil {
		return false, fmt.Errorf("listing the memberships of an account: %w", err)
	}
	for _, m := range memberships {
		if !m.Active {
			continue
		}
		if platform {
			return true, nil
		}
		if !nameableLayer(m.Tenant) {
			return false, fmt.Errorf("reading %s: %w", requireMFAKey, errSettingLayer)
		}
		value, err := settings.Get[bool](l.store, settings.ForTenant(m.Tenant), requireMFAKey)
		if err != nil {
			return false, fmt.Errorf("reading %s: %w", requireMFAKey, err)
		}
		if value {
			return true, nil
		}
	}
	return false, nil
}
