package profile

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// TenantProfileView is one tenant's organization profile as the profile routes
// show it. It carries no tenant identifier and no disable source.
type TenantProfileView struct {
	_ struct{} `description:"One tenant's organization profile: the read-only name, the kind, the owner shown by display name and username, the state and the four descriptive fields, behind tenant.profile.read."`

	Name             string    `json:"name" required:"true" description:"The name the request surface addresses the tenant by."`
	Kind             string    `json:"kind" required:"true" enum:"business,personal" description:"The tenant's kind: business for an ordinary tenant, personal for the single-person space a host derives for an account. A client branches on this value, never on the name."`
	DisplayName      string    `json:"display_name" required:"true" description:"The organization's display name. Empty when its profile names none; a surface falls back to the name then."`
	Description      string    `json:"description" required:"true" description:"The organization's description. Empty when its profile names none."`
	ContactEmail     string    `json:"contact_email" required:"true" description:"The organization's contact address. Empty when its profile names none. It is the organization's address, not a login identity."`
	Website          string    `json:"website" required:"true" description:"The organization's absolute http or https address. Empty when its profile names none."`
	OwnerDisplayName string    `json:"owner_display_name" required:"true" description:"The owner's display name, read from the account store. It is not the account identifier. Empty when the owner cannot be resolved, and never an identifier."`
	OwnerUsername    string    `json:"owner_username" required:"true" description:"The owner's account username, read from the account store: unique and user-facing, and not the account identifier. Empty when the owner cannot be resolved or the account has none, and never an identifier."`
	OwnerAccount     string    `json:"owner_account" required:"true" description:"The owning account's internal identifier, from the roster row's owner column. It is the key the ownership decision and the transfer-target exclusion use, and a client compares it to identify the owner; it is not displayed. A business organization carries one; a personal organization's owner is the account itself. Empty only for a tenant the roster holds with no owner."`
	Status           string    `json:"status" required:"true" enum:"active,disabled,deleted" description:"The tenant's state: active is served, disabled is not and may be served again, deleted is a tombstone that is never served again."`
	CreatedAt        time.Time `json:"created_at" required:"true" description:"When the tenant was registered, as an RFC 3339 instant in UTC."`
}

// profileViewOf builds the view field by field, so a field later added to the
// record does not reach a client by being there.
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

func (p *product) kindOf(ctx context.Context, id tenant.ID) (tenant.Kind, error) {
	if p.kind == nil {
		return tenant.KindBusiness, nil
	}
	return p.kind.KindOf(ctx, id)
}
