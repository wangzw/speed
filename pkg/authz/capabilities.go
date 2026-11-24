package authz

import "context"

// Enforcer answers one Query. It is delivered exclusively by the policy
// subpackage and reads the process's in-memory policy snapshot, so a decision
// does no IO.
//
// A false result is a policy denial. A non-nil error means the engine or its
// store is unavailable: the caller must treat it as a 503, never as a denial or
// as permission.
type Enforcer interface {
	// Allowed reports whether the subject may do the query's action on its
	// object within its domain. It returns once ctx ends and is safe to call
	// from many goroutines.
	Allowed(ctx context.Context, q Query) (bool, error)
}

// SubjectPermissions reads the permissions a subject effectively holds in one
// policy domain. It is delivered exclusively by the policy subpackage and reads
// the same in-memory policy snapshot as Enforcer, so a read does no IO.
type SubjectPermissions interface {
	// Held returns the permission names the subject effectively holds in
	// domain: the union over the roles it is bound to there, mapped through the
	// catalog's translation. A (object, action) pair no declared permission
	// translates to is left out, and a permission reached through several roles
	// appears once. The result is sorted by permission name, and an empty result
	// is an empty slice, not nil.
	//
	// A non-nil error means the policy snapshot or its store is unavailable:
	// the caller must treat it as a 503 and never as an empty permission set.
	Held(ctx context.Context, domain Domain, subject string) ([]Permission, error)
}

// Catalog is the set of permissions this capability declares, and what each of
// them translates to. It is delivered exclusively by the policy subpackage.
type Catalog interface {
	// Declared returns every declared permission. The result is a copy.
	Declared() []Permission
	// TenantGoverned returns the governed set: every declared permission a
	// module marked TenantGoverned, in permission-name order. The platform
	// administrator's grant set is not narrowed by it; a tenant administrator's
	// grant set is exactly it.
	TenantGoverned() []Permission
	// Governed reports whether a declared permission belongs to the governed set.
	// An undeclared permission answers false.
	Governed(p Permission) bool
	// Has reports whether p is a declared permission.
	Has(p Permission) bool
	// ObjectAction returns the (object, action) pair a permission translates to,
	// and whether the translation has a row for it at all. A declared permission
	// need not have one.
	ObjectAction(p Permission) (object, action string, ok bool)
	// PermissionOf returns the declared permission a (object, action) pair
	// translates back to, and whether any declared permission translates to it
	// at all. Rows are unique, so the answer is a function of the pair.
	PermissionOf(object, action string) (Permission, bool)
	// DescriptionKey returns the i18n key of the description the declaring
	// module gave p, and whether p is declared at all. A declared permission may
	// carry no key. The catalog hands out the key, not the text.
	DescriptionKey(p Permission) (key string, ok bool)
	// Metadata returns the governance metadata the declaring module gave p, and
	// whether p is declared at all. A declared permission whose module supplied
	// no metadata answers the zero PermissionMeta and true.
	Metadata(p Permission) (PermissionMeta, bool)
}

// Gate is the route declaration surface: it records the permission one
// (method, path) needs, or records it as open. The enforcement middleware and
// the Decider share one table. The write window is Init.
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
// subpackage takes it up. Every method names the tenant it acts within, and a
// write acts on one tenant alone.
type Manager interface {
	// Roles returns every role of a tenant.
	Roles(ctx context.Context, tenant string) ([]RoleRecord, error)
	// Grants returns every grant a role holds within a tenant.
	Grants(ctx context.Context, tenant string, role Role) ([]GrantRecord, error)
	// RoleSubsetHeld reports whether every permission the role holds is within
	// held: the anti-escalation rule a caller applies before handing a role to
	// someone else. A grant the catalog cannot map to a declared permission is
	// one the held set cannot contain, so it refuses too. A non-nil error means
	// the grants could not be read: the judgement is unavailable, never a pass
	// or a refusal.
	RoleSubsetHeld(ctx context.Context, tenant string, role Role, held map[Permission]bool) (bool, error)
	// Bindings returns every binding of a tenant.
	Bindings(ctx context.Context, tenant string) ([]Binding, error)
	// BindingsOf returns every binding a subject holds within a tenant.
	BindingsOf(ctx context.Context, tenant, account string) ([]Binding, error)
	// BindingsOfRole returns one page of the bindings one role holds within a
	// tenant, in account order. after is an exclusive account cursor; a positive
	// limit caps the page, a non-positive limit returns every binding after the
	// cursor, and a role with no binding is an empty page.
	BindingsOfRole(ctx context.Context, tenant string, role Role, after string, limit int) ([]Binding, error)
	// GrantCounts returns how many grants each role holds within a tenant, from
	// one grouped read. A role holding no grant is absent from the map, which a
	// caller reads as zero.
	GrantCounts(ctx context.Context, tenant string) (map[Role]int, error)
	// PutRole creates or rewrites a role without the builtin mark. A record
	// carrying the mark is refused with ErrBuiltinRole.
	PutRole(ctx context.Context, r RoleRecord) error
	// MarkRoleBuiltin sets the builtin mark on an existing role. It is the only
	// write that sets the mark. An absent role is ErrUnknownRole; an
	// already-builtin role is no work.
	MarkRoleBuiltin(ctx context.Context, tenant string, role Role) error
	// DeleteRole removes a role. A builtin role cannot be deleted.
	DeleteRole(ctx context.Context, tenant string, role Role) error
	// PutGrant creates a grant on a role that exists in the tenant and is not
	// builtin.
	PutGrant(ctx context.Context, g GrantRecord) error
	// DeleteGrant removes a grant. A builtin role's grants are refused.
	DeleteGrant(ctx context.Context, g GrantRecord) error
	// PutBinding binds a role that exists in the tenant to a subject.
	PutBinding(ctx context.Context, b Binding) error
	// DeleteBinding removes a subject's binding to a role within a tenant. The
	// last tenant administrator of a domain is not removable. The whole removal
	// takes the host's TenantLock.
	//
	// The membership-removal cascade and the account-deletion cleanup must not
	// come through here: they clear bindings with ClearAccountBindings, which
	// takes no lock, because this call would re-enter the lock and deadlock.
	DeleteBinding(ctx context.Context, tenant, account string, role Role) error
	// ClearAccountBindings removes every binding the account holds within the
	// tenant in one statement, without taking the tenant lock. The
	// last-tenant-administrator rule does not apply. It is idempotent, and the
	// tenant's other bindings and the account's bindings in other tenants stay.
	ClearAccountBindings(ctx context.Context, tenant, account string) error
}

