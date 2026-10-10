package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// opsModuleName is the name the operations module is registered under.
const opsModuleName = "ops"

// The operations routes. They sit on the endpoint no guard covers, because a
// probe carries no credential and names no tenant.
const (
	healthRoute  = "GET /health"
	readyRoute   = "GET /ready"
	openAPIRoute = "GET /openapi.json"
)

// The records a long-running command writes around its run. readyMsg is what a
// case waits for: the listening sockets are open before the command's action
// runs, so a case that waited on a port alone could act on a run whose modules
// have not all started. The command attribute says which command wrote it.
const (
	readyMsg   = "ready"
	stoppedMsg = "stopped"
)

// openAPIVersion is the OpenAPI version the merged document declares. Every
// fragment this host embeds is written against it.
const openAPIVersion = "3.1.0"

//go:embed openapi/ops.json
var opsOpenAPI []byte

// announceReady writes the record a caller waits for, once every module of the
// command has started. It goes through the process's default logger, which
// writes standard output whatever destinations the configuration names, so the
// record is where a supervisor reading the child's output finds it.
func announceReady(command string) {
	log.Default().Info(readyMsg, "command", command)
}

// announceStopped writes the record that says the command's run is over and
// its modules are about to be released.
func announceStopped(command string) {
	log.Default().Info(stoppedMsg, "command", command)
}

// opsProduct is the operations module's product.
type opsProduct struct {
	released
	db       db.Database
	router   speedhttp.Router
	document []byte
}

// health is what the liveness route answers with.
type health struct {
	Status string `json:"status"`
}

// readiness is what the readiness route answers with: the verdict, and which of
// its parts failed.
type readiness struct {
	Status   string   `json:"status"`
	Failures []string `json:"failures,omitempty"`
}

// reportHealth answers that the process is alive. It reads nothing.
func reportHealth(c *gin.Context) {
	c.JSON(nethttp.StatusOK, health{Status: "ok"})
}

// reportReady answers whether this process can serve: every endpoint accepting
// and the database answering. Each failure is named, so a probe's log says
// which half was down.
func (p *opsProduct) reportReady(c *gin.Context) {
	var failures []string
	for _, name := range servedEndpoints {
		endpoint, err := p.router.Endpoint(name)
		if err != nil || !endpoint.Accepting() {
			failures = append(failures, "endpoint "+name+" is not accepting")
		}
	}
	if pool, err := p.db.DB().DB(); err != nil {
		failures = append(failures, "the database handle has no pool: "+err.Error())
	} else if err := pool.PingContext(c.Request.Context()); err != nil {
		failures = append(failures, "the database does not answer: "+err.Error())
	}
	if len(failures) > 0 {
		c.JSON(nethttp.StatusServiceUnavailable, readiness{Status: "unavailable", Failures: failures})
		return
	}
	c.JSON(nethttp.StatusOK, readiness{Status: "ready"})
}

// serveDocument answers the merged OpenAPI document.
func (p *opsProduct) serveDocument(c *gin.Context) {
	c.Data(nethttp.StatusOK, "application/json; charset=utf-8", p.document)
}

// fragment is the part of a declared OpenAPI fragment the merge reads: its
// paths, and its components by section — schemas, parameters and the rest.
type fragment struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components map[string]map[string]json.RawMessage `json:"components"`
}

// fragmentSource says which module declared a fragment, and for which endpoint.
type fragmentSource struct {
	Module   string `json:"module"`
	Endpoint string `json:"endpoint"`
}

// mergedDocument is the document the operations endpoint serves.
type mergedDocument struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components map[string]map[string]json.RawMessage `json:"components"`
	Fragments  []fragmentSource                      `json:"x-fragments"`
}

// endpointKey is the extension every merged operation carries, naming the
// endpoint the operation is served on: one document describes four listening
// endpoints, and a path alone does not say which one answers it.
const endpointKey = "x-endpoint"

