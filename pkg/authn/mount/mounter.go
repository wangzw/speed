package mount

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// level is the access level a registration asks the gateway to apply.
type level uint8

const (
	// levelGuarded is the route alone: the gateway authenticates it.
	levelGuarded level = iota
	// levelPublic exempts the route from authentication.
	levelPublic
	// levelOptional makes authentication optional on the route.
	levelOptional
)

// Public registers the route and exempts it from authentication.
func (p *product) Public(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	p.register("Public", levelPublic, []string{method}, rel, h, mount.Resolve(opts...), false)
}

// Optional registers the route and makes authentication optional on it.
func (p *product) Optional(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	p.register("Optional", levelOptional, []string{method}, rel, h, mount.Resolve(opts...), false)
}

// Guarded registers the route for the gateway to guard.
func (p *product) Guarded(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	p.register("Guarded", levelGuarded, []string{method}, rel, h, mount.Resolve(opts...), false)
}

// Callback registers a public route for each of methods and adds the bypass of
// the cross-origin check for them, on this module's own check alone.
func (p *product) Callback(methods []string, rel string, h gin.HandlerFunc) {
	p.register("Callback", levelPublic, methods, rel, h, mount.Spec{}, true)
}

// BaseIn is the base path routes of the given scope are joined to.
func (p *product) BaseIn(scope speedhttp.Scope) string { return p.bases[scope] }

// Endpoint is the endpoint the ordinary scope is mounted on. It is settled in
// Init, and empty before it. A handler's own 401 names it as its realm, the
// protection space being the ordinary authentication surface.
func (p *product) Endpoint() string {
	if w := p.wired.Load(); w != nil {
		return w.defaultName
	}
	return ""
}

// PublicURL is the origin the deployment is reached at from outside, in the form
// a browser sends it, and empty when none is configured.
func (p *product) PublicURL() string { return p.publicURL }

// CSRF is the cross-origin check every registration passes through.
func (p *product) CSRF() *http.CrossOriginProtection { return p.csrf }

// register writes one route and its access level, for each of methods.
//
// The route goes to the entry point first: it is what checks the path's form and
// the handler, so a typo is refused before the gateway has been told anything
// about it. The access level then goes to the gateway under the same endpoint,
// method and path, which is the whole of what makes the pair one registration.
// A registration outside the window either surface accepts one in panics, with
// the route named in front of the surface's own text.
//
// The endpoint is the one configured for the route's scope. No scope falls back
// to another except the platform-management scope, which shares the ordinary
// one: a tenant-scoped route on the ordinary endpoint would be decided on a
// chain no tenant layer guards, so the missing key is refused here at its call
// site rather than mounted somewhere it does not belong.
func (p *product) register(call string, lvl level, methods []string, rel string, h gin.HandlerFunc, spec mount.Spec, callback bool) {
	w := p.wired.Load()
	if w == nil {
		panic(fmt.Sprintf("%s: %s(%s, %q) was called before this module's Init ran. Declare the "+
			"requirement on the route-mounting capability and register from the Init callback, "+
			"which runs after this module's", ModuleName, call, strings.Join(methods, ", "), rel))
	}
	// The access level does not imply a scope: a public route and a guarded one
	// of the same module share one base unless the declaration says otherwise.
	scope := spec.Scope
	base := p.bases[scope]
	if base == "" {
		panic(fmt.Sprintf("%s: %s(%s, %q) was registered for the scope %q, which has no configured "+
			"base path", ModuleName, call, strings.Join(methods, ", "), rel, scope))
	}
	endpoint, mounted := w.endpoints[scope]
	if !mounted {
		panic(fmt.Sprintf("%s: %s(%s, %q) was registered for the scope %q, which has no configured "+
			"endpoint. A route of that scope is decided in the tenant's policy domain, so its "+
			"endpoint has to be covered by the tenant resolution and admission layers; write %s",
			ModuleName, call, strings.Join(methods, ", "), rel, scope, key(tenantsMountItem)))
	}
	p.checkCall(call, methods, rel, h, base)

	path := base + rel
	// A tenant-scoped route is addressed by a tenant segment that has to name the
	// tenant the request resolved to. The guard that checks it is applied here, to
	// every route of the group, so a group cannot be mounted without it: the
	// mounting module never names the tenant directory, and the registration is
	// the one place the scope and the routes are both in hand.
	handler := h
	if scope == speedhttp.ScopeTenants {
		if w.routeGuard == nil {
			panic(fmt.Sprintf("%s: %s(%s, %q) was registered for the scope %q, which is "+
				"addressed by a tenant segment and must be held to the tenant the request resolved "+
				"to, and no module delivers the path-tenant guard. Write the tenant resolving "+
				"module's name into this command", ModuleName, call, strings.Join(methods, ", "), rel, scope))
		}
		handler = w.routeGuard.Guard(h)
	}
	chain := mount.Wrap(p.csrf, endpoint.name, handler)
	if callback {
		registering(call, strings.Join(methods, ", "), path, func() { mount.AllowCallback(p.csrf, methods, path) })
	}
	for _, method := range methods {
		registering(call, method, path, func() {
			endpoint.endpoint.Route(method, path, chain)
			switch lvl {
			case levelPublic:
				w.gate.Exempt(endpoint.name, method, path)
			case levelOptional:
				w.gate.Optional(endpoint.name, method, path)
			case levelGuarded:
				// The gateway guards every route it has not been told otherwise about.
			}
			if spec.Impersonable {
				w.gate.Impersonable(endpoint.name, method, path)
			}
		})
	}
}

// registering runs one write to a registration surface and, when the surface
// refuses it by panicking, panics again with the route named. The entry point's
// panic for a call outside its window names the endpoint and the call and not the
// route, and the route is what finds the call site among the many a module makes.
func registering(call, method, path string, write func()) {
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Sprintf("%s: %s of %s %s was refused: %v", ModuleName, call, method, path, r))
		}
	}()
	write()
}

// checkCall refuses a registration that cannot mean what it says, naming the
// route: a relative path that is not joined to the base with a slash would mount
// a path nobody wrote, a handler that is nil would crash on the first request,
// and a callback with no method or one written twice registers nothing or
// conflicts with itself.
func (p *product) checkCall(call string, methods []string, rel string, h gin.HandlerFunc, base string) {
	refuse := func(why string) {
		panic(fmt.Sprintf("%s: %s(%s, %q) %s", ModuleName, call, strings.Join(methods, ", "), rel, why))
	}
	if !strings.HasPrefix(rel, "/") {
		refuse("was given a path that does not begin with a slash, so it would not be joined to the " +
			"base path " + base + " as written")
	}
	if h == nil {
		refuse("was given a nil handler, which would crash when a request reaches the route")
	}
	if len(methods) == 0 {
		refuse("was given no method, so it would register no route")
	}
	for i, m := range methods {
		if m == "" {
			refuse("was given an empty method")
		}
		if slices.Contains(methods[:i], m) {
			refuse("was given the method " + m + " twice")
		}
	}
}
