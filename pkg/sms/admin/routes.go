package admin

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The management routes, relative to the fixed base path. They are mounted on
// their own base path and not under a configurable one, and every one of them is
// addressed by a natural key — a template name — so that no opaque row
// identifier ever appears in a path.
//
// The paths are the resolved forms of the declarations in operations.go, which
// are written relative to the API root the fragment carries.
const (
	basePath = "/api/v1/system/sms"

	pathTemplates   = basePath + "/templates"
	pathSubmissions = basePath + "/templates/submissions"
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group, and it is always mounted, because
// the registry it reads and writes is required.
var managementGroups = []string{"templates"}

// realmKey is where the name of the endpoint a management route was mounted on
// is put for the handler to read back. A route that answers 401 itself writes
// the endpoint's name as the realm of its challenge — the name of the protection
// space, the same realm the admission and authorization layers use — and the
// endpoint name is known only where the routes are mounted, which is why it is
// carried on the request rather than read in the handler.
const realmKey = "sms.admin.realm"

// withRealm wraps a management handler so the endpoint it was mounted on reaches
// it as the realm of its 401. The same handler is mounted on every configured
// endpoint, so the realm is bound per endpoint here rather than held by the
// handler.
func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

// route is one management route: the method and path it answers and the handler
// that serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// routes is the closed set of management routes, in the order they are mounted.
// Each route's handler is bound from the operation declared for it, and wrapped
// with the decisions the surface makes before the entry point binds the input:
// the caller is read and the route's permission is asked.
func (p *product) routes() []route {
	return []route{
		{
			listOperation.Method,
			adminSpec.ResolvePath(listOperation.Path),
			p.permitted(PermissionTemplateRead, listOperation.Handle(p.handleTemplateList)),
		},
		{
			registerOperation.Method,
			adminSpec.ResolvePath(registerOperation.Path),
			p.permitted(PermissionTemplateManage, registerOperation.Handle(p.handleTemplateRegister)),
		},
		{
			offerOperation.Method,
			adminSpec.ResolvePath(offerOperation.Path),
			p.permitted(PermissionTemplateManage, offerOperation.Handle(p.handleTemplateOffer)),
		},
		{
			setEnabledOperation.Method,
			adminSpec.ResolvePath(setEnabledOperation.Path),
			p.permitted(PermissionTemplateManage, setEnabledOperation.Handle(p.handleTemplateSetEnabled)),
		},
		{
			removeOperation.Method,
			adminSpec.ResolvePath(removeOperation.Path),
			p.permitted(PermissionTemplateManage, removeOperation.Handle(p.handleTemplateRemove)),
		},
	}
}

// mount resolves every configured endpoint on the entry point and registers the
// management routes on each. Every name is looked up before any route is
// registered, so a name the entry point does not declare fails the startup
// naming that endpoint rather than leaving a partial mounting behind.
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

// declareOpen records every management route as needing no permission on the
// route-declaration surface, when one is present.
//
// The routes are declared open so that the authorization middleware lets a
// request reach the handler: the handler makes the decision, on the permission
// the request's method implies, through the handler-internal guard. Declaring
// them to require a permission would make the middleware decide, and the module
// grants itself no permission. With no declaration surface the routes carry no
// declaration, and a guarded endpoint answers them as undeclared — which is why
// a command that mounts these routes should name the enforcement module too.
func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
