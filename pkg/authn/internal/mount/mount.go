// Package mount is the interface through which a module of the authentication
// capability mounts a route, and the options that qualify one registration.
//
// The implementation is the module in pkg/authn/mount, which registers the route
// and its access level in one call: a route and the exemption that lets it
// through the gateway are never written separately, so there is no route that
// was mounted and left unexempted, and no exemption that names a path with a typo
// in it.
//
// Two rules are read by that module and by the gateway both, and live here so
// that there is one reading of each: which endpoint the routes are mounted on
// (Endpoint) and which origins the cross-origin check trusts (TrustedOrigins,
// CrossOrigin). The gateway cannot hand its own check to the Mounter, because a
// host may supply a Gate of its own, so each builds an instance from the same
// rule.
//
// The package also holds what every handler mounted through it shares, so that
// each module writes none of it again: the closed set of problem codes and the
// writers that answer with them (Fail, Unauthorized, StepUpRequired, Forbidden,
// Throttled, Unavailable, Busy), the response-header writers a typed handler
// uses for a header its answer body does not express (SetResponseHeader,
// AddResponseHeader), the challenge a typed handler answers 401 with
// (UnauthorizedChallenge), the request body decoder
// (Decode), the wrapper every registration goes through (Wrap) and the bypass of
// the cross-origin check for a callback (AllowCallback). The mounttest package
// is a Mounter for the tests of those modules.
//
// The package is internal to pkg/authn. The modules that mount routes require
// the capability by this interface, and a host reaches it only through the
// modules, never by the type.
package mount

import (
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// DefaultBasePath is the base path the capability's ordinary routes are
// mounted under without a configured one: the self-service and sign-in
// surface, which carries no scope segment. The gateway declares it as the
// default of mount.bases.default and the module that mounts routes reads it
// back the same way; it is written once here so a host-supplied gateway and
// the module cannot come to disagree on where the routes are.
const DefaultBasePath = "/api/v1/auth"

// HumanProofMaxBytes is the longest human-verification proof a send request may
// carry in its human_proof field. A surface refuses a longer value as a bad
// request before it asks a provider, so an unbounded field never reaches one.
const HumanProofMaxBytes = 8 << 10

// The defaults of the management scopes. Each is derived from the same API
// root and the scope segment, so a deployment that changes none of them gets
// the path taxonomy the repository publishes.
const (
	DefaultTenantsBasePath = "/api/v1/tenants"
	DefaultSystemBasePath  = "/api/v1/system/auth"
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

// Mounter registers routes under the capability's base path together with the
// access level the gateway is to apply to each.
//
// Every registration goes through one wrapper: the response carries
// Cache-Control: no-store, and a POST, PUT, PATCH or DELETE that is not a
// Callback passes the cross-origin check first and is refused with 401 when it
// does not. The path is Base() + rel, and a registration that fails (a route
// conflict, a call outside the registration window) panics in the way the
// router's own Route does.
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
	// mounted on. A handler that answers 401 itself writes it as the realm of
	// its challenge, the name of the protection space being the endpoint, as
	// the gateway's own is.
	Endpoint() string
	// PublicURL is the origin the deployment is reached at from outside,
	// without a path.
	PublicURL() string
	// CSRF is the cross-origin check the wrapper applies, for a handler that
	// has to apply it to something that is not a route.
	CSRF() *http.CrossOriginProtection
}

// Declared registers the route an operation declares, at the access level the
// declaration names. The registration and the declaration are then one source:
// a route cannot be mounted public while its operation says it is guarded, and
// the fragment cannot publish a requirement the gateway does not apply.
//
// Callback is registered separately, because a callback is both public and
// exempt from the cross-origin check, and only its own registrant knows which
// methods arrive from another site.
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

// routeSpec is what the options of one registration add up to.
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
