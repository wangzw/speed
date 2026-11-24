package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

var (
	_ tenant.Catalog                  = (*stubCatalog)(nil)
	_ guard.Guard                     = (*stubGuard)(nil)
	_ audit.Audit                     = (*recordingAudit)(nil)
	_ authn.SignupVerification        = (*fakeSignupVerification)(nil)
	_ speedhttp.Router                = (*stubRouter)(nil)
	_ speedhttp.Endpoint              = (*stubEndpoint)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*permissiveRouteGuard)(nil)
	_ log.Logger                      = loggerStub{}
)

type catalogCall struct {
	op      string
	id      tenant.ID
	profile tenant.Profile
}

type stubCatalog struct {
	mu         sync.Mutex
	calls      []catalogCall
	held       tenant.Tenant
	found      bool
	rows       []tenant.Tenant
	err        error
	getErr     error
	profileErr error
	order      *[]string
}

func (s *stubCatalog) record(call catalogCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubCatalog) recorded() []catalogCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]catalogCall(nil), s.calls...)
}

func (s *stubCatalog) errNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stubCatalog) errOr(specific error) error {
	if specific != nil {
		return specific
	}
	return s.errNow()
}

func (s *stubCatalog) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	s.record(catalogCall{op: "Get", id: id})
	if err := s.errOr(s.getErr); err != nil {
		return tenant.Tenant{}, false, err
	}
	s.mu.Lock()
	rows := append([]tenant.Tenant(nil), s.rows...)
	held, found := s.held, s.found
	s.mu.Unlock()
	if rows != nil {
		for _, row := range rows {
			if row.ID == id {
				return row, true, nil
			}
		}
		return tenant.Tenant{}, false, nil
	}
	return held, found, nil
}

func (s *stubCatalog) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	s.record(catalogCall{op: "ByName"})
	if err := s.errNow(); err != nil {
		return tenant.Tenant{}, false, err
	}
	s.mu.Lock()
	rows := append([]tenant.Tenant(nil), s.rows...)
	s.mu.Unlock()
	for _, row := range rows {
		if row.Name == name {
			return row, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

func (s *stubCatalog) Put(context.Context, tenant.Tenant) error {
	return errors.New("the profile surface must not write through Put")
}

func (s *stubCatalog) Create(context.Context, tenant.Tenant) error {
	return errors.New("the profile surface must not register a tenant")
}

func (s *stubCatalog) SetStatus(context.Context, tenant.ID, tenant.Status) error {
	return errors.New("the profile surface must not change a status")
}

func (s *stubCatalog) SetStatusByAccount(context.Context, tenant.ID, tenant.Status) error {
	return errors.New("the profile surface must not change a status by account")
}

func (s *stubCatalog) Delete(context.Context, tenant.ID) error {
	return errors.New("the profile surface must not delete a tenant")
}

func (s *stubCatalog) SetOwner(context.Context, tenant.ID, string) error {
	return errors.New("the profile surface must not transfer a tenant's owner")
}

func (s *stubCatalog) SetOwnerIfEmpty(context.Context, tenant.ID, string) (bool, error) {
	return false, errors.New("the profile surface must not repair a tenant's owner")
}

func (s *stubCatalog) SetProfile(_ context.Context, id tenant.ID, profile tenant.Profile) error {
	s.record(catalogCall{op: "SetProfile", id: id, profile: profile})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.order != nil {
		*s.order = append(*s.order, "SetProfile")
	}
	if s.profileErr != nil {
		return s.profileErr
	}
	return s.err
}

func (s *stubCatalog) List(context.Context) ([]tenant.Tenant, error) {
	return nil, errors.New("the profile surface must not list the roster")
}

func (s *stubCatalog) Enter(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, nil
}

func (s *stubCatalog) EnterRegistered(ctx context.Context, _ tenant.ID) (context.Context, error) {
	return ctx, nil
}

type stubGuard struct {
	mu      sync.Mutex
	allow   bool
	err     error
	asked   []authz.Permission
	domains []authz.Domain
}

func (g *stubGuard) note(p authz.Permission, domain authz.Domain) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, p)
	g.domains = append(g.domains, domain)
}

func (g *stubGuard) answer() (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.allow, g.err
}

func (g *stubGuard) Allowed(_ *gin.Context, perm authz.Permission) (bool, error) {
	g.note(perm, "")
	return g.answer()
}

func (g *stubGuard) refuse(c *gin.Context) bool {
	allow, err := g.answer()
	switch {
	case err != nil:
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusServiceUnavailable})
		return false
	case !allow:
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
		return false
	}
	return true
}

func (g *stubGuard) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	g.note(perm, "")
	return g.refuse(c)
}

func (g *stubGuard) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	g.note(perm, domain)
	return g.refuse(c)
}

func (g *stubGuard) askedFor() []authz.Permission {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]authz.Permission(nil), g.asked...)
}

