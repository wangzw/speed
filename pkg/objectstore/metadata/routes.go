package metadata

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The routes, under the fixed base path. The set is closed: every action an
// object store exposes is one of these seven, and each is bound to the method
// and path the design fixes.
const (
	basePath           = "/api/v1/objects"
	pathObjects        = basePath
	pathObject         = basePath + "/:id"
	pathObjectContent  = basePath + "/:id/content"
	pathObjectComplete = basePath + "/:id/complete"
)

// route is one route: the method and path it answers, the permission it needs,
// and the handler that serves it.
type route struct {
	method  string
	path    string
	perm    authz.Permission
	handler gin.HandlerFunc
}

// routes is the closed set of routes, in the order they are mounted. The read
// routes need objectstore.object.read and the write routes objectstore.object.write.
func (p *product) routes() []route {
	return []route{
		{http.MethodPost, pathObjects, PermissionObjectWrite, p.handleCreate},
		{http.MethodPut, pathObjectContent, PermissionObjectWrite, p.handleUpload},
		{http.MethodPost, pathObjectComplete, PermissionObjectWrite, p.handleComplete},
		{http.MethodGet, pathObjects, PermissionObjectRead, p.handleList},
		{http.MethodGet, pathObject, PermissionObjectRead, p.handleGet},
		{http.MethodGet, pathObjectContent, PermissionObjectRead, p.handleContent},
		{http.MethodDelete, pathObject, PermissionObjectWrite, p.handleDelete},
	}
}

// mount resolves every configured endpoint on the entry point and registers the
// routes on each. Every name is looked up before any route is registered, so a
// name the entry point does not declare fails the startup naming that endpoint
// rather than leaving a partial mounting behind.
func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.endpoints))
	for _, name := range p.endpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: %w: %q: %w", ModuleName, authz.ErrUnknownEndpoint, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range p.routes() {
			tgt.endpoint.Route(r.method, r.path, r.handler)
		}
	}
	return nil
}

// declare records each route's permission on the route-declaration surface, when
// one is present. The route names the permission it needs; the enforcement
// middleware reads the declaration and decides, so the handler carries no
// decision of its own. A route naming a permission no module declares, or one
// the process's translation has no row for, fails the startup in the surface.
func (p *product) declare() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Require(r.method, r.path, r.perm)
	}
}
