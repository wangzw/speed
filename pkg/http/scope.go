package http

import "strings"

// Scope is the authorization level a Spec publishes its paths under. A Spec
// with no scope serves the ordinary surface — the caller's own resources and
// the unauthenticated sign-in routes — directly under the API root. The two
// declared scopes put a segment on the root: tenant administration under
// tenants, platform administration under system.
//
// The scope segment is what a reader uses to tell a management operation from
// an ordinary one without consulting the module that declared it; it is not
// the access level (that remains the route's gateway registration).
type Scope string

const (
	// ScopeTenants is tenant administration: operations decided in the
	// tenant's own policy domain, performed by a holder of the tenant's
	// management permissions. Every path under it names the tenant first.
	ScopeTenants Scope = "tenants"
	// ScopeSystem is platform administration: operations decided in the
	// platform domain, performed by platform staff. A tenant may still appear
	// in a path here as the data the operation acts on, never as the level.
	ScopeSystem Scope = "system"
)

// Valid reports whether s is one of the declared scopes.
func (s Scope) Valid() bool {
	switch s {
	case ScopeTenants, ScopeSystem:
		return true
	}
	return false
}

// tenantPathPrefix is the leading segment every path of a tenants-scope
// fragment carries: the tenant is part of the address, not a query value, so
// the level and the tenant the operation acts on are both on the request line.
const tenantPathPrefix = "/{tenant}/"

// scopeRoot is the segment a scope contributes to the API root. The ordinary
// surface has no value here.
func (s Scope) scopeRoot() string { return string(s) }

// joinsPath reports whether path is addressed the way the scope requires.
// Only the tenants scope constrains the path: it must name the tenant first.
func (s Scope) joinsPath(path string) bool {
	if s != ScopeTenants {
		return true
	}
	return strings.HasPrefix(path, tenantPathPrefix)
}
