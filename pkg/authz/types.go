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
// list of them, and the i18n key of the description shown for each. A module
// hands one of these to the policy subpackage's catalog.
type PermissionSpec struct {
	Permissions     []Permission
	DescriptionKeys map[Permission]string
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
// marks a role that cannot be deleted and whose permissions cannot be changed.
type RoleRecord struct {
	Tenant         string
	Role           Role
	Builtin        bool
	DescriptionKey string
	CreatedAt      time.Time
}

// GrantRecord is one permission grant of the grant table as it is read and
// written: a role holding a (resource, action) permission within a tenant.
type GrantRecord struct {
	Tenant    string
	Role      Role
	Resource  string
	Action    string
	CreatedAt time.Time
}