// mergeSpecs merges every declared fragment into one document.
//
// It reads every fragment in the binary — the available set — because the
// question the document answers is which API this binary provides; the HTTP
// module hands the fragments over unfiltered and leaves that decision to the
// reader. A path's operation declared twice is a conflict and fails the startup
// naming both declaring modules and the endpoint each declared it for, which is
// the only place the two can be connected back to their sources: a document
// served with one of them silently dropped would describe an API that is not
// the one being served. One document holds an operation once, so the conflict
// is judged across endpoints as well as within one: a route served on two
// endpoints is two operations the document cannot tell apart.
func mergeSpecs(specs []core.Resource[speedhttp.Spec]) ([]byte, error) {
	var doc mergedDocument
	doc.OpenAPI = openAPIVersion
	doc.Info.Title = hostName
	doc.Info.Version = hostVersionNumber
	doc.Paths = map[string]map[string]json.RawMessage{}
	doc.Components = map[string]map[string]json.RawMessage{}
	doc.Fragments = []fragmentSource{}

	owners := map[string]fragmentSource{}
	for _, res := range specs {
		var part fragment
		if err := json.Unmarshal(res.Value.Document, &part); err != nil {
			return nil, fmt.Errorf("reading the OpenAPI fragment module %q declares: %w", res.Module, err)
		}
		doc.Fragments = append(doc.Fragments, fragmentSource{Module: res.Module, Endpoint: res.Value.Endpoint})
		for path, item := range part.Paths {
			merged := doc.Paths[path]
			if merged == nil {
				merged = map[string]json.RawMessage{}
				doc.Paths[path] = merged
			}
			for key, raw := range item {
				where := path + " " + strings.ToUpper(key)
				if other, taken := owners[where]; taken {
					return nil, fmt.Errorf("the OpenAPI fragments of module %q for endpoint %q and module %q "+
						"for endpoint %q both declare %s", other.Module, other.Endpoint, res.Module,
						res.Value.Endpoint, where)
				}
				owners[where] = fragmentSource{Module: res.Module, Endpoint: res.Value.Endpoint}
				tagged, err := withEndpoint(raw, res.Value.Endpoint)
				if err != nil {
					return nil, fmt.Errorf("reading %s in the fragment module %q declares: %w", where, res.Module, err)
				}
				merged[key] = tagged
			}
		}
		// Components are shared by name across fragments: two fragments may
		// both declare the error body, and they agree or the merge stops.
		for section, entries := range part.Components {
			merged := doc.Components[section]
			if merged == nil {
				merged = map[string]json.RawMessage{}
				doc.Components[section] = merged
			}
			for name, value := range entries {
				if existing, taken := merged[name]; taken && !jsonEqual(existing, value) {
					return nil, fmt.Errorf("the OpenAPI fragment module %q declares components.%s.%s "+
						"differently from an earlier fragment", res.Module, section, name)
				}
				merged[name] = value
			}
		}
	}
	slices.SortFunc(doc.Fragments, func(a, b fragmentSource) int {
		return strings.Compare(a.Module+" "+a.Endpoint, b.Module+" "+b.Endpoint)
	})
	return json.Marshal(doc)
}

// withEndpoint adds the endpoint extension to one operation object.
func withEndpoint(raw json.RawMessage, endpoint string) (json.RawMessage, error) {
	var operation map[string]json.RawMessage
	if err := json.Unmarshal(raw, &operation); err != nil {
		return nil, err
	}
	name, err := json.Marshal(endpoint)
	if err != nil {
		return nil, err
	}
	operation[endpointKey] = name
	return json.Marshal(operation)
}

// jsonEqual compares two JSON values by content rather than by spelling.
func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, errL := json.Marshal(left)
	r, errR := json.Marshal(right)
	return errL == nil && errR == nil && string(l) == string(r)
}

// init registers the operations module. It runs on serve, where the operations
// endpoint is.
func init() {
	registerApplication(application{module: opsModule(), runsOn: []string{serveCommand}})
}

// opsModule binds the operations routes and merges the API document.
//
// The document is merged in Init rather than per request: the fragments are
// declarations, fixed for the life of the binary, and a conflict between two of
// them is a startup failure rather than a 500 on the first probe.
func opsModule() core.Module {
	return core.Module{
		Name: opsModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*db.Database)(nil)},
		},
		Resources: []any{
			speedhttp.Spec{Endpoint: opsEndpoint, Document: opsOpenAPI},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, opsModuleName)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			return &opsProduct{released: released{log: logger}, db: database}, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			product, ok := instance.(*opsProduct)
			if !ok {
				return errForeignInstance(opsModuleName, instance)
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ops, err := router.Endpoint(opsEndpoint)
			if err != nil {
				return err
			}
			document, err := mergeSpecs(core.Resources[speedhttp.Spec](reg))
			if err != nil {
				return err
			}
			product.router = router
			product.document = document
			mount(ops, healthRoute, reportHealth)
			mount(ops, readyRoute, product.reportReady)
			mount(ops, openAPIRoute, product.serveDocument)
			return nil
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}
