package gate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// phase is the three states the route registration surface moves through. The
// window in the middle opens in Migrate and shuts in Start: a stage advances one
// module at a time, so it contains the Init stage whole and reaches into the
// rest of the Migrate stage and the Start callbacks ordered ahead of this
// module.
type phase int

const (
	// phaseShut is before Migrate. A write here comes from a module
	// constructing, which is too early for the registry to have every
	// instance.
	phaseShut phase = iota
	// phaseOpen is the window Migrate opens and Start shuts: the Init stage
	// whole, plus the rest of the Migrate stage and the Start callbacks
	// ordered ahead of this module.
	phaseOpen
	// phaseSealed is Start onwards. The tables are frozen and requests read
	// them without a lock, so a later write would change what is guarded while
	// traffic is being served.
	phaseSealed
)

type route struct {
	method string
	path   string
}

// String renders the pair the way a diagnostic names a route.
func (r route) String() string { return r.method + " " + r.path }

type kind uint8

const (
	kindExempt kind = iota
	kindOptional
	kindImpersonable
)

// String is the name of the Gate method that makes the registration.
func (k kind) String() string {
	switch k {
	case kindExempt:
		return "Exempt"
	case kindOptional:
		return "Optional"
	case kindImpersonable:
		return "Impersonable"
	}
	return "unknown"
}

// registration is one call, kept as it was written. Nothing is judged at
// registration time: the registrants run in dependency order through the open
// window, and whether the set is well formed can only be asked once the window
// has shut.
type registration struct {
	kind     kind
	endpoint string
	route    route
}

type access struct {
	exempt       bool
	optional     bool
	impersonable bool
}

// Exempt registers a route that this gateway lets through without a credential.
//
// It returns nothing, for the reason the entry point's own registration surface
// does: the only way a registration can fail is a programming error at the call
// site, and handing back an error a registrant is free to drop would leave a
// route believed exempt and in fact guarded — or, worse, the other way round.
// What can only be judged once the window has shut is judged in Start and ends
// the startup there.
//
// A write outside the registration window panics. The window opens in this
// module's Migrate callback and shuts in its Start callback, so it contains the
// whole Init stage. It is not a configuration mistake but a call from the wrong
// stage, and the two ends of the window are stages of the drive, not anything a
// caller can retry.
func (g *gateway) Exempt(endpoint, method, path string) {
	g.register(kindExempt, endpoint, method, path)
}

// Optional registers a route that accepts a request with no credential. See
// Exempt for when the call may be made.
func (g *gateway) Optional(endpoint, method, path string) {
	g.register(kindOptional, endpoint, method, path)
}

// Impersonable registers a route that lets an impersonating identity through.
// See Exempt for when the call may be made.
func (g *gateway) Impersonable(endpoint, method, path string) {
	g.register(kindImpersonable, endpoint, method, path)
}

// register records one registration, or panics outside the window.
func (g *gateway) register(k kind, endpoint, method, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch g.phase {
	case phaseShut:
		panic(fmt.Sprintf("%s: %s(%q, %q, %q) was called before the registration gate "+
			"opened. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. Move the call "+
			"into the registering module's Init callback: every instance exists by then, "+
			"which is what makes it the stage modules wire each other up in",
			ModuleName, k, endpoint, method, path))
	case phaseSealed:
		panic(fmt.Sprintf("%s: %s(%q, %q, %q) was called after the registration gate "+
			"closed. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. The tables "+
			"are frozen in Start and requests read them from then on, so this "+
			"registration would change what is guarded while traffic is being served. "+
			"Move the call into the registering module's Init callback",
			ModuleName, k, endpoint, method, path))
	}
	g.declared = append(g.declared, registration{
		kind: k, endpoint: endpoint, route: route{method: method, path: path},
	})
}

func (g *gateway) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.phase = phaseOpen
}