// MembershipChecker reports whether an account is an active member of a tenant.
// The host supplies it.
type MembershipChecker interface {
	IsMember(ctx context.Context, tenant string, account string) (bool, error)
}

// AccountIdentityReader resolves the accounts a role-usage read names: the
// display name to show and whether each account is still active.
//
// It is optional. An error means the answer could not be read, and the caller
// treats it as a 503 rather than as an empty or inactive set. The returned map
// is keyed by the account string the caller passed; an account the host does not
// know is absent from it.
type AccountIdentityReader interface {
	Identities(ctx context.Context, tenant string, accounts []string) (map[string]AccountIdentity, error)
}

// WriteGuard reports whether a tenant's policy data rejects a management write.
// The management module asks it before a role, grant or binding write, and the
// answer only narrows.
//
// The seam is optional. An answer that cannot be read is a 503 and is never read
// as permission.
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

// WriteSettings reports whether a tenant's settings reject a management write.
// The tenant settings management surface asks it before a settings write, and
// the answer only narrows.
//
// It is separate from WriteGuard: a setting is not policy data and borrows no
// WriteOp. An assembly that mounts the tenant settings surface must supply it;
// an absent one ends the startup.
type WriteSettings interface {
	// Denied reports whether the tenant rejects the settings write. An error
	// means the answer could not be read, and a caller treats it as a refusal
	// to proceed rather than as a pass.
	Denied(ctx context.Context, tenant string) (bool, error)
}

// Seeder is the host's seeding surface over the policy data: it adds the grants
// a role is declared to hold, and it is the one write that reaches a builtin
// role's grant set. The policy subpackage delivers it exclusively, beside
// Manager.
//
// A seed adds grants and never removes, rewrites or deletes one.
type Seeder interface {
	// SeedGrants adds grants to a role that exists in the tenant, whether or
	// not it is builtin. A grant the role already holds is no work and no
	// error, so a repeated seed over an unchanged declared set writes nothing
	// and reports zero; the result is how many grants this call added. An absent
	// role is ErrUnknownRole and an empty tenant is ErrTenantMismatch.
	SeedGrants(ctx context.Context, tenant string, role Role, grants []Grant) (int, error)
}

// Reconciler is the host's converging surface over the policy data: it brings a
// builtin tenant-admin role's grant set to a target set and removes from it
// only the permissions the caller lists as retired. The policy subpackage
// delivers it exclusively, beside Seeder.
//
// It applies to TenantAdminRole only and never to PlatformAdminRole. The write
// is one transaction: the missing grants are added, then the held grants named
// in retired are removed, then the snapshot is reloaded and
// authz.policy.changed is published. A revised permission that leaves the
// governed set without being listed in retired stays, so replicas cannot erase
// each other's grants. A ReconcileGrants call is retryable and idempotent.
type Reconciler interface {
	// ReconcileGrants converges the role's grants to grants, and removes the
	// grants named in retired that it currently holds. It returns how many rows
	// this call added and removed. A role other than a builtin TenantAdminRole is
	// ErrReservedRole before any row is written; an absent role is ErrUnknownRole
	// and an empty tenant ErrTenantMismatch.
	ReconcileGrants(ctx context.Context, tenant string, role Role, grants, retired []Grant) (added, removed int, err error)
}
