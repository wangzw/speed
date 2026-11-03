// Package routecontract is the test support for the repository's check that a
// module's declared OpenAPI operations are the routes it actually registers.
//
// The two are separately maintained facts. The design leaves them free to drift
// at run time, and nothing in a run reports a difference, so the check that
// they agree is the only thing standing between a drifted fragment and a
// generated client built on a lie. It reads the fragments a module declares and
// the routes it registers and reports a difference in either direction: a route
// registered and not declared, an operation declared and not registered, a
// method written wrong, a path written wrong.
//
// It drives a real registry holding the module under test to Init, which is the
// stage a module mounts its routes in, through a stand-in entry point that
// records every Route call. The routes it compares are therefore the ones the
// module's own Init produced, from the same code a deployment runs, and not the
// list a case wrote down beside it. What the caller brings is the module and
// the stand-ins for what it requires.
//
// The authentication capability's modules mount through its own route-mounting
// module rather than through the entry point directly, so a carrier for one of
// those names that module in Stubs too; its registrations land on the recording
// entry point all the same, which is why one comparison covers both shapes.
//
// The package registers no module of its own beyond the two stand-ins it hands
// the drive, delivers no capability, and is imported by tests alone.
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

// DefaultEndpoint is the name of the single endpoint the stand-in entry point
// declares when a case names none. A module that resolves its endpoint from the
// entry point's list alone (the authentication capability's does) takes it.
const DefaultEndpoint = "api"

// Route is one operation: a method and the path it is served at, with a path
// parameter written the way gin writes it, ":name".
type Route struct {
	Method string
	Path   string
}

// String renders a route the way a case and a message read it.
func (r Route) String() string { return r.Method + " " + r.Path }

// key is the identity of a route in a set.
func (r Route) key() string { return r.Method + " " + r.Path }

// Router is an http.Router stand-in over a fixed endpoint list that records
// every route registered on it. It is safe to read after the drive; the
// registrations all happen in one goroutine, but the lock keeps it safe to read
// from any.
type Router struct {
	mu     sync.Mutex
	names  []string
	routes []Route
}

var _ speedhttp.Router = (*Router)(nil)

// NewRouter builds a stand-in entry point declaring the endpoints named. No
// name leaves it declaring DefaultEndpoint, which is what a module resolving
// its endpoint from the list alone needs.
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

// Routes returns every route registered on this entry point, in the order of
// the calls.
func (r *Router) Routes() []Route {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.routes)
}

// Module is a stand-in for the HTTP entry point, delivering the Router
// capability over this recorder.
func (r *Router) Module() core.Module {
	return StandIn("stand-in.router", r, (*speedhttp.Router)(nil))
}

func (r *Router) record(method, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, Route{Method: method, Path: path})
}

// recorder is the registration surface of one endpoint: everything it is asked
// to bind is written to the Router's ledger.
type recorder struct{ router *Router }

func (e recorder) Route(method, path string, handlers ...gin.HandlerFunc) {
	if len(handlers) == 0 {
		panic("routecontract: the module registered a route with no handler")
	}
	e.router.record(method, path)
}

func (recorder) Use(speedhttp.Middleware) {}

func (recorder) Accepting() bool { return true }

// Case is one module's check: the module, the stand-ins for what it requires,
// and the configuration the drive runs on.
type Case struct {
	// Module is the module under test. Its own Init must mount its routes.
	Module core.Module
	// Endpoints is the endpoint list the stand-in entry point declares. Empty
	// leaves DefaultEndpoint alone, and a module that reads the endpoint from
	// its own configuration must then name that same endpoint.
	Endpoints []string
	// Stubs are the modules that deliver what Module requires. Router.Module()
	// and the quiet logging stand-in are added for the caller.
	Stubs []core.Module
	// Config is the whole configuration document the drive runs on, JSON.
	Config map[string]any
	// QueryParams names, for each operation of the module, the query parameters
	// its handler accepts, keyed by the route written the way gin keeps a path
	// in. A nil map leaves the parameter check off.
	//
	// When it is set, Check holds every operation's declared query parameters
	// (those a fragment writes with "in": "query") to the set given here, in
	// both directions: a parameter the fragment declares that the handler does
	// not accept is a read a generated client would send and the process would
	// ignore, and one the handler accepts that the fragment does not declare is
	// a read a generated client cannot send. A route absent from the map accepts
	// no query parameter, so an operation that declares one and is not listed
	// fails. Path, header and cookie parameters are not compared.
	//
	// The set a case passes should be the one the handler closes over at run
	// time where the module has such a set, so that the comparison is against
	// what the process honors and not a second list kept beside it.
	QueryParams map[Route]map[string]bool
}

