package http

import "github.com/gin-gonic/gin"

// RouteGuard wraps the handler of one route before that route is served. It is
// the seam a mounting module uses when a route group has to be held to a fact
// the entry point cannot know: the module that mounts the group applies the
// guard to every route of it, and the module that knows the fact delivers the
// guard as a capability.
//
// It is a route-level seam rather than an endpoint-level layer because the
// routes that need it share an endpoint with routes that must not get it. The
// tenant scope is the case it exists for: a tenant-addressed route is held to
// the tenant the request resolved to, while a platform route that merely names
// a tenant in its path is not, even when both hang off one endpoint.
//
// Guard receives the handler the route would serve and returns the one it
// serves. The returned handler may refuse the request instead of calling next;
// it must not be nil. An assembly that mounts a route group requiring the seam
// but delivers no guard for it refuses the registration rather than serving the
// group unguarded.
type RouteGuard interface {
	// Guard wraps next with the guard's check.
	Guard(next gin.HandlerFunc) gin.HandlerFunc
}

// TenantAdminRouteGuard is the RouteGuard a tenant management surface wraps its
// routes with. It holds a route to the identity rule RouteGuard states — the
// tenant the path names is the tenant the request resolved to — and
// additionally refuses a request whose tenant is the platform domain: tenant
// administration acts on a tenant, and the platform domain is not one.
//
// It is a capability of its own rather than an option on RouteGuard because the
// refusal is the tenant management surfaces' alone. A tenant-scoped surface
// that manages the system tenant itself — the members, invitations and SSO
// groups do — is addressed by the platform domain legitimately, and the
// identity rule is the whole of what it needs. That is why the mounting module
// applies RouteGuard to every tenant-scoped group and only the tenant
// management modules apply this one.
type TenantAdminRouteGuard interface {
	// GuardTenantAdmin wraps next with both checks.
	GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc
}