// seal shuts the gate, validates what was registered and freezes one table per
// guarded endpoint. It runs in Start.
//
// The gate is shut before anything is judged, so that a registration racing the
// validation lands on the closed side rather than in a table already being
// read.
//
// What is validated is the endpoint each registration names and the access
// levels one route is given. The route is not: this module cannot read the entry
// point's routing table, so it cannot tell a route that was registered from one
// that was not, and a pair no route carries is a pair that never matches.
func (g *gateway) seal(router speedhttp.Router) error {
	g.mu.Lock()
	g.phase = phaseSealed
	declared := g.declared
	g.mu.Unlock()

	frozen := make(map[string]map[route]access)
	for _, r := range declared {
		if _, err := router.Endpoint(r.endpoint); err != nil {
			if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
				return fmt.Errorf("%w: the %s registration of %s names the endpoint %q, which the "+
					"entry point does not have. Correct the name, or declare that endpoint in "+
					"the entry point's configuration",
					authn.ErrUnknownEndpoint, r.kind, r.route, r.endpoint)
			}
			return fmt.Errorf("%s: checking the endpoint %q named by the %s registration of %s: %w",
				ModuleName, r.endpoint, r.kind, r.route, err)
		}
		if !g.guards(r.endpoint) {
			return fmt.Errorf("%w: the %s registration of %s lands on the endpoint %q, which this "+
				"gateway does not guard (it guards %s). A registration on an unguarded "+
				"endpoint changes nothing and would be swallowed without a signal. Write "+
				"that endpoint into %s.%s, or move the registration",
				authn.ErrExemptionUnguarded, r.kind, r.route, r.endpoint,
				describeGuarded(g.guardedNames()), ConfigNamespace, endpointsItem)
		}
		// The same registration made twice on one route is one registration:
		// two modules asking for a route to be public are asking for the same
		// thing.
		if frozen[r.endpoint] == nil {
			frozen[r.endpoint] = make(map[route]access)
		}
		a := frozen[r.endpoint][r.route]
		switch r.kind {
		case kindExempt:
			a.exempt = true
		case kindOptional:
			a.optional = true
		case kindImpersonable:
			a.impersonable = true
		}
		frozen[r.endpoint][r.route] = a
	}

	// A route that carries no credential cannot be one that is still being
	// authenticated, and cannot let an impersonating identity through: an
	// exempt route reads no credential, so it never has an identity to be
	// impersonating. Optional and Impersonable do not conflict, they act on
	// requests that do not overlap.
	for _, endpoint := range sortedKeys(frozen) {
		for _, r := range sortedRoutes(frozen[endpoint]) {
			a := frozen[endpoint][r]
			if a.exempt && (a.optional || a.impersonable) {
				other := kindOptional
				if !a.optional {
					other = kindImpersonable
				}
				return fmt.Errorf("%w: the route %s on the endpoint %q is registered both %s and %s. "+
					"An exempt route reads no credential, so it can be neither one that accepts "+
					"a request without one nor one that admits an impersonating identity. Drop "+
					"one of the two", authn.ErrAccessConflict, r, endpoint, kindExempt, other)
			}
		}
	}
	g.routes = frozen
	return nil
}

func describeGuarded(endpoints []string) string {
	if len(endpoints) == 0 {
		return "no endpoint at all"
	}
	return strings.Join(endpoints, ", ")
}

// accessOf looks up what the frozen tables hold for the route a request matched.
//
// The request is compared by the route the entry point matched it to, which gin
// reports in the spelling the route was registered under, and by its method. A
// request that matched no route reports no path, so it is no route's request:
// the empty path is refused here rather than looked up, because a registration
// made with an empty path would otherwise be the one that every unmatched
// request hits.
func (g *gateway) accessOf(endpoint string, c *gin.Context) access {
	path := c.FullPath()
	if path == "" {
		return access{}
	}
	return g.routes[endpoint][route{method: c.Request.Method, path: path}]
}

// routesOf lists the routes of one endpoint's table that carry the access, in a
// stable order, rendered as method and path.
func routesOf(table map[route]access, has func(access) bool) []string {
	var out []string
	for r, a := range table {
		if has(a) {
			out = append(out, r.String())
		}
	}
	sort.Strings(out)
	return out
}

// sortedKeys lists the endpoint names of a frozen table in ascending order.
func sortedKeys(table map[string]map[route]access) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sortedRoutes lists the routes of one endpoint's table in ascending order of
// their rendering.
func sortedRoutes(table map[route]access) []route {
	routes := make([]route, 0, len(table))
	for r := range table {
		routes = append(routes, r)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].String() < routes[j].String() })
	return routes
}
