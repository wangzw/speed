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

// phase is the three states the exemption registration surface moves through.
// The window in the middle opens in Migrate and shuts in Start: a stage
// advances one module at a time, so it contains the Init stage whole and
// reaches into the rest of the Migrate stage and the Start callbacks ordered
// ahead of this module.
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
	// phaseSealed is Start onwards. The table is frozen and requests read it
	// without a lock, so a later write would change what is guarded while
	// traffic is being served.
	phaseSealed
)

// route is the pair a registrant passes to Endpoint.Route: the method and the
// path in gin's spelling. An exemption names one, and a request is compared
// with it as it is, character for character.
type route struct {
	method string
	path   string
}

// String renders the pair the way a diagnostic names a route.
func (r route) String() string { return r.method + " " + r.path }

// exemption is one registration, kept as it was written. Nothing is judged at
// registration time: the registrants run in dependency order through the open
// window, and whether the set is well formed can only be asked once the window
// has shut.
type exemption struct {
	endpoint string
	route    route
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
	g.mu.Lock()
	defer g.mu.Unlock()

	switch g.phase {
	case phaseShut:
		panic(fmt.Sprintf("%s: Exempt(%q, %q, %q) was called before the registration gate "+
			"opened. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. Move the call "+
			"into the registering module's Init callback: every instance exists by then, "+
			"which is what makes it the stage modules wire each other up in",
			ModuleName, endpoint, method, path))
	case phaseSealed:
		panic(fmt.Sprintf("%s: Exempt(%q, %q, %q) was called after the registration gate "+
			"closed. The surface accepts writes from this module's Migrate callback until "+
			"its Start callback, a window that contains the whole Init stage. The exemption "+
			"table is frozen in Start and requests read it from then on, so this "+
			"registration would change what is guarded while traffic is being served. "+
			"Move the call into the registering module's Init callback",
			ModuleName, endpoint, method, path))
	}
	g.declared = append(g.declared, exemption{endpoint: endpoint, route: route{method: method, path: path}})
}

// open lifts the gate. It runs in Migrate.
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
// What is validated is the endpoint each exemption names. The route is not: this
// module cannot read the entry point's routing table, so it cannot tell a route
// that was registered from one that was not, and a pair no route carries is a
// pair that never matches.
func (g *gateway) seal(router speedhttp.Router) error {
	g.mu.Lock()
	g.phase = phaseSealed
	declared := g.declared
	g.mu.Unlock()

	guarded := make(map[string]struct{}, len(g.settings.endpoints))
	for _, name := range g.settings.endpoints {
		guarded[name] = struct{}{}
	}

	frozen := make(map[string]map[route]struct{})
	for _, e := range declared {
		if _, err := router.Endpoint(e.endpoint); err != nil {
			if errors.Is(err, speedhttp.ErrUnknownEndpoint) {
				return fmt.Errorf("%w: the exemption %s names the endpoint %q, which the entry "+
					"point does not have. Correct the name, or declare that endpoint in "+
					"the entry point's configuration",
					authn.ErrUnknownEndpoint, e.route, e.endpoint)
			}
			return fmt.Errorf("%s: checking the endpoint %q named by the exemption %s: %w",
				ModuleName, e.endpoint, e.route, err)
		}
		if _, ok := guarded[e.endpoint]; !ok {
			return fmt.Errorf("%w: the exemption %s lands on the endpoint %q, which this gateway "+
				"does not guard (it guards %s). An exemption on an unguarded endpoint "+
				"changes nothing and would be swallowed without a signal. Write that "+
				"endpoint into %s.%s, or move the exemption",
				authn.ErrExemptionUnguarded, e.route, e.endpoint,
				describeGuarded(g.settings.endpoints), ConfigNamespace, endpointsItem)
		}
		// The same route registered twice on one endpoint is one exemption:
		// two modules asking for a route to be public are asking for the
		// same thing.
		if frozen[e.endpoint] == nil {
			frozen[e.endpoint] = make(map[route]struct{})
		}
		frozen[e.endpoint][e.route] = struct{}{}
	}
	g.exempted = frozen
	return nil
}

// describeGuarded renders the guarded set the way a diagnostic names it.
func describeGuarded(endpoints []string) string {
	if len(endpoints) == 0 {
		return "no endpoint at all"
	}
	return strings.Join(endpoints, ", ")
}

// exempts reports whether this request is on an exempt route of the endpoint.
// The question is asked before the Authorization header is read, so a request on
// an exempt route reaches no backend at all.
//
// The request is compared by the route the entry point matched it to, which
// gin reports in the spelling the route was registered under, and by its
// method. A request that matched no route reports no path, so it is no route's
// request: the empty path is refused here rather than looked up, because an
// exemption registered with an empty path would otherwise be the one that
// every unmatched request hits.
func (g *gateway) exempts(endpoint string, c *gin.Context) bool {
	path := c.FullPath()
	if path == "" {
		return false
	}
	_, hit := g.exempted[endpoint][route{method: c.Request.Method, path: path}]
	return hit
}

// routesOf lists the routes of one endpoint's table in a stable order, rendered
// as method and path.
func routesOf(table map[route]struct{}) []string {
	out := make([]string, 0, len(table))
	for r := range table {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return out
}
