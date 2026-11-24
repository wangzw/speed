// Package mount is the interface through which a module mounts a route with its
// access level, and the options that qualify one registration. It is internal to
// pkg/authn and reached only through the modules that mount routes.
package mount

import (
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// DefaultBasePath is the base path the capability's ordinary routes mount under
// without a configured one: the self-service and sign-in surface, which carries
// no scope segment.
const DefaultBasePath = "/api/v1/auth"

// HumanProofMaxBytes is the longest human-verification proof a send request may
// carry in its human_proof field. A surface refuses a longer value as a bad
// request before it asks a provider, so an unbounded field never reaches one.
const HumanProofMaxBytes = 8 << 10

// The defaults of the management scopes: each scope's base path, derived from
// the same API root and scope segment, with the system base at the scope root so
// it contains every path the scope publishes.
const (
	DefaultTenantsBasePath = "/api/v1/tenants"
	DefaultSystemBasePath  = "/api/v1/system"
)

// DefaultBaseFor answers the default base path of one scope. The empty scope
// is the ordinary surface.
func DefaultBaseFor(scope speedhttp.Scope) string {
	switch scope {
	case speedhttp.ScopeTenants:
		return DefaultTenantsBasePath
	case speedhttp.ScopeSystem:
		return DefaultSystemBasePath
	default:
		return DefaultBasePath
	}
}

// Mounter registers routes under the capability's base path with the access
// level the gateway applies; every registration carries Cache-Control: no-store
// and, unless a Callback, the cross-origin check, and a failed one panics like Route.
type Mounter interface {
	// Public registers the route and exempts it from authentication.
	Public(method, rel string, h gin.HandlerFunc, opts ...Option)
	// Optional registers the route and makes authentication optional on it:
	// a request without a credential passes with no identity, one with a
	// credential that does not hold is still refused.
	Optional(method, rel string, h gin.HandlerFunc, opts ...Option)
	// Guarded registers the route only; the gateway guards it.
	Guarded(method, rel string, h gin.HandlerFunc, opts ...Option)
	// Callback registers a public route for each of methods that skips the
	// cross-origin check, for the redirect an external party sends the
	// browser back with from another site.
	Callback(methods []string, rel string, h gin.HandlerFunc)
	// BaseIn is the base path routes of the given scope are joined to.
	BaseIn(scope speedhttp.Scope) string
	// Endpoint is the name of the endpoint the ordinary scope's routes are
	// mounted on; a handler that answers 401 itself writes it as its challenge's realm.
	Endpoint() string
	// PublicURL is the origin the deployment is reached at from outside,
	// without a path.
	PublicURL() string
	// CSRF is the cross-origin check the wrapper applies, for a handler that
	// has to apply it to something that is not a route.
	CSRF() *http.CrossOriginProtection
}

// Declared registers the route an operation declares at the access level the
// declaration names, so a route cannot be mounted at a level its operation does
// not say. Callback is registered separately.
func Declared(m Mounter, operation speedhttp.Declared, rel string, h gin.HandlerFunc, opts ...Option) {
	descriptor := operation.Descriptor()
	switch descriptor.Access {
	case speedhttp.AccessPublic:
		m.Public(descriptor.Method, rel, h, opts...)
	case speedhttp.AccessOptional:
		m.Optional(descriptor.Method, rel, h, opts...)
	default:
		m.Guarded(descriptor.Method, rel, h, opts...)
	}
}

// Option qualifies one registration.
type Option func(*routeSpec)

type routeSpec struct {
	impersonable bool
	scope        speedhttp.Scope
}

// Spec is what the options of one registration add up to, as the implementation
// of Mounter reads it. It is the exported form of the unexported accumulator the
// options write to, which a package other than this one could not otherwise see.
type Spec struct {
	// Impersonable is whether the route is one an impersonating identity may
	// pass, registered through the gateway's Impersonable.
	Impersonable bool
	// Scope is the management level the route is published under. Empty is
	// the ordinary surface: the self-service and sign-in routes, which share
	// one base regardless of their access level.
	Scope speedhttp.Scope
}

// InScope publishes the route under the given scope's base path. A module
// whose routes span levels, such as the administration module serving system
// and tenant surfaces, passes it on the routes of every management scope.
func InScope(scope speedhttp.Scope) Option {
	return func(s *routeSpec) { s.scope = scope }
}

// Impersonable lets an impersonating identity through the route. It is for the
// few self-service routes of the capability that must stay reachable while an
// identity is being impersonated, such as the one that ends the impersonation.
func Impersonable() Option {
	return func(s *routeSpec) { s.impersonable = true }
}

// Resolve is what opts add up to. A nil option does nothing, and no option
// leaves every field at its zero value, which reads as the plain route.
func Resolve(opts ...Option) Spec {
	var s routeSpec
	for _, o := range opts {
		if o != nil {
			o(&s)
		}
	}
	return Spec{Impersonable: s.impersonable, Scope: s.scope}
}
