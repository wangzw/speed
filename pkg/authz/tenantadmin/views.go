package tenantadmin

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// The views the tenant management routes answer with. Each is built field by
// field from a root-package value and never handed through as it stands, so that
// a field added to a record later does not reach a client by being there. The
// names are exported because the OpenAPI fragment publishes them as components
// named after the type; they are distinct from the platform surface's views so
// that merging the two fragments has no component to reconcile.

// AuthzTenantAdminPermissionView is one governed permission as the catalog route
// shows it.
type AuthzTenantAdminPermissionView struct {
	_ struct{} `description:"One permission of the tenant-governed set."`

	Permission string `json:"permission" required:"true" description:"The permission name as the catalog declares it, for example authz.tenant.role.read. Only the authn.* permissions are the same vocabulary as authn.Action and carry its string."`

	Description string `json:"description" required:"true" description:"The permission's description, rendered in the request's language from the key the declaring module gave it. Empty when the assembly delivers no message localizer or the module gave the permission no description key. A key no catalog carries comes back as the key itself, the i18n fallback."`
}

// permissionViewOf builds one permission's view from the governed catalog. The
// description is the key the declaring module gave the permission, rendered
// through the optional message localizer in the request's locale.
func (p *product) permissionViewOf(ctx context.Context, perm authz.Permission) AuthzTenantAdminPermissionView {
	view := AuthzTenantAdminPermissionView{Permission: string(perm)}
	key, ok := p.catalog.DescriptionKey(perm)
	if !ok || key == "" || p.localizer == nil {
		return view
	}
	view.Description = p.localizer.Localize(ctx, i18n.Key(key), nil)
	return view
}

// AuthzTenantAdminRoleView is one role as the role routes show it.
type AuthzTenantAdminRoleView struct {
	_ struct{} `description:"One role of a tenant as the role routes report it."`

	Tenant         string    `json:"tenant" required:"true" description:"The tenant the role belongs to."`
	Role           string    `json:"role" required:"true" description:"The role name within the tenant."`
	Builtin        bool      `json:"builtin" required:"true" description:"True for a builtin role: the tenant management face can neither rewrite nor remove it, nor change its grants. The mark is set by the host that seeds the policy data and is never cleared."`
	DescriptionKey string    `json:"description_key" required:"true" description:"The i18n key of the role's description text; stored as given and not resolved by this surface."`
	CreatedAt      time.Time `json:"created_at" required:"true" description:"When the role was written, as an RFC 3339 instant in UTC."`
}

func roleViewOf(r authz.RoleRecord) AuthzTenantAdminRoleView {
	return AuthzTenantAdminRoleView{
		Tenant:         r.Tenant,
		Role:           string(r.Role),
		Builtin:        r.Builtin,
		DescriptionKey: r.DescriptionKey,
		CreatedAt:      r.CreatedAt.UTC(),
	}
}

// AuthzTenantAdminGrantView is one permission grant as the grant routes show
// it. The resource and the action are the pair the engine matches on; the
// permission a route names is translated to it on the way in, and the view
// translates it back so a client can name the grant without a local table.
type AuthzTenantAdminGrantView struct {
	_ struct{} `description:"One permission grant of a role: the permission it was written as, and the (resource, action) pair the policy engine matches on."`

	Tenant     string    `json:"tenant" required:"true" description:"The tenant the grant belongs to."`
	Role       string    `json:"role" required:"true" description:"The role holding the grant."`
	Permission string    `json:"permission" required:"true" description:"The permission the (resource, action) pair translates to, for example authz.tenant.role.manage; the empty string when the pair matches no declared permission, which is a grant left from a vocabulary this process no longer carries."`
	Resource   string    `json:"resource" required:"true" description:"The resource the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	Action     string    `json:"action" required:"true" description:"The action the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	CreatedAt  time.Time `json:"created_at" required:"true" description:"When the grant was written, as an RFC 3339 instant in UTC."`
}

// grantViewOf builds one grant's view. The permission is read back from the
// catalog's reverse index; a pair no declared permission translates to leaves
// the field empty and the pair itself on the view.
func (p *product) grantViewOf(g authz.GrantRecord) AuthzTenantAdminGrantView {
	view := AuthzTenantAdminGrantView{
		Tenant:    g.Tenant,
		Role:      string(g.Role),
		Resource:  g.Resource,
		Action:    g.Action,
		CreatedAt: g.CreatedAt.UTC(),
	}
	if permission, ok := p.catalog.PermissionOf(g.Resource, g.Action); ok {
		view.Permission = string(permission)
	}
	return view
}

// AuthzTenantAdminBindingView is one role binding as the binding routes show it.
type AuthzTenantAdminBindingView struct {
	_ struct{} `description:"One role binding: an account holds a role within a tenant."`

	Tenant    string    `json:"tenant" required:"true" description:"The tenant the binding belongs to."`
	Account   string    `json:"account" required:"true" description:"The account the role is bound to."`
	Role      string    `json:"role" required:"true" description:"The role bound to the account."`
	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

func bindingViewOf(b authz.Binding) AuthzTenantAdminBindingView {
	return AuthzTenantAdminBindingView{
		Tenant:    b.Tenant,
		Account:   b.Account,
		Role:      string(b.Role),
		GrantedAt: b.GrantedAt.UTC(),
	}
}

// bindingViewsOf builds the views of a binding read.
func bindingViewsOf(bindings []authz.Binding) []AuthzTenantAdminBindingView {
	views := make([]AuthzTenantAdminBindingView, len(bindings))
	for i, b := range bindings {
		views[i] = bindingViewOf(b)
	}
	return views
}
