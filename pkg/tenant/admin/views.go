package admin

import (
	"github.com/vislake/speed/pkg/tenant"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there.

// TenantAdminTenant is one tenant as the management routes show it. It carries
// the tenant's identifier, the alias the request surface addresses it by, and
// its state, spelled the way tenant.Status spells it: active, disabled or
// deleted. A disabled tenant also carries who disabled it, spelled the way
// tenant.DisabledBy spells the source: operator or account.
type TenantAdminTenant struct {
	_ struct{} `description:"One tenant as the management routes render it: the identifier, the alias and the state, plus who disabled it while it is disabled."`

	ID     string `json:"id" required:"true" description:"The tenant's identifier, the natural key the roster keys the row by."`
	Alias  string `json:"alias" required:"true" description:"The alias the request surface addresses the tenant by."`
	Status string `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	// DisabledBy is present only while Status is disabled. The field is left
	// out otherwise, so a source never appears on a row whose state it does not
	// describe.
	DisabledBy string `json:"disabledBy,omitempty" enum:"operator,account" description:"Who disabled the tenant: operator through a status change, or account through the account's own state. Present only while the state is disabled."`
}

// tenantView is the name the handlers and their tests use for the view. It is an
// alias, so the fragment publishes the component under the name the hand-written
// fragment used: TenantAdminTenant.
type tenantView = TenantAdminTenant

// tenantViewOf builds the view field by field from a root-package value. The
// disable source is rendered only for a disabled tenant: tenant.Tenant reads the
// source as operator for every other state, and copying that onto an active or
// deleted row would say who stopped a tenant that is not stopped.
func tenantViewOf(t tenant.Tenant) TenantAdminTenant {
	view := TenantAdminTenant{
		ID:     string(t.ID),
		Alias:  t.Alias,
		Status: t.Status.String(),
	}
	if t.Status == tenant.StatusDisabled {
		view.DisabledBy = t.DisabledBy.String()
	}
	return view
}
