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

// SubjectPermissions reads the permissions a subject effectively holds in one
// policy domain. It is delivered exclusively by the policy subpackage, beside
// the enforcement engine, and it reads the same in-memory policy snapshot, so a
// read does no IO.
//
// It exists because a decision answers one query at a time: a host that has to
// show a caller what it may do, rather than ask whether it may do one thing,
// cannot derive the answer from the enforcement engine without enumerating a
// catalog and asking one query per permission — and a permission's (object,
// action) pair is not enough to tell a grant from a translation the process does
// not carry.
type SubjectPermissions interface {
	// Held returns the permission names the subject effectively holds in
	// domain: the union over the roles it is bound to there, mapped through the
	// catalog's translation. A (object, action) pair no declared permission
	// translates to is left out, and a permission reached through several roles
	// appears once. The result is sorted by permission name, and an empty result
	// is an empty slice, not nil.
	//
	// A non-nil error means the policy snapshot or its store is unavailable:
	// the caller must treat it as a 503 and never as an empty permission set,
	// the same rule Enforcer states for a decision it could not make.
	Held(ctx context.Context, domain Domain, subject string) ([]Permission, error)
}

// Catalog is the set of permissions this capability declares, and what each of
// them translates to. It is delivered exclusively by the policy subpackage and
// read by whatever enumerates the permissions, such as a management surface's
// permission picker, and by a host command that writes policy data in permission
// names — the grant table holds the (object, action) pair, not the permission, so
// writing one takes the translation.
//
// It carries the translation because the two are one set of data: the catalog is
// every module's declared permissions, and the translation is a row for every one
// of them. Reading it here is what lets a caller reach the translation without
// the enforcement module, and so without the HTTP entry point that module
// requires.
type Catalog interface {
	// Declared returns every declared permission. The result is a copy: a
	// caller sorting or trimming it does not disturb what the next caller sees.
	Declared() []Permission
	// Has reports whether p is a declared permission.
	Has(p Permission) bool
	// ObjectAction returns the (object, action) pair a permission translates to,
	// and whether the translation has a row for it at all.
	//
	// A declared permission need not have one. The catalog is built from the
	// modules' PermissionSpec and the translation from their TranslationSpec, and
	// a module may declare the first without the second — a legal assembly in
	// which a route naming that permission fails the startup and nothing else
	// does. A caller that writes policy data in a permission's name therefore
	// reads the second result: one that is false means there is no pair to write.
	ObjectAction(p Permission) (object, action string, ok bool)
	// DescriptionKey returns the i18n key of the description the declaring
	// module gave p, and whether p is declared at all. A declared permission may
	// carry no key: a module that listed it in PermissionSpec.Permissions and
	// left it out of DescriptionKeys is legal, and the second result is then true
	// with an empty key, which a reader shows as no description. A permission no
	// module declared answers ("", false).
	//
	// The catalog hands out the key, not the text: rendering it is the i18n
	// engine's, and the descriptor a renderer needs is the one the management
	// surface holds as an optional capability.
	DescriptionKey(p Permission) (key string, ok bool)
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
	// PutRole creates or rewrites a role without the builtin mark. A record
	// carrying the mark is refused with ErrBuiltinRole, and a builtin role
	// cannot be rewritten at all.
	PutRole(ctx context.Context, r RoleRecord) error
	// MarkRoleBuiltin sets the builtin mark on an existing role. It is the
	// only write that sets the mark: after it, the management surface cannot
	// rewrite or delete the role and cannot change its grants; Seeder is the
	// one write that still adds grants. An absent role is ErrUnknownRole; an
	// already-builtin role is no work.
	MarkRoleBuiltin(ctx context.Context, tenant string, role Role) error
	// DeleteRole removes a role. A builtin role cannot be deleted.
	DeleteRole(ctx context.Context, tenant string, role Role) error
	// PutGrant creates a grant on a role that exists in the tenant and is not
	// builtin. A builtin role's grants are changed only by the host's seeding
	// surface, Seeder.
	PutGrant(ctx context.Context, g GrantRecord) error
	// DeleteGrant removes a grant. A builtin role's grants are refused.
	DeleteGrant(ctx context.Context, g GrantRecord) error
	// PutBinding binds a role that exists in the tenant to a subject.
	PutBinding(ctx context.Context, b Binding) error
	// DeleteBinding removes a subject's binding to a role within a tenant.
	DeleteBinding(ctx context.Context, tenant, account string, role Role) error
	// ClearAccountBindings removes every binding the account holds within the
	// tenant in one statement. It is the host's account-deletion path, called
	// while an account is being deleted, so the last-tenant-administrator rule
	// that DeleteBinding enforces does not apply: the rule keeps a domain still
	// in service governable, and the deleted account's own winding-up is not a
	// removal from a domain it must leave governed. The write acts on one tenant
	// and one account: the tenant's other bindings and the account's bindings in
	// other tenants stay, and so do the roles and grants. It is idempotent: an
	// account with no binding in the tenant writes nothing.
	ClearAccountBindings(ctx context.Context, tenant, account string) error
}

