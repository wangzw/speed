package authz

import "errors"

// The failures this capability directory judges for itself. A caller tells them
// apart with errors.Is, and every wrap on the way out uses %w so the chain
// stays whole.
var (
	// ErrNoRouter reports that Init found no HTTP entry point to register a
	// layer on.
	ErrNoRouter = errors.New("authz: no HTTP entry point in this command")

	// ErrUnknownEndpoint reports that an endpoint name is not one the entry
	// point declares.
	ErrUnknownEndpoint = errors.New("authz: endpoint name is not one the entry point declares")

	// ErrUndeclaredRoute reports a route that carries no declaration at all,
	// reached under the closed default.
	ErrUndeclaredRoute = errors.New("authz: a guarded route declares no permission")

	// ErrUnknownPermission reports a route that declares a permission the
	// catalog does not hold.
	ErrUnknownPermission = errors.New("authz: a route declares a permission the catalog does not hold")

	// ErrUnknownRole reports a write whose role is not in the role table of the
	// tenant.
	ErrUnknownRole = errors.New("authz: the role does not exist in this tenant")

	// ErrBuiltinRole reports an attempt to delete a builtin role or to change
	// its permissions through Manager.
	ErrBuiltinRole = errors.New("authz: a builtin role cannot be changed or deleted")

	// ErrTenantMismatch reports a policy row whose tenant is not the tenant the
	// caller named.
	ErrTenantMismatch = errors.New("authz: a policy row's tenant is not the caller's")

	// ErrPlatformDomainMissing reports that the roster holds no tenant whose
	// identifier is the platform domain.
	ErrPlatformDomainMissing = errors.New("authz: the roster holds no tenant carrying the platform domain")

	// ErrReservedRole reports a role reserved to one domain used in another:
	// PlatformAdminRole exists only in the platform domain. Reconciler also
	// reports it for a target other than a builtin TenantAdminRole.
	ErrReservedRole = errors.New("authz: a role reserved to the platform domain was used in another domain")

	// ErrLastTenantAdmin reports a delete that would leave a domain with no
	// tenant administrator.
	ErrLastTenantAdmin = errors.New("authz: the last tenant administrator binding of a domain cannot be removed")

	// ErrMissingMembershipChecker reports a write binding the platform
	// administrator role while the assembly supplied no MembershipChecker.
	ErrMissingMembershipChecker = errors.New("authz: no membership checker is assembled to bind the platform administrator role")

	// ErrPlatformMembershipRequired reports that the account a platform
	// administrator binding names is not an active member of the platform
	// tenant.
	ErrPlatformMembershipRequired = errors.New("authz: the account is not an active member of the platform tenant")

	// ErrPolicyUnavailable reports that the policy snapshot, its store or the
	// startup self-check is not available.
	ErrPolicyUnavailable = errors.New("authz: the policy snapshot or its store is not available")
)
