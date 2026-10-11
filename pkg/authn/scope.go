package authn

// ScopeGuard is delivered by the tenant admission layer, exclusively, and says
// which endpoints that layer guards.
//
// The gateway reads it for two decisions. An identity that is impersonating,
// one whose Actor differs from its Account, is admitted only to a route
// registered with Gate.Impersonable and to an endpoint the guard covers: the
// tenant an impersonation is confined to is checked on the endpoints the
// admission layer guards and nowhere else, so an impersonating identity let
// into any other route would have no confinement at all. And the endpoint the
// authentication routes are mounted on may not be one the guard covers, which
// the gateway refuses to start on (ErrInvalidConfig).
//
// The guard is optional for the gateway. In an assembly without the admission
// layer the gateway admits an impersonating identity to the routes registered
// Impersonable alone.
type ScopeGuard interface {
	// Covers reports whether the admission layer guards the named endpoint.
	Covers(endpoint string) bool
}
