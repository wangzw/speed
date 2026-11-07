// Package authz is the interface package of the authorization capability
// directory: the types, capability interfaces, permission constants, model
// text and sentinel errors that the policy and enforcement subpackages and
// every consumer of a permission share.
//
// This package registers no module and imports nothing but the standard
// library. It is imported by every consumer of a permission — the enforcement
// point, the management surface and any host code that names a permission — so
// a third-party library or a subpackage of this directory reached from here
// would enter all of their closures. The policy subpackage is where the Casbin
// engine and its storage live and the enforcement subpackage is where gin
// lives; neither belongs here.
package authz

import "time"

// Permission names one thing a subject may do, as a string. It maps to one
// (resource, action) pair through the translation the enforcement point holds,
// and that pair is what the policy engine matches.
//
// The values align one-to-one with authn.Action: a constant here carries the
// same string as the action of the same name, so the question the
// authentication side asks and the permission this side stores are one
// vocabulary. A published value is never renamed, and a new constant never
// changes an existing one.
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
// key of the description shown for each. A module hands one of these to the
// policy subpackage's catalog.
//
// TenantGoverned must be a subset of Permissions: a permission marked governed
// and not declared is a startup ErrUnknownPermission. A declared permission not
// in the subset is platform-only — no tenant administrator receives it.
type PermissionSpec struct {
	Permissions     []Permission
	TenantGoverned  []Permission
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
// the grantee's power over other accounts or over the tenant's policy, which a
// management surface calls out when an operator is about to grant it.
type PermissionMeta struct {
	Group     string
	Dangerous bool
}

// Translation is one row of the permission translation: the object and the
// action the policy engine matches on for a permission.
type Translation struct {
	Object string
	Action string
}

// TranslationSpec is what one module declares about translating the permissions
// it owns beyond the ones this capability carries: a row for each, naming the
// (object, action) pair the engine matches on.
//
// The enforcement subpackage unions every registered module's rows with the rows
// it carries itself to form the one translation the whole process decides by, so
// a module that declares a permission of its own also declares how it
// translates. A permission the union has no row for is one the enforcement
// point cannot express, and a route that names it fails the startup.
type TranslationSpec struct {
	Rows map[Permission]Translation
}

// Query is one question put to the engine: whether Subject may do Action on
// Object within Domain. Subject is an account's key; the pair (Object, Action)
// comes from the permission-to-(resource, action) translation the enforcement
// point holds, and Domain is a tenant identifier or the platform domain.
type Query struct {
	Subject string
	Domain  Domain
	Object  string
	Action  string
}

// Requirement is what one route asks for: the permission needed to reach
// (Method, Path). A route registered with no requirement is open.
type Requirement struct {
	Method     string
	Path       string
	Permission Permission
}

// Binding is one role binding as it is read: a subject holding a role within a
// tenant.
type Binding struct {
	Tenant    string
	Account   string
	Role      Role
	GrantedAt time.Time
}

// RoleRecord is one role of the role table as it is read and written. Builtin
// marks a role the management surface may not delete or rewrite and whose
// grants it may not change; it is set only by Manager.MarkRoleBuiltin and read
// back as it stands. A builtin role's grants are extended only by
// Seeder.SeedGrants and converged only by Reconciler.ReconcileGrants.
//
// DescriptionKey is the i18n key a host seeds into a builtin role; Description is
// the free text the management surface writes. Both are stored, and a reader
// prefers the description it can render: a builtin role with a key localises it,
// while a role the management surface created carries the operator's own text.
type RoleRecord struct {
	Tenant         string
	Role           Role
	Builtin        bool
	DescriptionKey string
	Description    string
	CreatedAt      time.Time
}

// RoleDescriptionMax bounds a role's free-text description, in characters. A
// management surface refuses a longer one before it reaches the Manager; the
// Manager stores what it is given.
const RoleDescriptionMax = 500

// GrantRecord is one permission grant of the grant table as it is read and
// written: a role holding a (resource, action) permission within a tenant.
type GrantRecord struct {
	Tenant    string
	Role      Role
	Resource  string
	Action    string
	CreatedAt time.Time
}

// Grant is one permission grant a seeding step adds or removes from a role: the
// (resource, action) pair the role holds. It is the input shape of
// Seeder.SeedGrants and of Reconciler.ReconcileGrants, each of which names the
// tenant and the role once for the whole set; a stored row is a GrantRecord,
// which also carries the row's tenant, role and creation time.
type Grant struct {
	Resource string
	Action   string
}

// AccountIdentity is what a host knows about one account, as the role-usage read
// needs it: the display name and login username to show, and whether the account
// is still active. The username is the account's user-facing login name, never
// an identifier used as a fallback; a host that knows no username answers the
// empty string. The capability holds no account table of its own, so a host
// supplies all of it through AccountIdentityReader.
type AccountIdentity struct {
	DisplayName string
	Username    string
	Active      bool
}
