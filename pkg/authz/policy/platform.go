package policy

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// checkPlatformDomain refuses to start when the roster holds a tenant whose
// identifier is the reserved platform domain.
//
// The platform domain carries the platform operations' roles, grants and
// bindings, and the identifier carries the same string as a tenant's would, so
// a real tenant named system would have this module's account-level decisions
// land on that tenant's policy rows. The direction is the safe one — the rows
// are still that tenant's — but the semantics are not the ones the design
// states, so the conflict is refused before the process serves rather than left
// to be discovered. The check runs only in an assembly that has a roster; a
// policy module with no tenant module has none to check.
func checkPlatformDomain(ctx context.Context, roster tenant.Catalog) error {
	_, found, err := roster.Get(ctx, tenant.ID(authz.PlatformDomain))
	if err != nil {
		return fmt.Errorf("authz/policy: reading the roster for the platform domain: %w", err)
	}
	if found {
		return fmt.Errorf("%w: the roster holds a tenant named %q. Rename that tenant: the "+
			"platform domain is a reserved value and its policy rows are the platform operations'",
			authz.ErrPlatformDomainTaken, authz.PlatformDomain)
	}
	return nil
}
