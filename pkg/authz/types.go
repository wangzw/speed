// Package authz is the interface package of the authorization capability: the types, capability interfaces, permission constants, model text and sentinels its consumers and implementations share.
package authz

import "time"

// Permission names one thing a subject may do, as a string. It maps to one
// (resource, action) pair through the translation the enforcement point holds,
// and that pair is what the policy engine matches.
//
// The values align one-to-one with authn.Action: a constant here carries the
// same string as the action of the same name.
type Permission string

// Role names a set of permissions within one policy domain. A role is granted a
// permission by a grant row and held by a subject through a binding row; it
// carries no hierarchy and no inheritance.
type Role string

// Domain is the scope a policy row belongs to: a tenant identifier, or the
// reserved platform domain PlatformDomain. A role binding and a permission
// grant both carry one.
type Domain string

// PermissionSpec is what one module declares about the permissions it owns: the
// list of them, which of those a tenant administrator may govern, and the i18n
// key of the description shown for each.
//
// TenantGoverned must be a subset of Permissions: a permission marked governed
// and not declared is a startup ErrUnknownPermission. A declared permission not
// in the subset is platform-only.
type PermissionSpec struct {
	// Permissions is the set of permissions this module declares.
	Permissions []Permission
	// TenantGoverned is the subset of Permissions a tenant administrator may govern.
	TenantGoverned []Permission
	// DescriptionKeys maps a declared permission to the i18n key of its description.
	DescriptionKeys map[Permission]string
	// Metadata is the governance metadata of the permissions this module
	// declares: the group a management surface places one in and whether granting
	// it is dangerous. An entry for a permission the module does not list in
	// Permissions is ignored.
	Metadata map[Permission]PermissionMeta
}

// PermissionMeta is what a declaring module says about one permission beyond its
// name and description: the group a management surface groups it under and
// whether granting it is dangerous. A dangerous permission is one that widens
// the grantee's power over other accounts or over the tenant's policy.
type PermissionMeta struct {
	// Group is the group a management surface places the permission in.
	Group string
	// Dangerous reports whether granting the permission widens the grantee's power over other accounts or over the tenant's policy.
	Dangerous bool
}

// Translation is one row of the permission translation: the object and the
// action the policy engine matches on for a permission.
type Translation struct {
	// Object is the object the policy engine matches on.
	Object string
	// Action is the action the policy engine matches on.
	Action string
}

// TranslationSpec is what one module declares about translating the permissions
// it owns beyond the ones this capability carries: a row for each, naming the
// (object, action) pair the engine matches on.
type TranslationSpec struct {
	// Rows maps each declared permission to the (object, action) pair the engine matches on.
	Rows map[Permission]Translation
}

// Query is one question put to the engine: whether Subject may do Action on
// Object within Domain. Subject is an account's key, and Domain is a tenant
// identifier or the platform domain.
type Query struct {
	// Subject is the account key the question is about.
	Subject string
	// Domain is the tenant identifier or the platform domain the question is asked in.
	Domain Domain
	// Object is the object the action is applied to.
	Object string
	// Action is the action being asked about.
	Action string
}

// Requirement is what one route asks for: the permission needed to reach
// (Method, Path). A route registered with no requirement is open.
type Requirement struct {
	// Method is the HTTP method of the route.
	Method string
	// Path is the route's path.
	Path string
	// Permission is the permission needed to reach the route.
	Permission Permission
}

// Binding is one role binding as it is read: a subject holding a role within a
// tenant.
type Binding struct {
	// Tenant is the tenant the binding belongs to.
	Tenant string
	// Account is the account holding the role.
	Account string
	// Role is the role the account holds.
	Role Role
	// GrantedAt is when the binding was written.
	GrantedAt time.Time
}

// RoleRecord is one role of the role table as it is read and written. Builtin
// marks a role the management surface may not delete or rewrite and whose
// grants it may not change; only Manager.MarkRoleBuiltin sets it.
//
// DescriptionKey is the i18n key a host seeds into a builtin role; Description is
// the free text the management surface writes. A reader prefers the description
// it can render.
type RoleRecord struct {
	// Tenant is the tenant the role belongs to.
	Tenant string
	// Role is the role's name within the tenant.
	Role Role
	// Builtin marks a role the management surface may not delete or rewrite, nor change the grants of.
	Builtin bool
	// DescriptionKey is the i18n key a host seeds into a builtin role.
	DescriptionKey string
	// Description is the free text the management surface writes.
	Description string
	// CreatedAt is when the role was written.
	CreatedAt time.Time
}

// RoleDescriptionMax bounds a role's free-text description, in characters. A
// management surface refuses a longer one before it reaches the Manager; the
// Manager stores what it is given.
const RoleDescriptionMax = 500

// GrantRecord is one permission grant of the grant table as it is read and
// written: a role holding a (resource, action) permission within a tenant.
type GrantRecord struct {
	// Tenant is the tenant the grant belongs to.
	Tenant string
	// Role is the role holding the grant.
	Role Role
	// Resource is the resource the grant matches on.
	Resource string
	// Action is the action the grant matches on.
	Action string
	// CreatedAt is when the grant was written.
	CreatedAt time.Time
}

// Grant is one permission grant a seeding step adds or removes from a role: the
// (resource, action) pair the role holds. Seeder.SeedGrants and
// Reconciler.ReconcileGrants name the tenant and the role once for the whole
// set.
type Grant struct {
	// Resource is the resource the grant matches on.
	Resource string
	// Action is the action the grant matches on.
	Action string
}

// AccountIdentity is what a host knows about one account, as the role-usage read
// needs it: the display name and login username to show, and whether the account
// is still active. A host that knows no username answers the empty string.
type AccountIdentity struct {
	// DisplayName is the account's display name to show.
	DisplayName string
	// Username is the account's login username, empty when the host knows none.
	Username string
	// Active reports whether the account is still active.
	Active bool
}
