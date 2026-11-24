package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
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
		locate:   func(t *testing.T) string { return filepath.Join(t.TempDir(), "inbox.db") },
	},
}

type host struct {
	database core.Module
	section  string
	dsn      string
	bus      bool
	inbox    map[string]any
	extra    []core.Module
	depth    core.Stage
}

func (e engine) on(t *testing.T, h host) host {
	h.database = e.database
	h.section = e.section
	h.dsn = e.locate(t)
	return h
}

func (h host) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	dir := t.TempDir()
	inboxSection := h.inbox
	if inboxSection == nil {
		inboxSection = map[string]any{}
	}
	document := map[string]any{
		"db": map[string]any{h.section: map[string]any{"dsn": h.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")},
		}},
		"notification": map[string]any{"inbox": inboxSection},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}

	modules := []core.Module{h.database, log.Module(), catalog.Module(), Module()}
	if h.bus {
		modules = append(modules, memory.Module())
	}
	modules = append(modules, h.extra...)

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDINBOXTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
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
	reg.Supply(reader, config.ModuleName)
	depth := h.depth
	if depth == 0 {
		depth = core.StageStart
	}
	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        depth,
		Participants: names,
		Action:       action,
	}); err != nil && !errors.Is(err, errCaseFailed) {
		t.Fatalf("driving the assembly: %v", err)
	}
}

var errCaseFailed = errors.New("the case failed")

func forEachEngine(t *testing.T, run func(t *testing.T, e engine)) {
	t.Helper()
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) { run(t, e) })
	}
}

func inboxOf(t *testing.T, reg *core.Registry) notification.Inbox {
	t.Helper()
	face, err := core.Resolve[notification.Inbox](reg)
	if err != nil {
		t.Fatalf("resolving the inbox: %v", err)
	}
	return face
}

func product(t *testing.T, reg *core.Registry) *inboxStore {
	t.Helper()
	face := inboxOf(t, reg)
	s, ok := face.(*inboxStore)
	if !ok {
		t.Fatalf("the inbox is a %T, not this module's product", face)
	}
	return s
}

func tenantCtx(t *testing.T, reg *core.Registry, id string) context.Context {
	t.Helper()
	cam, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		t.Fatalf("resolving the tenant roster: %v", err)
	}
	if putErr := cam.Put(t.Context(), tenant.Tenant{
		ID: tenant.ID(id), Name: id, Status: tenant.StatusActive,
	}); putErr != nil {
		t.Fatalf("registering the tenant %q: %v", id, putErr)
	}
	ctx, err := cam.Enter(t.Context(), tenant.ID(id))
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return ctx
}

func rowCount(t *testing.T, s *inboxStore) int64 {
	t.Helper()
	var row struct {
		N int64 `gorm:"column:n"`
	}
	if err := s.handle.Raw("SELECT count(*) AS n FROM " + tableName).Scan(&row).Error; err != nil {
		t.Fatalf("counting the in-app messages: %v", err)
	}
	return row.N
}

type storedRow struct {
	TenantID        string `gorm:"column:tenant_id"`
	ID              string `gorm:"column:id"`
	RecipientUserID string `gorm:"column:recipient_user_id"`
	TypeKey         string `gorm:"column:type_key"`
	Group           string `gorm:"column:group"`
	ReadAt          *int64 `gorm:"column:read_at"`
	ExpiresAt       *int64 `gorm:"column:expires_at"`
	CreatedAt       int64  `gorm:"column:created_at"`
	DedupeKey       string `gorm:"column:dedupe_key"`
}

func rowsOf(t *testing.T, s *inboxStore) map[string]storedRow {
	t.Helper()
	var raw []storedRow
	err := s.handle.
		Raw(`SELECT tenant_id, id, recipient_user_id, type_key, "group", read_at, expires_at, created_at, dedupe_key FROM ` + tableName).
		Scan(&raw).Error
	if err != nil {
		t.Fatalf("reading the in-app messages back: %v", err)
	}
	out := make(map[string]storedRow, len(raw))
	for _, r := range raw {
		out[r.ID] = r
	}
	return out
}

// plant writes one row directly, the way another writer or a later build would,
// so a case can steer expires_at, read_at and created_at. It writes through a
// raw statement, which the tenant filter leaves alone.
func plant(t *testing.T, s *inboxStore, tenantID string, r message) {
	t.Helper()
	if r.Params == "" {
		r.Params = "null"
	}
	if r.DedupeKey == "" {
		// The dedupe key column is not null and unique; a planted row carries a
		// key of its own so two planted rows never collide on it.
		r.DedupeKey = "plant:" + r.ID
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = 1
	}
	if r.UpdatedAt == 0 {
		r.UpdatedAt = r.CreatedAt
	}
	err := s.handle.Exec(
		`INSERT INTO `+tableName+` (tenant_id, id, recipient_user_id, type_key, "group", title, body, params, link, `+
			`dedupe_key, expires_at, read_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, r.ID, r.RecipientUserID, r.TypeKey, r.Group, r.Title, r.Body, r.Params, r.Link,
		r.DedupeKey, r.ExpiresAt, r.ReadAt, r.CreatedAt, r.UpdatedAt).Error
	if err != nil {
		t.Fatalf("planting the row %q: %v", r.ID, err)
	}
}

type sink struct {
	mu       sync.Mutex
	received []event.Event
}

func (s *sink) module() core.Module {
	return core.Module{
		Name:     "test.sink",
		Requires: []core.Requirement{{Token: (*event.Bus)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return s, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe(EventInboxCreated, "notification.inbox.test", func(_ context.Context, evt event.Event) error {
				s.mu.Lock()
				s.received = append(s.received, evt)
				s.mu.Unlock()
				return nil
			})
			return nil
		},
	}
}

func (s *sink) events() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.received...)
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
		return nil, fmt.Errorf("the entry point declares no endpoint %q", name)
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

func standIn(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// engineFor builds a fresh gin engine with the module's whole route set mounted
// the way the real entry point mounts them. A conflict among the routes panics
// here, which is how a case holds that the route set is mountable.
func engineFor(t *testing.T, s *inboxStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, r := range s.routes() {
		engine.Handle(r.method, r.path, r.handler)
	}
	return engine
}

func requestContext(t *testing.T, base context.Context, opts ...authntest.Option) context.Context {
	t.Helper()
	return authntest.Context(t, base, opts...)
}