// MembershipChecker reports whether an account is an active member of a tenant.
// The host supplies it; this package does not import the authentication
// capability.
//
// The policy subpackage takes it up optionally. A write binding
// PlatformAdminRole in the platform domain asks it before writing, and refuses
// with ErrMissingMembershipChecker when the assembly supplied none: an account
// no membership check can vouch for must not receive the platform
// administrator's permissions.
type MembershipChecker interface {
	IsMember(ctx context.Context, tenant string, account string) (bool, error)
}

// WriteGuard reports whether a tenant's policy data rejects a management write.
//
// The capability knows which tenants and roles a deployment must protect no
// more than it knows which accounts are their members, so the answer is a host
// seam. The management module asks it before a role, grant or binding write; a
// write the guard refuses is not carried out. The answer only narrows: it cannot
// make a write the Manager would refuse succeed.
//
// The seam is optional. An assembly that delivers no WriteGuard mounts the
// management routes as it did before the seam existed; an answer that cannot be
// read is a 503 and is never read as permission.
type WriteGuard interface {
	// Denied reports whether the tenant rejects the write op names. An error
	// means the answer could not be read, and a caller treats it as a refusal to
	// proceed rather than a pass.
	Denied(ctx context.Context, tenant string, op WriteOp) (bool, error)
}

// WriteOp names a management write a WriteGuard protects. The set is closed.
type WriteOp int

const (
	// WriteRole is a role create, rewrite or delete. It is the zero value, so a
	// value nobody chose reads as the write that can remove a role.
	WriteRole WriteOp = iota
	// WriteGrant adds or removes a permission a role holds.
	WriteGrant
	// WriteBinding binds or unbinds an account to a role.
	WriteBinding
)

// Seeder is the host's seeding surface over the policy data: it adds the grants
// a role is declared to hold, and it is the one write that reaches a builtin
// role's grant set. The policy subpackage delivers it exclusively, beside
// Manager.
//
// It is a capability of its own and not a method on Manager because the
// management surface takes Manager up: a granted route can then change a role's
// grants only through Manager, which refuses a builtin role, and holds no way to
// reach this surface. The seeding write exists for the host, which has to be
// able to re-run its bootstrap after the process declares a permission the role
// does not hold: the builtin mark keeps the role off the management surface's
// delete and rewrite paths, and the host's bootstrap keeps the role complete.
//
// A seed adds grants and never removes, rewrites or deletes one. A builtin
// role's existing grants are therefore fixed against every path, and a host
// that needs to revoke one gives the account another role instead.
type Seeder interface {
	// SeedGrants adds grants to a role that exists in the tenant, whether or
	// not it is builtin. A grant the role already holds is no work and no
	// error, so a repeated seed over an unchanged declared set writes nothing
	// and reports zero; the result is how many grants this call added, not how
	// many it considered. An absent role is ErrUnknownRole and an empty tenant
	// is ErrTenantMismatch.
	SeedGrants(ctx context.Context, tenant string, role Role, grants []Grant) (int, error)
}