// Check drives an assembly holding c.Module to Init and reports every
// difference between the routes the module registered there and the operations
// its own OpenAPI fragments declare.
//
// A drive that fails, or a module that declares no fragment at all, fails the
// case: a check that compared an empty set against an empty set would pass over
// nothing.
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

// declared reads back every operation the module declared in the fragments it
// carries as speedhttp.Spec resources.
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

// operations reads one fragment's paths into the operations they declare. Only
// the HTTP methods are read: a fragment's path item may carry other keys, and
// one that does is not an operation.
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

// declaredParams reads, per operation of the module, the query parameters its
// fragments declare with "in": "query". A route with no such parameter is left
// out or maps to an empty set, which compareParams reads the same way.
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

// readParams adds one fragment's declared query parameters to into, keyed by the
// operation they belong to. Only the parameters an operation writes in its own
// "parameters" list are read: a parameter a path item writes above its methods
// is not, and path, header and cookie parameters are skipped because the check is
// about the query surface alone.
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

// paramSig is one query parameter of one operation: the route and the name.
type paramSig struct {
	Route Route
	Name  string
}

// paramDifferences splits the two per-operation sets: the query parameters an
// operation declares that its handler does not accept, and the ones its handler
// accepts that no fragment declares. A route in either map and not the other is
// compared against the empty set, so an operation that declares a query
// parameter and is not listed, and a listed operation writing none, are both
// caught. Both results are sorted by route then name, and both are empty when
// every operation's two sets are one.
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

// compareParams holds each operation's declared query parameters to the set its
// handler accepts, in both directions.
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

// isHTTPMethod reports whether a method is one a route can be registered under.
func isHTTPMethod(method string) bool {
	return slices.Contains([]string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace,
	}, method)
}

// ginPath rewrites an OpenAPI path into the form gin keeps a path in: a
// parameter is "{name}" in the one and ":name" in the other. A path with no
// parameter is returned as written.
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

// differences splits the two sets: the routes registered that no fragment
// declares, and the operations declared that no route registered. Both are
// sorted, and both are empty when the two sets are one.
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

// compare reports every route the module registered that its fragments do not
// declare and every operation they declare that it did not register.
//
// A module that declared no operation at all fails the case outright: an empty
// pair of sets would otherwise compare equal, and a check that reports nothing
// over nothing is the failure this whole package exists to avoid.
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

// Config merges configuration subtrees into one document, so a case can take
// the subtree a fixture built and add its own module's beside it. Two parts
// that write the same mapping merge; two that write the same leaf is a case's
// own mistake and panics rather than letting one silently replace the other.
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

// StandIn is a module that delivers instance for every token named, so a case
// can supply a stand-in for a capability without writing a descriptor for it.
//
// The usual stand-in is a struct embedding the capability's interface with no
// value in it — struct{ authn.Accounts } — which satisfies the interface
// without implementing a method. A module that only resolves a capability and
// holds it never calls one, and the ones that do need a value: the entry point
// records, the gateway registers, and a database answers.
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

// quietLogging delivers the logging capability over a logger that discards
// every record, so a module's startup diagnostics do not reach the process
// default logger and its standard output during a test.
type quietLogging struct{}

var _ log.Logger = quietLogging{}

func (quietLogging) Named(string) *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (quietLogging) Redaction() log.Redaction { return noRedaction{} }

// noRedaction is the registration surface of a chain that masks nothing: the
// stand-in writes no record, so there is nothing to mask.
type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

func quietLoggingModule() core.Module {
	return StandIn("stand-in.logging", quietLogging{}, (*log.Logger)(nil))
}

// pathTenantStandIn is both pkg/http route guards, wrapping nothing. The check
// compares declarations with registrations, so what a guard would decide is not
// its subject; the mounter refuses a tenant-scoped registration and the tenant
// management modules refuse their own mount when no guard is delivered, so both
// capabilities have to be here.
type pathTenantStandIn struct{}

func (pathTenantStandIn) Guard(next gin.HandlerFunc) gin.HandlerFunc { return next }

func (pathTenantStandIn) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

func pathTenantModule() core.Module {
	return StandIn("stand-in.path-tenant", pathTenantStandIn{},
		(*speedhttp.RouteGuard)(nil), (*speedhttp.TenantAdminRouteGuard)(nil))
}
