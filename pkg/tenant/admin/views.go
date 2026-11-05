package admin

import (
	"time"

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
	_ struct{} `description:"One tenant as the management routes render it: the identifier, the alias, the organization profile, the state, the creation time and who disabled it while it is disabled."`

	ID           string `json:"id" required:"true" description:"The tenant's identifier, the natural key the roster keys the row by."`
	Alias        string `json:"alias" required:"true" description:"The alias the request surface addresses the tenant by."`
	DisplayName  string `json:"displayName" required:"true" description:"The organization's display name. Empty when its profile names none; a surface falls back to the alias then."`
	Description  string `json:"description" required:"true" description:"The organization's description. Empty when its profile names none."`
	ContactEmail string `json:"contactEmail" required:"true" description:"The organization's contact address. Empty when its profile names none. It is the organization's address, not a login identity."`
	Website      string `json:"website" required:"true" description:"The organization's absolute http or https address. Empty when its profile names none."`
	Status       string `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	// CreatedAt is when the roster recorded the registration. Every row carries
	// one, so the field is always present and a surface needs no fallback.
	CreatedAt time.Time `json:"createdAt" required:"true" description:"When the tenant was registered, as an RFC 3339 instant in UTC."`
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
// disable source is rendered whenever the view spells the state as disabled,
// which includes a value this build does not know: that value reads as disabled
// everywhere else, so the source it carries belongs with it. Rendering it only
// for the literal StatusDisabled would leave such a row showing a disabled state
// and no reason, which is the contradiction the view exists to avoid.
func tenantViewOf(t tenant.Tenant) TenantAdminTenant {
	view := TenantAdminTenant{
		ID:           string(t.ID),
		Alias:        t.Alias,
		DisplayName:  t.Profile.DisplayName,
		Description:  t.Profile.Description,
		ContactEmail: t.Profile.ContactEmail,
		Website:      t.Profile.Website,
		Status:       t.Status.String(),
		CreatedAt:    t.CreatedAt.UTC(),
	}
	if t.Status.String() == tenant.StatusDisabled.String() {
		view.DisabledBy = t.DisabledBy.String()
	}
	return view
}
