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

// phase is the three states the route registration surface moves through; the
// middle one is the window Migrate opens and Start shuts.
type phase int

const (
	// phaseShut is before Migrate, when the registry has no instance yet.
	phaseShut phase = iota
	// phaseOpen is the window Migrate opens and Start shuts.
	phaseOpen
	// phaseSealed is Start onwards, when the tables are frozen.
	phaseSealed
)

type route struct {
	method string
	path   string
}

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

// registration is one call, kept as it was written; whether the set is well
// formed is judged once the window has shut.
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

// Exempt registers a route that the gateway lets through without a credential.
// The call is accepted in the window Migrate opens and Start shuts and panics
// outside it; what can only be judged then is judged in Start.
func (g *gateway) Exempt(endpoint, method, path string) {
	g.register(kindExempt, endpoint, method, path)
}

// Optional registers a route that accepts a request with no credential; the
// call window is Exempt's.
func (g *gateway) Optional(endpoint, method, path string) {
	g.register(kindOptional, endpoint, method, path)
}

// Impersonable registers a route that lets an impersonating identity through;
// the call window is Exempt's.
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
// guarded endpoint, in Start. It validates the named endpoint and each route's
// access levels, not the route, which this module cannot read.
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

// accessOf looks up the frozen table by the route gin matched, c.FullPath() plus
// method. An unmatched request reports no path and is refused here, so an
// empty-path registration is never hit by one.
func (g *gateway) accessOf(endpoint string, c *gin.Context) access {
	path := c.FullPath()
	if path == "" {
		return access{}
	}
	return g.routes[endpoint][route{method: c.Request.Method, path: path}]
}

// routesOf lists the endpoint's routes that carry the access, as method and
// path, in a stable order.
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

// sortedRoutes lists the endpoint's routes in ascending order of their rendering.
func sortedRoutes(table map[route]access) []route {
	routes := make([]route, 0, len(table))
	for r := range table {
		routes = append(routes, r)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].String() < routes[j].String() })
	return routes
}
