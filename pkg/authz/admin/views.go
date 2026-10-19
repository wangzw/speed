package admin

import (
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// The views the management routes answer with. Each is built field by field from
// a root-package value and never handed through as it stands, so that a field
// added to a record later does not reach a client by being there. The names are
// exported because the OpenAPI fragment publishes them as components named after
// the type.

// AuthzAdminPermissionView is one declared permission as the catalog route shows
// it.
type AuthzAdminPermissionView struct {
	_ struct{} `description:"One permission the catalog declares."`

	Permission string `json:"permission" required:"true" description:"The permission name as the catalog declares it, for example authn.tenant.access. Only the authn.* permissions are the same vocabulary as authn.Action and carry its string; a permission outside it, such as authz.role.read, has no corresponding authn.Action."`
}

func permissionViewOf(p authz.Permission) AuthzAdminPermissionView {
	return AuthzAdminPermissionView{Permission: string(p)}
}

// AuthzAdminRoleView is one role as the role routes show it.
type AuthzAdminRoleView struct {
	_ struct{} `description:"One role of a tenant as the role routes report it."`

	Tenant         string    `json:"tenant" required:"true" description:"The tenant the role belongs to."`
	Role           string    `json:"role" required:"true" description:"The role name within the tenant."`
	Builtin        bool      `json:"builtin" required:"true" description:"True for a builtin role: the management face can neither rewrite nor remove it, nor change its grants. The mark is set by the host that seeds the policy data and is never cleared."`
	DescriptionKey string    `json:"description_key" required:"true" description:"The i18n key of the role's description text; stored as given and not resolved by this surface."`
	CreatedAt      time.Time `json:"created_at" required:"true" description:"When the role was written, as an RFC 3339 instant in UTC."`
}

func roleViewOf(r authz.RoleRecord) AuthzAdminRoleView {
	return AuthzAdminRoleView{
		Tenant:         r.Tenant,
		Role:           string(r.Role),
		Builtin:        r.Builtin,
		DescriptionKey: r.DescriptionKey,
		CreatedAt:      r.CreatedAt.UTC(),
	}
}

// AuthzAdminGrantView is one permission grant as the grant routes show it. The
// resource and the action are the pair the engine matches on; the permission a
// route names is translated to it on the way in.
type AuthzAdminGrantView struct {
	_ struct{} `description:"One permission grant of a role: the (resource, action) pair the policy engine matches on."`

	Tenant    string    `json:"tenant" required:"true" description:"The tenant the grant belongs to."`
	Role      string    `json:"role" required:"true" description:"The role holding the grant."`
	Resource  string    `json:"resource" required:"true" description:"The resource the grant matches on."`
	Action    string    `json:"action" required:"true" description:"The action the grant matches on."`
	CreatedAt time.Time `json:"created_at" required:"true" description:"When the grant was written, as an RFC 3339 instant in UTC."`
}

func grantViewOf(g authz.GrantRecord) AuthzAdminGrantView {
	return AuthzAdminGrantView{
		Tenant:    g.Tenant,
		Role:      string(g.Role),
		Resource:  g.Resource,
		Action:    g.Action,
		CreatedAt: g.CreatedAt.UTC(),
	}
}

// AuthzAdminBindingView is one role binding as the binding routes show it.
type AuthzAdminBindingView struct {
	_ struct{} `description:"One role binding: an account holds a role within a tenant."`

	Tenant    string    `json:"tenant" required:"true" description:"The tenant the binding belongs to."`
	Account   string    `json:"account" required:"true" description:"The account the role is bound to."`
	Role      string    `json:"role" required:"true" description:"The role bound to the account."`
	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

func bindingViewOf(b authz.Binding) AuthzAdminBindingView {
	return AuthzAdminBindingView{
		Tenant:    b.Tenant,
		Account:   b.Account,
		Role:      string(b.Role),
		GrantedAt: b.GrantedAt.UTC(),
	}
}
