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
	"github.com/vislake/speed/pkg/db/sqlite"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

type engine struct {
	name     string
	database core.Module
	section  string
	locate   func(t *testing.T) string
}

// engines lists the database capabilities a case is exercised on, SQLite
// first: a case that names none of its own runs on engines[0].
var engines = []engine{
	{
		name:     "sqlite",
		database: sqlite.Module(),
		section:  "sqlite",
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "preferences.db") },
	},
}

func forEachEngine(t *testing.T, run func(t *testing.T, e engine)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e) })
	}
}

type harness struct {
	engine     engine
	dsn        string
	declare    []core.Module
	registered []core.Module
	endpoints  []string
	withRouter bool
	router     speedhttp.Router
	depth      core.Stage
}

func (e engine) on(t *testing.T, h harness) harness {
	h.engine = e
	if h.dsn == "" {
		h.dsn = e.locate(t)
	}
	return h
}

func (h harness) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	if err := h.run(t, action); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

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
	if h.endpoints != nil {
		document["notification"] = map[string]any{
			"preferences": map[string]any{"endpoints": h.endpoints},
		}
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

func declaringModule(name string, specs ...notification.TypeSpec) core.Module {
	resources := make([]any, len(specs))
	for i, spec := range specs {
		resources[i] = spec
	}
	return core.Module{Name: name, Resources: resources}
}

func preferencesOf(t *testing.T, reg *core.Registry) notification.Preferences {
	t.Helper()
	prefs, err := core.Resolve[notification.Preferences](reg)
	if err != nil {
		t.Fatalf("resolving the preferences: %v", err)
	}
	return prefs
}

func productOfAssembly(t *testing.T, reg *core.Registry) *product {
	t.Helper()
	prefs := preferencesOf(t, reg)
	p, ok := prefs.(*product)
	if !ok {
		t.Fatalf("the preferences are a %T, not this module's product", prefs)
	}
	return p
}

func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := roster.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Name: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := roster.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

func rowCount(t *testing.T, reg *core.Registry) int64 {
	t.Helper()
	p := productOfAssembly(t, reg)
	var n int64
	if err := p.handle.Table(tableName).Count(&n).Error; err != nil {
		t.Fatalf("counting the preference rows: %v", err)
	}
	return n
}

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

type mountedRoute struct{ method, path string }

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

// serveHandler drives one request through a gin engine that carries a
// single route, so a case exercises a handler's decision order without an
// entry point.
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
