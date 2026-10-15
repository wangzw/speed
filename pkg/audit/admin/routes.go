package admin

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The query routes, relative to the fixed base path. They are mounted on their
// own base path and not under a configurable one, and each is addressed by a
// natural key — a row identifier — so no opaque cursor ever appears in a path.
const (
	basePath = "/audit/admin"

	pathRecords = basePath + "/records"
	pathRecord  = basePath + "/records/:id"
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group, and it is always mounted, because
// the read surface it reads through is required.
var managementGroups = []string{"records"}

// realmKey is where the name of the endpoint a query route was mounted on is put
// for the handler to read back. A handler that answers 401 itself writes the
// endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the routes are mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "audit.admin.realm"

// withRealm wraps a query handler so the endpoint it was mounted on reaches it
// as the realm of its 401. The same handler is mounted on every configured
// endpoint, so the realm is bound per endpoint here rather than held by the
// handler.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one query route: the method and path it answers and the handler that
// serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// routes is the closed set of query routes, in the order they are mounted. Every
// route reads and none exports: the export surface is another permission and
// another module's to carry.
func (p *product) routes() []route {
	return []route{
		{http.MethodGet, pathRecords, p.handleRecordList},
		{http.MethodGet, pathRecord, p.handleRecordGet},
	}
}

// mount resolves every configured endpoint on the entry point and registers the
// query routes on each. Every name is looked up before any route is registered,
// so a name the entry point does not declare fails the startup naming that
// endpoint rather than leaving a partial mounting behind.
func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.cfg.endpoints))
	for _, name := range p.cfg.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, withRealm(tgt.name, r.handler))
		}
	}
	return nil
}

// declareOpen records every query route as needing no permission on the
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the handler: the handler makes the decision, on the permission
// audit.read, through the handler-internal guard. Declaring them to require a
// permission would make the middleware decide, and the module grants itself no
// permission — audit.read is the authorization capability's. With no declaration
// surface the routes carry no declaration, and a guarded endpoint answers them
// as undeclared — which is why a command that mounts these routes should name
// the enforcement module too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
