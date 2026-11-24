// Package routecontract checks that a module's declared OpenAPI operations are the routes it registers.
package routecontract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/log"
)

// DefaultEndpoint is the name of the single endpoint the stand-in entry point declares when a case
// names none.
const DefaultEndpoint = "api"

// Route is one operation: a method and the path it is served at, with a path parameter written the
// way gin writes it, ":name".
type Route struct {
	// Method is the HTTP method.
	Method string
	// Path is the route path.
	Path string
}

// String renders a route the way a case and a message read it.
func (r Route) String() string { return r.Method + " " + r.Path }

func (r Route) key() string { return r.Method + " " + r.Path }

// Router is an http.Router stand-in over a fixed endpoint list that records every route registered
// on it.
type Router struct {
	mu     sync.Mutex
	names  []string
	routes []Route
}

var _ speedhttp.Router = (*Router)(nil)

// NewRouter builds a stand-in entry point declaring the endpoints named.
// No name leaves it declaring DefaultEndpoint, which is what a module resolving its endpoint from
// the list alone needs.
func NewRouter(names ...string) *Router {
	if len(names) == 0 {
		names = []string{DefaultEndpoint}
	}
	own := slices.Clone(names)
	sort.Strings(own)
	return &Router{names: own}
}

// Endpoints returns the declared endpoint names, ascending.
func (r *Router) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.names)
}

// Endpoint returns the endpoint named, which is a recorder.
func (r *Router) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.names, name) {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return recorder{router: r}, nil
}

// Routes returns every route registered on this entry point, in the order of the calls.
func (r *Router) Routes() []Route {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.routes)
}

// Module is a stand-in for the HTTP entry point, delivering the Router capability over this
// recorder.
func (r *Router) Module() core.Module {
	return StandIn("stand-in.router", r, (*speedhttp.Router)(nil))
}

func (r *Router) record(method, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, Route{Method: method, Path: path})
}

type recorder struct{ router *Router }

func (e recorder) Route(method, path string, handlers ...gin.HandlerFunc) {
	if len(handlers) == 0 {
		panic("routecontract: the module registered a route with no handler")
	}
	e.router.record(method, path)
}

func (recorder) Use(speedhttp.Middleware) {}

func (recorder) Accepting() bool { return true }

// Case is one module's check: the module, the stand-ins for what it requires, and the
// configuration the drive runs on.
type Case struct {
	// Module is the module under test.
	// Its own Init must mount its routes.
	Module core.Module
	// Endpoints is the endpoint list the stand-in entry point declares.
	// Empty leaves DefaultEndpoint alone, and a module that reads the endpoint from its own
	// configuration must then name that same endpoint.
	Endpoints []string
	// Stubs are the modules that deliver what Module requires.
	// Router.Module() and the quiet logging stand-in are added for the caller.
	Stubs []core.Module
	// Config is the whole configuration document the drive runs on, JSON.
	Config map[string]any
	// QueryParams names, for each operation of the module, the query parameters its handler accepts,
	// keyed by the route written the way gin keeps a path in.
	// A nil map leaves the parameter check off.
	QueryParams map[Route]map[string]bool
}

// Check drives an assembly holding c.Module to Init and reports every difference between the
// routes the module registered there and the operations its own OpenAPI fragments declare.
func Check(t *testing.T, c Case) {
	t.Helper()

	router := NewRouter(c.Endpoints...)
	modules := append([]core.Module{c.Module}, c.Stubs...)
	modules = append(modules, router.Module(), quietLoggingModule(), pathTenantModule())

	document := c.Config
	if document == nil {
		document = map[string]any{}
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("routecontract: encoding the configuration: %v", err)
	}
	reader := configtest.Reader(t, string(body), modules...)

	reg := core.New()
	reg.Register(config.Module())
	var names []string
	seen := map[string]bool{}
	for _, m := range modules {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)

	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
	}); err != nil {
		t.Fatalf("routecontract: driving %s to Init: %v", c.Module.Name, err)
	}

	compare(t, c.Module.Name, router.Routes(), declared(t, reg, c.Module.Name))
	if c.QueryParams != nil {
		compareParams(t, c.Module.Name, declaredParams(t, reg, c.Module.Name), c.QueryParams)
	}
}

func declared(t *testing.T, reg *core.Registry, module string) []Route {
	t.Helper()
	var out []Route
	for _, res := range core.Resources[speedhttp.Spec](reg) {
		if res.Module != module {
			continue
		}
		for _, route := range operations(t, module, res.Value.Document) {
			route.Path = res.Value.ResolvePath(route.Path)
			out = append(out, route)
		}
	}
	return out
}

func operations(t *testing.T, module string, fragment []byte) []Route {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(fragment, &doc); err != nil {
		t.Fatalf("routecontract: the fragment module %q declares is not a JSON object: %v", module, err)
	}
	var out []Route
	for path, item := range doc.Paths {
		for method := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			out = append(out, Route{Method: upper, Path: ginPath(path)})
		}
	}
	return out
}

func declaredParams(t *testing.T, reg *core.Registry, module string) map[Route]map[string]bool {
	t.Helper()
	out := map[Route]map[string]bool{}
	for _, res := range core.Resources[speedhttp.Spec](reg) {
		if res.Module != module {
			continue
		}
		readParams(t, module, res.Value, out)
	}
	return out
}

