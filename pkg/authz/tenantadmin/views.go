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

	Group string `json:"group" required:"true" description:"The group the declaring module places the permission in, for example authorization or membership. Empty when the module gave it no group; a client groups such a permission as it sees fit."`

	Dangerous bool `json:"dangerous" required:"true" description:"True when granting the permission widens the grantee's power over other accounts or over the tenant's policy; a client calls such a permission out before an operator grants it."`

	Grantable bool `json:"grantable" required:"true" description:"True when the caller holds the permission in the tenant and may therefore grant it to a role. A permission the caller does not hold is still listed: the catalog is the whole governed set, not the caller's grantable subset."`
}

// permissionViewOf builds one permission's view from the governed catalog. The
// description is the key the declaring module gave the permission, rendered
// through the optional message localizer in the request's locale. The metadata
// is the declaring module's, and grantable is the caller's own held set.
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

	Tenant  string `json:"tenant" required:"true" description:"The tenant the role belongs to."`
	Role    string `json:"role" required:"true" description:"The role name within the tenant. A role is never renamed: the name is the key a binding holds."`
	Builtin bool   `json:"builtin" required:"true" description:"True for a builtin role: the tenant management face can neither rewrite nor remove it, nor change its grants. The mark is set by the host that seeds the policy data and is never cleared."`

	// DescriptionKey is kept for a client that still reads the legacy field. The
	// role page reads Description; a client written before the free-text change
	// keeps working through this member.
	DescriptionKey string `json:"description_key" required:"true" description:"Deprecated: the i18n key of the role's description text, kept for a client that still reads it. It is stored as given and not resolved by this surface; new clients read description."`

	Description string `json:"description" required:"true" description:"The role's description in the request's language: a builtin role's key is rendered through the message localizer (falling back to the key), and a role the management face created carries the operator's free text as written."`

	PermissionCount int `json:"permission_count" required:"true" description:"How many permission grants the role holds within the tenant. A role holding none answers zero."`

	MemberCount *int `json:"member_count" description:"How many active members hold the role within the tenant. Null when the assembly delivers no account identity reader: the count cannot be told apart from an unavailable identity state, so it is not reported as zero."`

	CanManage bool `json:"can_manage" required:"true" description:"True when the caller holds authz.tenant.role.manage in the tenant, which decides whether a client renders the grant and revoke controls."`

	CreatedAt time.Time `json:"created_at" required:"true" description:"When the role was written, as an RFC 3339 instant in UTC."`
}

// roleViewOf builds one role's view. The description is the reader's choice of
// the role's two stored texts: a builtin role's key is rendered in the request's
// language, and a role the management face created carries the free text as
// written.
func (p *product) roleViewOf(ctx context.Context, r authz.RoleRecord, permissionCount int, memberCount *int, canManage bool) AuthzTenantAdminRoleView {
	return AuthzTenantAdminRoleView{
		Tenant:          r.Tenant,
		Role:            string(r.Role),
		Builtin:         r.Builtin,
		DescriptionKey:  r.DescriptionKey,
		Description:     p.roleDescription(ctx, r),
		PermissionCount: permissionCount,
		MemberCount:     memberCount,
		CanManage:       canManage,
		CreatedAt:       r.CreatedAt.UTC(),
	}
}

// roleDescription resolves a role's description. A builtin role's description is
// the localised text of the key the host seeded; a key no catalog carries comes
// back as the key itself, which is the i18n fallback and the stored
// description_key. A role the management face created carries the operator's
// free text as written.
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

// AuthzTenantAdminRoleBindingView is one account holding a role, as the role
// usage read shows it.
type AuthzTenantAdminRoleBindingView struct {
	_ struct{} `description:"One account holding a role within a tenant, as the role usage read shows it."`

	Account string `json:"account" required:"true" description:"The account the role is bound to."`

	DisplayName string `json:"display_name" required:"true" description:"The account's display name, as the host's account identity reader reports it. Empty when the assembly delivers no identity reader or the reader knows no name for the account."`

	Active *bool `json:"active" description:"Whether the account is still active. Null when the assembly delivers no account identity reader or the reader does not know the account: an unavailable identity state is reported as unknown, never as false."`

	GrantedAt time.Time `json:"granted_at" required:"true" description:"When the binding was written, as an RFC 3339 instant in UTC."`
}

// roleBindingViewOf builds one role-usage view from a binding and the identity
// the reader answered for its account.
func roleBindingViewOf(b authz.Binding, identity authz.AccountIdentity, known bool) AuthzTenantAdminRoleBindingView {
	view := AuthzTenantAdminRoleBindingView{
		Account:   b.Account,
		GrantedAt: b.GrantedAt.UTC(),
	}
	if !known {
		return view
	}
	view.DisplayName = identity.DisplayName
	active := identity.Active
	view.Active = &active
	return view
}

// roleBindingViewsOf builds the views of one role page. identities is nil when
// the assembly delivers no identity reader.
func roleBindingViewsOf(bindings []authz.Binding, identities map[string]authz.AccountIdentity) []AuthzTenantAdminRoleBindingView {
	views := make([]AuthzTenantAdminRoleBindingView, len(bindings))
	for i, b := range bindings {
		identity, known := identities[b.Account]
		views[i] = roleBindingViewOf(b, identity, known)
	}
	return views
}

// roleMemberCounts counts the active members of every role from one tenant-wide
// binding read and one batch identity read. A nil result member is the unknown
// state: with no identity reader the count cannot be told from an unavailable
// one, so it is reported as null and never as zero.
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

// accountIdentities resolves the accounts a role-usage read names. A nil reader
// answers nil: the identity state is unknown and the caller reports it as such.
// A reader that cannot answer is a 503, never an empty or inactive set.
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
