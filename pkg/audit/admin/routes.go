package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

var (
	recordsPath = auditSpec.ResolvePath(pathRecords)
	recordPath  = auditSpec.ResolvePath(pathRecord)
	exportPath  = auditSpec.ResolvePath(pathExport)
)

// managementGroups names the route groups in the order the start diagnostic
// writes them. The whole surface is one group, and it is always mounted, because
// the read surface it reads through is required.
var managementGroups = []string{"records"}

const realmKey = "audit.admin.realm"

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

// shape is one route's request-shape check. It runs after the caller and the
// decision and before the typed handler binds the declared input.
type shape func(*gin.Context) bool

func (p *product) routes() []route {
	return []route{
		{http.MethodGet, recordsPath, p.guardRoute(listParams, listShape, listOperation.Handle(p.handleRecordList))},
		{http.MethodGet, recordPath, p.guardRoute(nil, nil, getOperation.Handle(p.handleRecordGet))},
		{http.MethodGet, exportPath, p.guardRoute(exportParams, nil, exportOperation.Handle(p.handleRecordExport))},
	}
}

// guardRoute puts one decision order in front of a route: the caller first (401
// with no identity), then the handler-internal guard for audit.read.
func (p *product) guardRoute(accepted map[string]bool, check shape, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := p.caller(c); !ok || !p.require(c) {
			return
		}
		if !onlyAccepted(c, accepted) {
			p.bad(c)
			return
		}
		if check != nil && !check(c) {
			p.bad(c)
			return
		}
		next(c)
	}
}

// listShape checks the one bound the typed binder would answer with its
// generic refusal rather than this module's: a limit that is not a
// non-negative integer.
func listShape(c *gin.Context) bool {
	s := c.Query(paramLimit)
	if s == "" {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0
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
