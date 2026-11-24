package metadata

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

func (p *product) contentRoutes() []route {
	return []route{
		{http.MethodGet, contentPath, p.serveRead},
		{http.MethodHead, contentPath, p.serveRead},
		{http.MethodPut, contentPath, p.serveWrite},
	}
}

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
