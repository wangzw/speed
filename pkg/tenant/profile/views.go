package profile

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The views this module answers with. Each is built field by field from a
// root-package value and never handed through as it stands, so that a field
// added to the record later does not reach a client by being there.

// TenantProfileView is one tenant's organization profile as the tenant profile
// routes show it. It carries the read-only name, the kind and the state beside
// the four descriptive fields. The owner is shown by display name and username
// together, because a display name alone is ambiguous when two members share
// one; the owning account travels beside them as the internal key the ownership
// decision and the transfer-target exclusion use, and a client compares it to
// identify the owner. It carries no tenant identifier: the identifier is
// internal, and the request surface addresses the tenant by the name. It is
// deliberately not the platform management surface's view: that one renders the
// disable source, which belongs to the platform surface and not to a tenant
// administrator's own profile page.
//
// The kind is the one value a client branches on: a personal organization has
// no ownership controls, and its name is the owner's username, which says
// nothing about the kind.
//
// The owner is exposed here, behind tenant.profile.read, and nowhere an ordinary
// member can read: the member list carries the membership and its roles, never
// the organization's owner. The read route asks tenant.profile.read in the
// tenant's own policy domain, so a member without it is refused before the view
// is built.
type TenantProfileView struct {
	_ struct{} `description:"One tenant's organization profile: the read-only name, the kind, the owner shown by display name and username, the state and the four descriptive fields, behind tenant.profile.read."`

	Name             string    `json:"name" required:"true" description:"The name the request surface addresses the tenant by."`
	Kind             string    `json:"kind" required:"true" enum:"business,personal" description:"The tenant's kind: business for an ordinary tenant, personal for the single-person space a host derives for an account. A client branches on this value, never on the name."`
	DisplayName      string    `json:"displayName" required:"true" description:"The organization's display name. Empty when its profile names none; a surface falls back to the name then."`
	Description      string    `json:"description" required:"true" description:"The organization's description. Empty when its profile names none."`
	ContactEmail     string    `json:"contactEmail" required:"true" description:"The organization's contact address. Empty when its profile names none. It is the organization's address, not a login identity."`
	Website          string    `json:"website" required:"true" description:"The organization's absolute http or https address. Empty when its profile names none."`
	OwnerDisplayName string    `json:"ownerDisplayName" required:"true" description:"The owner's display name, read from the account store. It is not the account identifier. Empty when the owner cannot be resolved, and never an identifier."`
	OwnerUsername    string    `json:"ownerUsername" required:"true" description:"The owner's account username, read from the account store: unique and user-facing, and not the account identifier. Empty when the owner cannot be resolved or the account has none, and never an identifier."`
	OwnerAccount     string    `json:"ownerAccount" required:"true" description:"The owning account's internal identifier, from the roster row's owner column. It is the key the ownership decision and the transfer-target exclusion use, and a client compares it to identify the owner; it is not displayed. A business organization carries one; a personal organization's owner is the account itself. Empty only for a tenant the roster holds with no owner."`
	Status           string    `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	CreatedAt        time.Time `json:"created_at" required:"true" description:"When the tenant was registered, as an RFC 3339 instant in UTC."`
}

// profileViewOf builds the view field by field from a root-package value.
func profileViewOf(t tenant.Tenant, kind tenant.Kind) TenantProfileView {
	return TenantProfileView{
		Name:         t.Name,
		Kind:         string(kind),
		DisplayName:  t.Profile.DisplayName,
		Description:  t.Profile.Description,
		ContactEmail: t.Profile.ContactEmail,
		Website:      t.Profile.Website,
		OwnerAccount: t.OwnerAccount,
		Status:       t.Status.String(),
		CreatedAt:    t.CreatedAt.UTC(),
	}
}

// kindOf answers one tenant's kind. With no resolver assembled every tenant is
// a business tenant, the same rule the caller tenant read applies.
func (p *product) kindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	if p.kind == nil {
		return tenant.KindBusiness, nil
	}
	return p.kind.KindOf(ctx, id)
}
