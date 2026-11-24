package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

func checkPlatformDomain(ctx context.Context, roster tenant.Catalog) error {
	_, found, err := roster.Get(ctx, tenant.ID(authz.PlatformDomain))
	if err != nil {
		return fmt.Errorf("authz/policy: reading the roster for the platform domain: %w", err)
	}
	if !found {
		return fmt.Errorf("%w: the roster holds no tenant named %q. Register it: the platform "+
			"domain's policy rows are that tenant's, and a platform decision has nowhere to land without it",
			authz.ErrPlatformDomainMissing, authz.PlatformDomain)
	}
	return nil
}
