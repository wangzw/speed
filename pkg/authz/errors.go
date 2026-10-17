package authz

import "errors"

// The failures this capability directory judges for itself. A caller tells them
// apart with errors.Is, and every wrap on the way out uses %w so the chain
// stays whole.
//
// The set is closed over what this capability directory produces: a newly
// judged class of failure either joins one of these or earns another. Errors
// that pass through from an external library — Casbin's matcher errors, gorm's
// store errors — are not among them.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a
	// layer on. The fix is to write the entry point module's name into this
	// command: a command that guards routes and has no entry point has nothing
	// to guard.
	ErrNoRouter = errors.New("authz: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports that an endpoint name is not one the entry
	// point declares, whether it was written into a configuration list or into
	// a route registration. The fix is to correct the name, or to declare that
	// endpoint in the entry point's configuration.
	ErrUnknownEndpoint = errors.New("authz: endpoint name is not one the entry point declares")

	// ErrUndeclaredRoute reports a route that carries no declaration at all —
	// neither a required permission nor an open registration — reached under
	// the closed default. The fix is to declare the route's permission, or to
	// register it open.
	ErrUndeclaredRoute = errors.New("authz: a guarded route declares no permission")

	// ErrUnknownPermission reports a route that declares a permission the
	// catalog does not hold. The fix is to register a declared permission, or
	// to add the permission to the policy subpackage.
	ErrUnknownPermission = errors.New("authz: a route declares a permission the catalog does not hold")

	// ErrUnknownRole reports a write whose role is not in the role table of the
	// tenant: a grant or a binding naming a role that was not created first.
	// The fix is to create the role first, or to use one that exists.
	ErrUnknownRole = errors.New("authz: the role does not exist in this tenant")

	// ErrBuiltinRole reports an attempt to delete a builtin role or to change
	// its permissions through Manager. The fix is to give the subject another
	// role: a builtin role's permissions are fixed against every write except
	// Seeder.SeedGrants, which only adds.
	ErrBuiltinRole = errors.New("authz: a builtin role cannot be changed or deleted")

	// ErrTenantMismatch reports a policy row whose tenant is not the tenant the
	// caller named: a write acts on one tenant alone, so a row drawn from
	// another is refused rather than written. The fix is at the call site that
	// assembled the row.
	ErrTenantMismatch = errors.New("authz: a policy row's tenant is not the caller's")

	// ErrPlatformDomainTaken reports that the roster holds a tenant whose
	// identifier is the reserved platform domain. The fix is to rename that
	// tenant: the platform domain is a reserved value.
	ErrPlatformDomainTaken = errors.New("authz: a tenant's identifier is the reserved platform domain")

	// ErrPolicyUnavailable reports that the policy snapshot, its store or the
	// startup self-check is not available. The fix is the dependency: it
	// recovers on its own once the dependency does, and the response does not
	// tell which one failed.
	ErrPolicyUnavailable = errors.New("authz: the policy snapshot or its store is not available")
)
