package profile

import (
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The views this module answers with. Each is built field by field from a
// root-package value and never handed through as it stands, so that a field
// added to the record later does not reach a client by being there.

// TenantProfileView is one tenant's organization profile as the tenant profile
// routes show it. It carries the read-only alias, the owning account and the
// state beside the four descriptive fields. It carries no identifier: the
// identifier is internal, and the request surface addresses the tenant by the
// alias. It is deliberately not the platform management surface's view: that
// one renders the disable source, which belongs to the platform surface and not
// to a tenant administrator's own profile page.
//
// The owner is exposed here, behind tenant.profile.read, and nowhere an ordinary
// member can read: the member list carries the membership and its roles, never
// the organization's owner. The read route asks tenant.profile.read in the
// tenant's own policy domain, so a member without it is refused before the view
// is built.
type TenantProfileView struct {
	_ struct{} `description:"One tenant's organization profile: the read-only alias, the owning account, the state and the four descriptive fields, behind tenant.profile.read."`

	Alias        string    `json:"alias" required:"true" description:"The alias the request surface addresses the tenant by."`
	DisplayName  string    `json:"displayName" required:"true" description:"The organization's display name. Empty when its profile names none; a surface falls back to the alias then."`
	Description  string    `json:"description" required:"true" description:"The organization's description. Empty when its profile names none."`
	ContactEmail string    `json:"contactEmail" required:"true" description:"The organization's contact address. Empty when its profile names none. It is the organization's address, not a login identity."`
	Website      string    `json:"website" required:"true" description:"The organization's absolute http or https address. Empty when its profile names none."`
	OwnerAccount string    `json:"ownerAccount" required:"true" description:"The account that owns the organization, from the roster row's owner column. A business organization carries one; a personal organization's owner is the account itself. Empty only for a tenant the roster holds with no owner."`
	Status       string    `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	CreatedAt    time.Time `json:"createdAt" required:"true" description:"When the tenant was registered, as an RFC 3339 instant in UTC."`
}

// profileViewOf builds the view field by field from a root-package value.
func profileViewOf(t tenant.Tenant) TenantProfileView {
	return TenantProfileView{
		Alias:        t.Alias,
		DisplayName:  t.Profile.DisplayName,
		Description:  t.Profile.Description,
		ContactEmail: t.Profile.ContactEmail,
		Website:      t.Profile.Website,
		OwnerAccount: t.OwnerAccount,
		Status:       t.Status.String(),
		CreatedAt:    t.CreatedAt.UTC(),
	}
}
