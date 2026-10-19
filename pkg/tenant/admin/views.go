package admin

import (
	"github.com/vislake/speed/pkg/tenant"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// TenantAdminTenant is one tenant as the management routes show it. It carries
// the tenant's identifier, the alias the request surface addresses it by, and
// its state, spelled the way tenant.Status spells it: active or disabled.
type TenantAdminTenant struct {
	_ struct{} `description:"One tenant as the management routes render it: the identifier, the alias and the state."`

	ID     string `json:"id" required:"true" description:"The tenant's identifier, the natural key the roster keys the row by."`
	Alias  string `json:"alias" required:"true" description:"The alias the request surface addresses the tenant by."`
	Status string `json:"status" required:"true" enum:"active,disabled" description:"The tenant's state: active is served, disabled is not."`
}

// tenantView is the name the handlers and their tests use for the view. It is an
// alias, so the fragment publishes the component under the name the hand-written
// fragment used: TenantAdminTenant.
type tenantView = TenantAdminTenant

// tenantViewOf builds the view field by field from a root-package value.
func tenantViewOf(t tenant.Tenant) TenantAdminTenant {
	return TenantAdminTenant{
		ID:     string(t.ID),
		Alias:  t.Alias,
		Status: t.Status.String(),
	}
}