func readParams(t *testing.T, module string, spec speedhttp.Spec, into map[Route]map[string]bool) {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec.Document, &doc); err != nil {
		t.Fatalf("routecontract: the fragment module %q declares is not a JSON object: %v", module, err)
	}
	for path, item := range doc.Paths {
		for method, raw := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			var op struct {
				Parameters []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("routecontract: the %s operation on %s in a fragment module %q declares is not a JSON object: %v",
					upper, path, module, err)
			}
			route := Route{Method: upper, Path: spec.ResolvePath(ginPath(path))}
			for _, p := range op.Parameters {
				if p.In != "query" {
					continue
				}
				if into[route] == nil {
					into[route] = map[string]bool{}
				}
				into[route][p.Name] = true
			}
		}
	}
}

type paramSig struct {
	Route Route
	Name  string
}

func paramDifferences(declared, accepted map[Route]map[string]bool) (declaredOnly, acceptedOnly []paramSig) {
	routes := map[Route]bool{}
	for r := range declared {
		routes[r] = true
	}
	for r := range accepted {
		routes[r] = true
	}
	for r := range routes {
		for name := range declared[r] {
			if !accepted[r][name] {
				declaredOnly = append(declaredOnly, paramSig{Route: r, Name: name})
			}
		}
		for name := range accepted[r] {
			if !declared[r][name] {
				acceptedOnly = append(acceptedOnly, paramSig{Route: r, Name: name})
			}
		}
	}
	byRouteThenName := func(s []paramSig) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].Route.key() != s[j].Route.key() {
				return s[i].Route.key() < s[j].Route.key()
			}
			return s[i].Name < s[j].Name
		})
	}
	byRouteThenName(declaredOnly)
	byRouteThenName(acceptedOnly)
	return declaredOnly, acceptedOnly
}

func compareParams(t *testing.T, module string, declared, accepted map[Route]map[string]bool) {
	t.Helper()

	declaredOnly, acceptedOnly := paramDifferences(declared, accepted)
	for _, d := range declaredOnly {
		t.Errorf("the module %s declares the query parameter %q on %s in an OpenAPI fragment, which "+
			"its handler does not accept. A generated client would send it and the process would "+
			"ignore it, answering an unfiltered result under a name that looks honored", module, d.Name, d.Route)
	}
	for _, d := range acceptedOnly {
		t.Errorf("the module %s accepts the query parameter %q on %s, which none of its OpenAPI "+
			"fragments declares. A generated client has no way to send it, so the read it "+
			"drives is unreachable", module, d.Name, d.Route)
	}
}

func isHTTPMethod(method string) bool {
	return slices.Contains([]string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace,
	}, method)
}

func ginPath(p string) string {
	if !strings.ContainsAny(p, "{}") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			if p[i] != '}' {
				b.WriteByte(p[i])
			}
			continue
		}
		b.WriteByte(':')
		for i++; i < len(p) && p[i] != '}'; i++ {
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

func differences(registered, declared []Route) (undeclared, unregistered []string) {
	have := map[string]bool{}
	for _, r := range registered {
		have[r.key()] = true
	}
	want := map[string]bool{}
	for _, r := range declared {
		want[r.key()] = true
	}

	for _, r := range registered {
		if !want[r.key()] {
			undeclared = append(undeclared, r.String())
		}
	}
	for _, r := range declared {
		if !have[r.key()] {
			unregistered = append(unregistered, r.String())
		}
	}
	sort.Strings(undeclared)
	sort.Strings(unregistered)
	return undeclared, unregistered
}

func compare(t *testing.T, module string, registered, want []Route) {
	t.Helper()

	if len(want) == 0 {
		t.Fatalf("routecontract: the module %s declares no operation in any speedhttp.Spec fragment it "+
			"carries, so there is nothing to compare its routes against", module)
	}

	undeclared, unregistered := differences(registered, want)

	for _, r := range undeclared {
		t.Errorf("the module %s registers %s, which none of its OpenAPI fragments declares. "+
			"The declaration is what a generated client is built from, so the route is served and "+
			"undocumented", module, r)
	}
	for _, r := range unregistered {
		t.Errorf("the module %s declares %s in an OpenAPI fragment and registers no such route. "+
			"A generated client would call an operation the process does not serve", module, r)
	}
}

// Config merges configuration subtrees into one document, so a case can take the subtree a fixture
// built and add its own module's beside it.
func Config(parts ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, part := range parts {
		mergeInto(merged, part, "")
	}
	return merged
}

func mergeInto(into, from map[string]any, prefix string) {
	for key, value := range from {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		existing, taken := into[key]
		if !taken {
			into[key] = value
			continue
		}
		intoMap, intoIsMap := existing.(map[string]any)
		fromMap, fromIsMap := value.(map[string]any)
		if intoIsMap && fromIsMap {
			mergeInto(intoMap, fromMap, path)
			continue
		}
		panic("routecontract: two configuration parts write the key " + path)
	}
}

// StandIn is a module that delivers instance for every token named, so a case can supply a
// stand-in for a capability without writing a descriptor for it.
func StandIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type quietLogging struct{}

var _ log.Logger = quietLogging{}

func (quietLogging) Named(string) *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (quietLogging) Redaction() log.Redaction { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

func quietLoggingModule() core.Module {
	return StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil))
}

type pathTenantStandIn struct{}

func (pathTenantStandIn) Guard(next gin.HandlerFunc) gin.HandlerFunc { return next }

func (pathTenantStandIn) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

func pathTenantModule() core.Module {
	return StandIn("stand-in.path-tenant", pathTenantStandIn{},
		(*speedhttp.RouteGuard)(nil), (*speedhttp.TenantAdminRouteGuard)(nil))
}
