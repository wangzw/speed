package admin

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	basePath = "/api/v1/system/sms"

	pathTemplates   = basePath + "/templates"
	pathSubmissions = basePath + "/templates/submissions"
	pathCarrier     = basePath + "/carrier"
)

var managementGroups = []string{"templates", "carrier"}

const realmKey = "sms.admin.realm"

func withRealm(endpoint string, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(realmKey, endpoint)
		handler(c)
	}
}

type route struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

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
		{
			carrierOperation.Method,
			adminSpec.ResolvePath(carrierOperation.Path),
			p.permitted(PermissionTemplateRead, carrierOperation.Handle(p.handleCarrier)),
		},
	}
}

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

func (p *product) declareOpen() {
	if p.gate == nil {
		return
	}
	for _, r := range p.routes() {
		p.gate.Open(r.method, r.path)
	}
}
