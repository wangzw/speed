package preferences

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// engine is one database capability this module is exercised on. The PostgreSQL
// one skips on a machine without docker.
type engine struct {
	name     string
	database core.Module
	section  string
	locate   func(t *testing.T) string
}

var engines = []engine{
	{
		name:     "sqlite",
		database: sqlite.Module(),
		section:  "sqlite",
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "preferences.db") },
	},
	{
		name:     "postgres",
		database: dbpostgres.Module(),
		section:  "postgres",
		locate:   pgtest.Acquire,
	},
}

// forEachEngine runs a case on every engine.
func forEachEngine(t *testing.T, run func(t *testing.T, e engine)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e) })
	}
}

// harness is one assembled run of this module: the engine it sits on, the
// modules that declare notification types, the entry point a case supplies and
// the stage the run reaches.
type harness struct {
	engine engine
	dsn    string
	// declare holds modules that take part in the run and declare types.
	declare []core.Module
	// registered holds modules that are registered but left out of the
	// participating set, the shape a module a command does not drive has.
	registered []core.Module
	withRouter bool
	router     *stubRouter
	depth      core.Stage
}

// on builds the harness on an engine over a fresh database of that engine's own.
func (e engine) on(t *testing.T, h harness) harness {
	h.engine = e
	if h.dsn == "" {
		h.dsn = e.locate(t)
	}
	return h
}

// drive loads the configuration the way a host loads it, drives a registry to
// the run's depth, and runs action there. A drive failure is fatal; a case that
// expects one calls run instead.
//
// The preferences module declares no configuration of its own, so the only
// sections are the database's and the logging capability's.
func (h harness) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	if err := h.run(t, action); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// run is the body of drive, returning the drive's error rather than failing on
// it, so a case can hold the startup to a refusal.
func (h harness) run(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	chosen := h.engine
	if chosen.name == "" {
		chosen = engines[0]
		h.dsn = chosen.locate(t)
	}
	dir := t.TempDir()
	document := map[string]any{
		"db": map[string]any{chosen.section: map[string]any{"dsn": h.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	modules := append([]core.Module{chosen.database, log.Module(), catalog.Module(), Module()}, h.declare...)
	if h.withRouter || h.router != nil {
		router := h.router
		if router == nil {
			router = newStubRouter("public")
		}
		modules = append(modules, standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
	}

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDNOTIFICATIONTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	for _, m := range h.registered {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(modules))
	seen := map[string]bool{}
	for _, m := range modules {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		reg.Register(m)
		names = append(names, m.Name)
	}
	for _, m := range h.registered {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		// Registered but not participating: the way a command leaves a module
		// in the available set without driving it.
		reg.Register(m)
	}
	reg.Supply(reader, config.ModuleName)
	depth := h.depth
	if depth == 0 {
		depth = core.StageStart
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action:       action,
	})
}

// declaringModule is a stand-in business module that declares notification types
// through its Resources, the way a real one does.
func declaringModule(name string, specs ...notification.TypeSpec) core.Module {
	resources := make([]any, len(specs))
	for i, spec := range specs {
		resources[i] = spec
	}
	return core.Module{Name: name, Resources: resources}
}

// preferencesOf resolves the preferences seam.
func preferencesOf(t *testing.T, reg *core.Registry) notification.Preferences {
	t.Helper()
	prefs, err := core.Resolve[notification.Preferences](reg)
	if err != nil {
		t.Fatalf("resolving the preferences: %v", err)
	}
	return prefs
}

// productOfAssembly narrows the resolved seam to this module's product.
func productOfAssembly(t *testing.T, reg *core.Registry) *product {
	t.Helper()
	prefs := preferencesOf(t, reg)
	p, ok := prefs.(*product)
	if !ok {
		t.Fatalf("the preferences are a %T, not this module's product", prefs)
	}
	return p
}

// tenantCtx registers a tenant in the roster and returns a context that belongs
// to it.
func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := roster.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Alias: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := roster.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

// rowCount counts the rows in the preference table, for the cases that care
// whether a write happened.
func rowCount(t *testing.T, reg *core.Registry) int64 {
	t.Helper()
	p := productOfAssembly(t, reg)
	var n int64
	if err := p.handle.Table(tableName).Count(&n).Error; err != nil {
		t.Fatalf("counting the preference rows: %v", err)
	}
	return n
}

// aSpec is a valid declaration a case can lean on, with the key it is given.
func aSpec(key string, unsubscribable bool, channels ...notification.Channel) notification.TypeSpec {
	spec := notification.TypeSpec{
		Key:             key,
		Group:           "test",
		DefaultChannels: channels,
		Unsubscribable:  unsubscribable,
	}
	if len(channels) == 0 {
		spec.DefaultChannels = []notification.Channel{notification.ChannelInApp}
	}
	return spec
}

// mountedRoute is one (method, path) a registration recorded.
type mountedRoute struct{ method, path string }

// stubEndpoint records the routes registered on it.
type stubEndpoint struct {
	mu     sync.Mutex
	routes []mountedRoute
}

func (e *stubEndpoint) Route(method, path string, _ ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = append(e.routes, mountedRoute{method, path})
}

func (e *stubEndpoint) Use(speedhttp.Middleware) {}

func (e *stubEndpoint) Accepting() bool { return true }

func (e *stubEndpoint) registered() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.routes...)
}

// stubRouter is a stand-in http.Router over a fixed set of endpoints.
type stubRouter struct {
	mu        sync.Mutex
	endpoints map[string]*stubEndpoint
	order     []string
}

func newStubRouter(names ...string) *stubRouter {
	r := &stubRouter{endpoints: make(map[string]*stubEndpoint, len(names))}
	for _, name := range names {
		r.endpoints[name] = &stubEndpoint{}
		r.order = append(r.order, name)
	}
	return r
}

func (r *stubRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, ok := r.endpoints[name]
	if !ok {
		return nil, errors.New("the entry point declares no endpoint " + name)
	}
	return endpoint, nil
}

func (r *stubRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *stubRouter) endpoint(name string) *stubEndpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpoints[name]
}

// standIn is a module that delivers instance for the tokens named.
func standIn(name string, instance any, tokens ...core.Token) core.Module {
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

// serveHandler drives one request through a gin engine that carries a single
// route, so a case exercises a handler's decision order without an entry point.
// pattern is the route as it is mounted — with the same :params — and target is
// the concrete path the request asks for.
func serveHandler(t *testing.T, method, pattern, target string, ctx context.Context, body io.Reader, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(method, pattern, handler)
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}
