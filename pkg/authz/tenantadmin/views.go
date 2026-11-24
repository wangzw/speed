package tenantadmin

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/i18n"
)

// AuthzTenantAdminPermissionView is one governed permission as the catalog route
// shows it. The name is exported because the OpenAPI fragment names each
// component after its Go type; keep it distinct from the platform management
// surface's views, since the merge refuses one component name carrying
// different content.
type AuthzTenantAdminPermissionView struct {
	_ struct{} `description:"One permission of the tenant-governed set."`

	Permission string `json:"permission" required:"true" description:"The permission name as the catalog declares it, for example authz.tenant.role.read. Only the authn.* permissions are the same vocabulary as authn.Action and carry its string."`

	Description string `json:"description" required:"true" description:"The permission's description, rendered in the request's language from the key the declaring module gave it. Empty when the assembly delivers no message localizer or the module gave the permission no description key. A key no catalog carries comes back as the key itself, the i18n fallback."`

	Group string `json:"group" required:"true" description:"The group the declaring module places the permission in, for example authorization or membership. Empty when the module gave it no group; a client groups such a permission as it sees fit."`

	Dangerous bool `json:"dangerous" required:"true" description:"True when granting the permission widens the grantee's power over other accounts or over the tenant's policy; a client calls such a permission out before an operator grants it."`

	Grantable bool `json:"grantable" required:"true" description:"True when the caller holds the permission in the tenant and may therefore grant it to a role. A permission the caller does not hold is still listed: the catalog is the whole governed set, not the caller's grantable subset."`
}