func (g *stubGuard) domainsAsked() []authz.Domain {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]authz.Domain(nil), g.domains...)
}

type recordingAudit struct {
	mu    sync.Mutex
	rows  []audit.Entry
	err   error
	order *[]string
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, e)
	if a.order != nil {
		*a.order = append(*a.order, "audit")
	}
	return nil
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fail(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

type fakeSignupVerification struct {
	mu     sync.Mutex
	state  authn.SignupState
	err    error
	reads  int
	lastID authn.AccountID
}

func (v *fakeSignupVerification) SignupStateOf(_ context.Context, id authn.AccountID) (authn.SignupStatus, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	v.lastID = id
	return authn.SignupStatus{State: v.state}, v.err
}

// PendingAccountByContact is not part of this surface.
func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("tenant.profile: the pending-contact read is not part of this surface")
}

func (v *fakeSignupVerification) answer(state authn.SignupState, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.err = state, err
}

func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

type permissiveRouteGuard struct{}

func (permissiveRouteGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc { return next }

type mountedRoute struct {
	method  string
	path    string
	handler gin.HandlerFunc
}

type stubEndpoint struct {
	mu     sync.Mutex
	routes []mountedRoute
}

func (e *stubEndpoint) Route(method, path string, handlers ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	handler := gin.HandlerFunc(func(*gin.Context) {})
	if len(handlers) > 0 {
		handler = handlers[len(handlers)-1]
	}
	e.routes = append(e.routes, mountedRoute{method: method, path: path, handler: handler})
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

type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func decode(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding the response %q: %v", w.Body.String(), err)
	}
}

type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

func problemOf(t *testing.T, w *httptest.ResponseRecorder) problem {
	t.Helper()
	var p problem
	decode(t, w, &p)
	return p
}

func standIn(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type rig struct {
	t        *testing.T
	p        *product
	catalog  *stubCatalog
	guard    *stubGuard
	audit    *recordingAudit
	router   *stubRouter
	logs     *logBuffer
	engine   *gin.Engine
	signup   *fakeSignupVerification
	tenantID string
	order    *[]string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	order := &[]string{}
	logs := &logBuffer{}
	r := &rig{
		t:        t,
		catalog:  &stubCatalog{found: true, held: tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}, order: order},
		guard:    &stubGuard{allow: true},
		audit:    &recordingAudit{order: order},
		logs:     logs,
		signup:   &fakeSignupVerification{},
		tenantID: "acme",
		order:    order,
	}
	r.p = &product{
		catalog:      r.catalog,
		guard:        r.guard,
		audit:        r.audit,
		verification: r.signup,
		routeGuard:   permissiveRouteGuard{},
		kind:         kindStub{},
		logger:       logs.logger(),
		cfg:          resolved{endpoints: []string{"api"}},
	}
	r.mount()
	return r
}

func (r *rig) mount() {
	r.t.Helper()
	r.router = newStubRouter("api")
	if err := r.p.mount(r.router); err != nil {
		r.t.Fatalf("mounting the routes: %v", err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, rt := range r.router.endpoint("api").registered() {
		engine.Handle(rt.method, rt.path, rt.handler)
	}
	r.engine = engine
}

func (r *rig) do(method, target string, body any, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			r.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	ctx := authntest.Context(r.t, context.Background(), opts...)
	ctx = tenantctx.WithID(ctx, r.tenantID)
	return r.send(method, target, reader, body != nil, ctx)
}

func (r *rig) doRaw(method, target, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Context(r.t, context.Background())
	ctx = tenantctx.WithID(ctx, r.tenantID)
	return r.send(method, target, strings.NewReader(raw), true, ctx)
}

func (r *rig) doAnonymous(method, target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
	ctx = tenantctx.WithID(ctx, r.tenantID)
	return r.send(method, target, nil, false, ctx)
}

func (r *rig) send(method, target string, reader io.Reader, jsonBody bool, ctx context.Context) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(method, target, reader)
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, req)
	return w
}

var sampleCreatedAt = time.Date(2026, time.November, 5, 9, 30, 0, 0, time.UTC)

func sampleTenant() tenant.Tenant {
	return tenant.Tenant{
		ID:           "acme",
		Name:         "acme",
		Status:       tenant.StatusActive,
		OwnerAccount: "acct-owner",
		Profile: tenant.Profile{
			DisplayName:  "Acme Corporation",
			Description:  "A maker of things.",
			ContactEmail: "ops@acme.test",
			Website:      "https://acme.test",
		},
		CreatedAt: sampleCreatedAt,
	}
}

const profileTarget = "/api/v1/tenants/acme/profile"

type kindStub struct{ personal map[tenant.ID]bool }

func (k kindStub) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if k.personal[id] {
		return tenant.KindPersonal, nil
	}
	return tenant.KindBusiness, nil
}
