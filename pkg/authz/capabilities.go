package authz

import "context"

// Enforcer answers one Query. It is delivered exclusively by the policy
// subpackage, and it reads the process's in-memory policy snapshot rather than
// the store, so a decision does no IO.
//
// A false result is a policy denial. A non-nil error means the engine or its
// store is unavailable: the caller must treat it as a 503 and never as a
// denial, and must not read it as permission either.
type Enforcer interface {
	// Allowed reports whether the subject may do the query's action on its
	// object within its domain. It returns once ctx ends and is safe to call
	// from many goroutines.
	Allowed(ctx context.Context, q Query) (bool, error)
}

// Catalog is the set of permissions this capability declares. It is delivered
// exclusively by the policy subpackage and read by whatever enumerates the
// permissions, such as a management surface's permission picker.
type Catalog interface {
	// Declared returns every declared permission. The result is a copy: a
	// caller sorting or trimming it does not disturb what the next caller sees.
	Declared() []Permission
	// Has reports whether p is a declared permission.
	Has(p Permission) bool
}

// Gate is the route declaration surface: it records the permission one
// (method, path) needs, or records it as open. The policy and enforcement
// subpackages use the same declarations, so the enforcement middleware and the
// Decider share one table.
//
// The write window is Init: a module declares its routes from its own Init
// callback.
type Gate interface {
	// Require records that (method, path) needs perm. A route declared with a
	// permission the catalog does not hold fails the startup.
	Require(method, path string, perm Permission)
	// Open records that (method, path) needs no permission.
	Open(method, path string)
	// Covered reports whether (method, path) carries any declaration, open or
	// requiring a permission.
	Covered(method, path string) bool
}

// Manager is the read and write surface over the policy data: roles, grants and
// bindings. The policy subpackage delivers it exclusively and the management
// subpackage takes it up.
//
// Every method names the tenant it acts within, and a write acts on one tenant
// alone.
type Manager interface {
	// Roles returns every role of a tenant.
	Roles(ctx context.Context, tenant string) ([]RoleRecord, error)
	// Grants returns every grant a role holds within a tenant.
	Grants(ctx context.Context, tenant string, role Role) ([]GrantRecord, error)
	// Bindings returns every binding of a tenant.
	Bindings(ctx context.Context, tenant string) ([]Binding, error)
	// BindingsOf returns every binding a subject holds within a tenant.
	BindingsOf(ctx context.Context, tenant, account string) ([]Binding, error)
	// PutRole creates or rewrites a role. A builtin role's permissions cannot
	// be changed.
	PutRole(ctx context.Context, r RoleRecord) error
	// DeleteRole removes a role. A builtin role cannot be deleted.
	DeleteRole(ctx context.Context, tenant string, role Role) error
	// PutGrant creates a grant on a role that exists in the tenant.
	PutGrant(ctx context.Context, g GrantRecord) error
	// DeleteGrant removes a grant.
	DeleteGrant(ctx context.Context, g GrantRecord) error
	// PutBinding binds a role that exists in the tenant to a subject.
	PutBinding(ctx context.Context, b Binding) error
	// DeleteBinding removes a subject's binding to a role within a tenant.
	DeleteBinding(ctx context.Context, tenant, account string, role Role) error
}