// permissionViewOf builds one permission's view from the governed catalog.
func (p *product) permissionViewOf(ctx context.Context, perm authz.Permission, grantable bool) AuthzTenantAdminPermissionView {
	view := AuthzTenantAdminPermissionView{Permission: string(perm), Grantable: grantable}
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

// AuthzTenantAdminRoleView is one role as the role routes show it.
type AuthzTenantAdminRoleView struct {
	_ struct{} `description:"One role of a tenant as the role routes report it."`

	Tenant  string `json:"tenant" required:"true" description:"The name of the tenant the role belongs to."`
	Role    string `json:"role" required:"true" description:"The role name within the tenant. A role is never renamed: the name is the key a binding holds."`
	Builtin bool   `json:"builtin" required:"true" description:"True for a builtin role: the tenant management face can neither rewrite nor remove it, nor change its grants. The mark is set by the host that seeds the policy data and is never cleared."`

	Description string `json:"description" required:"true" description:"The role's description in the request's language: a builtin role's key is rendered through the message localizer (falling back to the key), and a role the management face created carries the operator's free text as written."`

	PermissionCount int `json:"permission_count" required:"true" description:"How many permission grants the role holds within the tenant. A role holding none answers zero."`

	MemberCount *int `json:"member_count" required:"true" nullable:"true" description:"How many active members hold the role within the tenant. Null when the assembly delivers no account identity reader: the count cannot be told apart from an unavailable identity state, so it is not reported as zero."`

	CanManage bool `json:"can_manage" required:"true" description:"True when the caller holds authz.tenant.role.manage in the tenant, which decides whether a client renders the grant and revoke controls."`

	CreatedAt time.Time `json:"created_at" required:"true" description:"When the role was written, as an RFC 3339 instant in UTC."`
}

func (p *product) roleViewOf(ctx context.Context, r authz.RoleRecord, permissionCount int, memberCount *int, canManage bool, tenantName string) AuthzTenantAdminRoleView {
	return AuthzTenantAdminRoleView{
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

// roleDescription resolves a role's description: a builtin role's key localised,
// or the operator's free text.
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

// AuthzTenantAdminGrantView is one permission grant as the grant routes show it.
type AuthzTenantAdminGrantView struct {
	_ struct{} `description:"One permission grant of a role: the permission it was written as, and the (resource, action) pair the policy engine matches on."`

	Tenant     string    `json:"tenant" required:"true" description:"The name of the tenant the grant belongs to."`
	Role       string    `json:"role" required:"true" description:"The role holding the grant."`
	Permission string    `json:"permission" required:"true" description:"The permission the (resource, action) pair translates to, for example authz.tenant.role.manage; the empty string when the pair matches no declared permission, which is a grant left from a vocabulary this process no longer carries."`
	Resource   string    `json:"resource" required:"true" description:"The resource the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	Action     string    `json:"action" required:"true" description:"The action the grant matches on, kept beside the permission so an untranslatable grant can still be diagnosed."`
	CreatedAt  time.Time `json:"created_at" required:"true" description:"When the grant was written, as an RFC 3339 instant in UTC."`
}

// grantViewOf builds one grant's view. A pair no declared permission translates
// to leaves the permission field empty and the pair on the view.
func (p *product) grantViewOf(g authz.GrantRecord, tenantName string) AuthzTenantAdminGrantView {
	view := AuthzTenantAdminGrantView{
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

// AuthzTenantAdminBindingView is one role binding as the binding routes show it.
type AuthzTenantAdminBindingView struct {
	_ struct{} `description:"One role binding: an account holds a role within a tenant."`

	Tenant    string    `json:"tenant" required:"true" description:"The name of the tenant the binding belongs to."`
	Account   string    `json:"account" required:"true" description:"The account the role is bound to."`
	Role      string    `json:"role" required:"true" description:"The role bound to the account."`
	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

func bindingViewOf(b authz.Binding, tenantName string) AuthzTenantAdminBindingView {
	return AuthzTenantAdminBindingView{
		Tenant:    tenantName,
		Account:   b.Account,
		Role:      string(b.Role),
		GrantedAt: b.GrantedAt.UTC(),
	}
}

func bindingViewsOf(bindings []authz.Binding, tenantName string) []AuthzTenantAdminBindingView {
	views := make([]AuthzTenantAdminBindingView, len(bindings))
	for i, b := range bindings {
		views[i] = bindingViewOf(b, tenantName)
	}
	return views
}

// AuthzTenantAdminRoleBindingView is one account holding a role, as the role
// usage read shows it.
type AuthzTenantAdminRoleBindingView struct {
	_ struct{} `description:"One account holding a role within a tenant, as the role usage read shows it."`

	Account string `json:"account" required:"true" description:"The account the role is bound to."`

	DisplayName string `json:"display_name" required:"true" description:"The account's display name, as the host's account identity reader reports it. Empty when the assembly delivers no identity reader or the reader knows no name for the account."`

	Username string `json:"username" required:"true" description:"The account's login username, as the host's account identity reader reports it: unique, user-facing, and not an identifier of the account. Empty when the assembly delivers no identity reader or the account has none; it is never a fallback to another login identifier."`

	Active *bool `json:"active" required:"true" nullable:"true" description:"Whether the account is still active. Null when the assembly delivers no account identity reader or the reader does not know the account: an unavailable identity state is reported as unknown, never as false."`

	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

func roleBindingViewOf(b authz.Binding, identity authz.AccountIdentity, known bool) AuthzTenantAdminRoleBindingView {
	view := AuthzTenantAdminRoleBindingView{
		Account:   b.Account,
		GrantedAt: b.GrantedAt.UTC(),
	}
	if !known {
		return view
	}
	view.DisplayName = identity.DisplayName
	view.Username = identity.Username
	active := identity.Active
	view.Active = &active
	return view
}

func roleBindingViewsOf(bindings []authz.Binding, identities map[string]authz.AccountIdentity) []AuthzTenantAdminRoleBindingView {
	views := make([]AuthzTenantAdminRoleBindingView, len(bindings))
	for i, b := range bindings {
		identity, known := identities[b.Account]
		views[i] = roleBindingViewOf(b, identity, known)
	}
	return views
}

// roleMemberCounts counts the active members of every role. With no identity
// reader a count is null, never zero.
func (p *product) roleMemberCounts(ctx context.Context, tenant string, roles []authz.RoleRecord) (map[authz.Role]*int, error) {
	counts := make(map[authz.Role]*int, len(roles))
	if p.identities == nil {
		for _, role := range roles {
			counts[role.Role] = nil
		}
		return counts, nil
	}
	bindings, err := p.manager.Bindings(ctx, tenant)
	if err != nil {
		return nil, p.failureOf("listing the roles' bindings", err)
	}
	accountsByRole := make(map[authz.Role][]string, len(roles))
	seen := make(map[string]struct{}, len(bindings))
	accounts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		accountsByRole[b.Role] = append(accountsByRole[b.Role], b.Account)
		if _, twice := seen[b.Account]; twice {
			continue
		}
		seen[b.Account] = struct{}{}
		accounts = append(accounts, b.Account)
	}
	var identities map[string]authz.AccountIdentity
	if len(accounts) > 0 {
		identities, err = p.accountIdentities(ctx, tenant, accounts)
		if err != nil {
			return nil, err
		}
	}
	for _, role := range roles {
		active := 0
		for _, account := range accountsByRole[role.Role] {
			if identity, known := identities[account]; known && identity.Active {
				active++
			}
		}
		count := active
		counts[role.Role] = &count
	}
	return counts, nil
}

// accountIdentities resolves the accounts a role-usage read names. A reader that
// cannot answer is a 503, never an empty or inactive set.
func (p *product) accountIdentities(ctx context.Context, tenant string, accounts []string) (map[string]authz.AccountIdentity, error) {
	if p.identities == nil {
		return nil, nil
	}
	identities, err := p.identities.Identities(ctx, tenant, accounts)
	if err != nil {
		return nil, p.unavailable("reading the accounts behind the role bindings", err)
	}
	return identities, nil
}
