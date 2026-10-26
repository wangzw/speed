package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// checkPlatformDomain refuses to start when the roster holds no tenant whose
// identifier is the platform domain.
//
// The platform domain carries the platform operations' roles, grants and
// bindings, and it is a real roster tenant at the same time: the identifier
// carries the same string as a tenant's would, so the platform policy rows are
// that tenant's rows. An assembly with a roster and no such tenant has nowhere
// for a platform decision to land, and a binding of the platform administrator
// role would name an account no membership can place there, so the startup is
// refused before the process serves. The check runs only in an assembly that has
// a roster; a policy module with no tenant module has none to check.
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
