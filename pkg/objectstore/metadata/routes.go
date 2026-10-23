package metadata

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// route is one content route: the method and path it answers and the handler
// that serves it.
type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

// contentRoutes is the closed set of routes this module mounts, in the order they
// are mounted: one path, one token parameter, and the three methods a browser
// uses for bytes. There is no other HTTP surface: the metadata and lifecycle
// calls are the Objects seam, reached in process by a business backend and never
// by a frontend.
func (p *product) contentRoutes() []route {
	return []route{
		{http.MethodGet, contentPath, p.serveRead},
		{http.MethodHead, contentPath, p.serveRead},
		{http.MethodPut, contentPath, p.serveWrite},
	}
}

// mount resolves every configured endpoint on the entry point and registers the
// content route on each. Every name is looked up before any route is registered,
// so a name the entry point does not declare fails the startup naming that
// endpoint rather than leaving a partial mounting behind.
//
// The route is mounted anonymously: the token is the credential, and a browser
// loading an image can carry neither an Authorization header nor a tenant one.
// Which layers sit on the endpoint is the host's configuration; this module
// mounts on the endpoints it is told to and nowhere else.
func (p *product) mount(router speedhttp.Router) error {
	type target struct {
		name     string
		endpoint speedhttp.Endpoint
	}
	targets := make([]target, 0, len(p.contentEndpoints))
	for _, name := range p.contentEndpoints {
		endpoint, err := router.Endpoint(name)
		if err != nil {
			return fmt.Errorf("%s: the content route cannot mount on the endpoint %q: %w",
				ModuleName, name, err)
		}
		targets = append(targets, target{name: name, endpoint: endpoint})
	}
	for _, tgt := range targets {
		for _, r := range p.contentRoutes() {
			tgt.endpoint.Route(r.method, r.path, r.handler)
		}
	}
	p.contentMounted = true
	return nil
}
