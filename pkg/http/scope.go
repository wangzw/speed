package http

import "strings"

// Scope is the management level a Spec publishes its paths under, not the access
// level. Its empty value is an ordinary interface, whose paths hang directly
// under the API root.
type Scope string

const (
	// ScopeTenants is tenant administration: operations decided in the tenant's own policy
	// domain, performed by a holder of the tenant's management permissions.
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

const tenantPathPrefix = "/{tenant}/"

const tenantPathRoot = "/{tenant}"

func (s Scope) scopeRoot() string { return string(s) }

// joinsPath reports whether path is addressed the way the scope requires.
// Only the tenants scope constrains the path: it must name the tenant first.
func (s Scope) joinsPath(path string) bool {
	if s != ScopeTenants {
		return true
	}
	return path == tenantPathRoot || strings.HasPrefix(path, tenantPathPrefix)
}
