package http

import "github.com/gin-gonic/gin"

// RouteGuard wraps the handler of one route before that route is served. The
// returned handler may refuse the request instead of calling next and must not
// be nil; an assembly that requires the seam but delivers no guard refuses the
// registration.
type RouteGuard interface {
	// Guard wraps next with the guard's check.
	Guard(next gin.HandlerFunc) gin.HandlerFunc
}

// TenantAdminRouteGuard is the RouteGuard a tenant management surface wraps its
// routes with: it holds the route to the tenant the path names and additionally
// refuses the platform domain.
type TenantAdminRouteGuard interface {
	// GuardTenantAdmin wraps next with both checks.
	GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc
}
