package admin

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// AuthzAdminPermissionView is one declared permission as the catalog route shows
// it. The name is exported because the OpenAPI fragment names each component
// after its Go type; keep it distinct from the tenant management surface's
// views, since the merge refuses one component name carrying different content.
type AuthzAdminPermissionView struct {
	_ struct{} `description:"One permission the catalog declares."`

	// Permission is the permission name as the catalog declares it.
	Permission string `json:"permission" required:"true" description:"The permission name as the catalog declares it, for example authn.tenant.access. Only the authn.* permissions are the same vocabulary as authn.Action and carry its string; a permission outside it, such as authz.role.read, has no corresponding authn.Action."`

	// Description is the permission's description rendered in the request's language, empty when none is available.
	Description string `json:"description" required:"true" description:"The permission's description, rendered in the request's language from the key the declaring module gave it. Empty when the assembly delivers no message localizer or the module gave the permission no description key. A key no catalog carries comes back as the key itself, the i18n fallback; a client shows the text as received and does not resolve keys."`

	// Group is the group the declaring module places the permission in.
	Group string `json:"group" required:"true" description:"The group the declaring module places the permission in, for example authorization or membership. Empty when the module gave it no group; a client groups such a permission as it sees fit."`

	// Dangerous reports whether granting the permission widens the grantee's power over other accounts or over the tenant's policy.
	Dangerous bool `json:"dangerous" required:"true" description:"True when granting the permission widens the grantee's power over other accounts or over the tenant's policy; a client calls such a permission out before an operator grants it, and asks for a deliberate second confirmation."`

	// Grantable reports whether the caller holds the permission in the platform domain and may grant it.
	Grantable bool `json:"grantable" required:"true" description:"True when the caller holds the permission in the platform domain and may therefore grant it to a role. A permission the caller does not hold is still listed: the catalog is the whole declared set, not the caller's grantable subset."`
}

func (p *product) permissionViewOf(ctx context.Context, perm authz.Permission, grantable bool) AuthzAdminPermissionView {
	view := AuthzAdminPermissionView{Permission: string(perm), Grantable: grantable}
	if meta, ok := p.catalog.Metadata(perm); ok {
		view.Group = meta.Group
		view.Dangerous = meta.Dangerous
	}
	key, ok := p.catalog.DescriptionKey(perm)
	if !ok || key == "" || p.localizer == nil {
		return view
	}
	view.Description = p.localizer.Localize(ctx, i18n.Key(key), nil)
	return view
}

// AuthzAdminRoleView is one role as the role routes show it.
type AuthzAdminRoleView struct {
	_ struct{} `description:"One role of a tenant as the role routes report it."`

	// Tenant is the tenant the role belongs to.
	Tenant string `json:"tenant" required:"true" description:"The tenant the role belongs to."`
	// Role is the role's name within the tenant.
	Role string `json:"role" required:"true" description:"The role name within the tenant."`
	// Builtin marks a role this surface may neither rewrite nor remove, nor change its grants.
	Builtin bool `json:"builtin" required:"true" description:"True for a builtin role: the management face can neither rewrite nor remove it, nor change its grants. The mark is set by the host that seeds the policy data and is never cleared."`
	// Description is the role's description as a client shows it.
	Description string `json:"description" required:"true" description:"The role's description as a client shows it: the operator's free text for a role this surface created, or a builtin role's description key rendered in the request's language. With no message localizer assembled, a builtin role answers its key itself, the i18n fallback."`
	// PermissionCount is how many permission grants the role holds.
	PermissionCount int `json:"permission_count" required:"true" description:"How many permission grants the role holds, from one grouped read of the tenant's grants."`
	// MemberCount is how many active members hold the role, nil when no identity reader is assembled.
	MemberCount *int `json:"member_count" required:"true" nullable:"true" description:"How many active members hold the role. Null when the assembly delivers no account-identity reader: the count cannot be read, and the binding count is not a stand-in for it."`
	// CanManage reports whether the caller holds authz.role.manage in the platform domain.
	CanManage bool `json:"can_manage" required:"true" description:"True when the caller holds authz.role.manage in the platform domain, so a client renders the role, grant and binding controls. False hides them; the route guard still refuses a write the caller may not make."`
	// CreatedAt is when the role was written.
	CreatedAt time.Time `json:"created_at" required:"true" description:"When the role was written, as an RFC 3339 instant in UTC."`
}

func (p *product) roleViewOf(ctx context.Context, r authz.RoleRecord, permissionCount int, memberCount *int, canManage bool, tenantName string) AuthzAdminRoleView {
	return AuthzAdminRoleView{
		Tenant:          tenantName,
		Role:            string(r.Role),
		Builtin:         r.Builtin,
		Description:     p.roleDescription(ctx, r),
		PermissionCount: permissionCount,
		MemberCount:     memberCount,
		CanManage:       canManage,
		CreatedAt:       r.CreatedAt.UTC(),
	}
}

func (p *product) roleDescription(ctx context.Context, r authz.RoleRecord) string {
	if !r.Builtin {
		return r.Description
	}
	if r.DescriptionKey == "" {
		return r.Description
	}
	if p.localizer == nil {
		return r.DescriptionKey
	}
	return p.localizer.Localize(ctx, i18n.Key(r.DescriptionKey), nil)
}

// AuthzAdminGrantView is one permission grant as the grant routes show it.
type AuthzAdminGrantView struct {
	_ struct{} `description:"One permission grant of a role: the permission it was written as, and the (resource, action) pair the policy engine matches on."`

	// Tenant is the tenant the grant belongs to.
	Tenant string `json:"tenant" required:"true" description:"The tenant the grant belongs to."`
	// Role is the role holding the grant.
	Role string `json:"role" required:"true" description:"The role holding the grant."`
	// Permission is the permission the pair translates to, empty when no declared permission matches.
	Permission string `json:"permission" required:"true" description:"The permission the (resource, action) pair translates to, for example authz.role.manage; the empty string when the pair matches no declared permission, which is a grant left from a vocabulary this process no longer carries."`
	// Resource is the resource the grant matches on.
	Resource string `json:"resource" required:"true" description:"The resource the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	// Action is the action the grant matches on.
	Action string `json:"action" required:"true" description:"The action the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	// CreatedAt is when the grant was written.
	CreatedAt time.Time `json:"created_at" required:"true" description:"When the grant was written, as an RFC 3339 instant in UTC."`
}

func (p *product) grantViewOf(g authz.GrantRecord, tenantName string) AuthzAdminGrantView {
	view := AuthzAdminGrantView{
		Tenant:    tenantName,
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

// AuthzAdminBindingView is one role binding as the binding routes show it.
type AuthzAdminBindingView struct {
	_ struct{} `description:"One role binding: an account holds a role within a tenant."`

	// Tenant is the tenant the binding belongs to.
	Tenant string `json:"tenant" required:"true" description:"The tenant the binding belongs to."`
	// Account is the account the role is bound to.
	Account string `json:"account" required:"true" description:"The account the role is bound to."`
	// Role is the role bound to the account.
	Role string `json:"role" required:"true" description:"The role bound to the account."`
	// GrantedAt is when the binding was written.
	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

func bindingViewOf(b authz.Binding, tenantName string) AuthzAdminBindingView {
	return AuthzAdminBindingView{
		Tenant:    tenantName,
		Account:   b.Account,
		Role:      string(b.Role),
		GrantedAt: b.GrantedAt.UTC(),
	}
}
