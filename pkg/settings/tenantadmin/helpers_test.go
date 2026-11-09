package tenantadmin

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
	"reflect"
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
	"github.com/vislake/speed/pkg/settings"
)

// The stubs implement the seams the module takes up, so a stub and an interface
// cannot drift apart without failing the build.
var (
	_ settings.Store                  = (*stubStore)(nil)
	_ guard.Guard                     = (*stubGuard)(nil)
	_ audit.Audit                     = (*recordingAudit)(nil)
	_ authz.WriteSettings             = (*stubWriteSettings)(nil)
	_ speedhttp.Router                = (*stubRouter)(nil)
	_ speedhttp.Endpoint              = (*stubEndpoint)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*stubRouteGuard)(nil)
	_ log.Logger                      = loggerStub{}
)

// base is the tenant surface's base path, with the tenant the cases address.
const base = "/api/v1/tenants/acme/settings"

// decl builds a declared item the shape Declared reports: the full key, the
// module that declared it, the declared type, the default's text and the
// declaration's metadata.
func decl(key, module string, typ reflect.Type, def string, item settings.Item) settings.DeclaredItem {
	return settings.DeclaredItem{Key: key, Module: module, Type: typ, Default: def, Item: item}
}

// storeCall is one call the stub store recorded: the method's name, the layer
// and key it named, and the value a write carried, so a case holds what the
// handler passed on and that an action the module refused never reached the
// store.
type storeCall struct {
	op    string
	layer settings.Layer
	key   string
	value any
	actor string
}

// stubStore is a stand-in settings.Store. It answers reads from what a case set,
// fails the methods a case told it to, and records every call.
type stubStore struct {
	mu            sync.Mutex
	calls         []storeCall
	declared      []settings.DeclaredItem
	rows          []settings.Entry
	values        map[string]any
	listErr       error
	globalListErr error
	getErr        error
	setErr        error
	deleteErr     error
}

func (s *stubStore) record(call storeCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubStore) recorded() []storeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storeCall(nil), s.calls...)
}

// ops lists the operations a case's store was asked to do, in order.
func (s *stubStore) ops() []string {
	calls := s.recorded()
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.op
	}
	return names
}

// hasWriteOp reports whether the store recorded an operation named op. A write
// case uses it because the handler may read the global layer first to judge
// platform enforcement, so the write is not necessarily the first call.
func hasWriteOp(calls []storeCall, op string) bool {
	for _, c := range calls {
		if c.op == op {
			return true
		}
	}
	return false
}

// holdsLayerKey reports whether the store was ever asked to act on this layer
// and key with this operation.
func (s *stubStore) holdsLayerKey(op string, layer settings.Layer, key string) bool {
	for _, c := range s.recorded() {
		if c.op == op && c.layer == layer && c.key == key {
			return true
		}
	}
	return false
}

func (s *stubStore) Get(layer settings.Layer, key string, target any) error {
	s.record(storeCall{op: "Get", layer: layer, key: key})
	s.mu.Lock()
	readErr := s.getErr
	value, ok := s.values[key]
	s.mu.Unlock()
	if readErr != nil {
		return readErr
	}
	if !ok {
		return nil
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

func (s *stubStore) Set(_ context.Context, w settings.Write) error {
	s.record(storeCall{op: "Set", layer: w.Layer, key: w.Key, value: w.Value, actor: w.Actor})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setErr
}

func (s *stubStore) Delete(_ context.Context, layer settings.Layer, key, actor string) error {
	s.record(storeCall{op: "Delete", layer: layer, key: key, actor: actor})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteErr
}

// List returns the rows of the layer named. The stub matches a layer the way the
// settings module does: the global layer is the rows whose tenant column is
// empty, a tenant layer the rows whose column is that tenant.
func (s *stubStore) List(_ context.Context, layer settings.Layer) ([]settings.Entry, error) {
	s.record(storeCall{op: "List", layer: layer})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	if layer == settings.Global() && s.globalListErr != nil {
		return nil, s.globalListErr
	}
	var out []settings.Entry
	for _, row := range s.rows {
		if row.Tenant == "" {
			if layer == settings.Global() {
				out = append(out, row)
			}
			continue
		}
		if layer == settings.ForTenant(row.Tenant) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *stubStore) Declared() []settings.DeclaredItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]settings.DeclaredItem(nil), s.declared...)
}

func (s *stubStore) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

// Read answers from the stub's Get and WrittenAt. The surfaces here never call
// it; it is present because Store carries it.
func (s *stubStore) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		at, present, err := s.WrittenAt(read.Layer, key)
		if err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{WrittenAt: at, Present: present}
	}
	return results, nil
}

// stubGuard is a stand-in guard.Guard. It records the permission every decision
// asked about and the domain it decided in, and answers from one setting: allow,
// deny, or fail. The write-out matches the real guard's — a 403 for a denial and
// a 503 for an outage — so a handler that returns on a shortfall writes the same
// answer.
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

// recordingAudit keeps the rows it was asked to write and can be told to fail,
// which is how a case holds that a write whose mandatory row did not land
// reached no settings call.
type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, e)
	return nil
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fail(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

// stubWriteSettings is the host's personal-tenant write seam. Its zero value
// admits every tenant; a case scripts a refusal, a read failure, or both, and
// reads back the tenants it was asked about.
type stubWriteSettings struct {
	mu     sync.Mutex
	denied bool
	err    error
	calls  []string
}

func (w *stubWriteSettings) Denied(_ context.Context, tenant string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, tenant)
	return w.denied, w.err
}

func (w *stubWriteSettings) refuse() { w.mu.Lock(); w.denied = true; w.mu.Unlock() }

func (w *stubWriteSettings) fail(err error) { w.mu.Lock(); w.err = err; w.mu.Unlock() }

func (w *stubWriteSettings) askedTenants() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

// stubRouteGuard is a stand-in for the tenant administration guard the tenant
// capability delivers. It passes every request through: the cases that mount
// the routes directly exercise the handler, and the guard's own rule is the
// tenant capability's case. The mount still requires one, which is why it is
// here. The shared guard is recorded separately so a case can hold that this
// surface applies the stricter one.
type stubRouteGuard struct {
	mu     sync.Mutex
	admin  int
	shared int
}

// Guard is the shared identity guard. The tenant settings surface must not use
// it, because it would leave the platform domain addressable.
func (g *stubRouteGuard) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.shared++
	g.mu.Unlock()
	return next
}

func (g *stubRouteGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.admin++
	g.mu.Unlock()
	return next
}

func (g *stubRouteGuard) adminCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.admin
}

func (g *stubRouteGuard) sharedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shared
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

// stubRouter is a stand-in speedhttp.Router over a fixed set of endpoints. A
// name it does not hold is an error, the way the real entry point answers an
// undeclared endpoint.
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

// loggerStub delivers the logging capability over a recorder, so the module's
// diagnostic lands in a case's buffer rather than on the test's output.
type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes redaction registrations and masks nothing: a case reads the
// records themselves, not their masking.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// logBuffer collects what the module logs, so a case can hold the module's own
// records apart from the test's output.
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

// decode reads a JSON response body into target.
func decode(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("decoding the response %q: %v", w.Body.String(), err)
	}
}

// problem is the default problem-details shape this module's refusals carry.
type problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
}

// problemOf reads the problem a recorder's body holds.
func problemOf(t *testing.T, w *httptest.ResponseRecorder) problem {
	t.Helper()
	var p problem
	decode(t, w, &p)
	return p
}

// standIn is a module that delivers instance for the tokens named, the way
// another module delivers its capability exclusively.
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

// rig is the module with every dependency a case drives, mounted on a real gin
// engine so a case reaches it over a real HTTP surface: the real routing, the
// real path parameters and the real JSON binding, with the settings surface and
// the other capabilities stood in for.
type rig struct {
	t         *testing.T
	p         *product
	store     *stubStore
	guard     *stubGuard
	audit     *recordingAudit
	writeSeam *stubWriteSettings
	logs      *logBuffer
	engine    *gin.Engine
	// signup answers the acting account's self-registration state. Its zero
	// value reports SignupNone, which is what an account created outside
	// self-registration reads as.
	signup *fakeSignupVerification
}

// newRig builds the module over the stubs and mounts its route set on a real
// engine.
func newRig(t *testing.T) *rig {
	t.Helper()
	logs := &logBuffer{}
	r := &rig{
		t:         t,
		store:     &stubStore{values: map[string]any{}},
		guard:     &stubGuard{allow: true},
		audit:     &recordingAudit{},
		writeSeam: &stubWriteSettings{},
		logs:      logs,
		signup:    &fakeSignupVerification{},
	}
	r.p = &product{
		store:         r.store,
		guard:         r.guard,
		audit:         r.audit,
		verification:  r.signup,
		writeSettings: r.writeSeam,
		names:         identityNames{},
		logger:        logs.logger(),
		cfg:           resolved{endpoints: []string{"api"}},
	}
	r.engine = r.mount()
	return r
}

// mount registers the module's whole route set on a fresh gin engine, the way
// the real entry point mounts them, and binds the realm each handler reads. A
// conflict among the routes panics here, which is how a case holds that the
// route set is mountable.
func (r *rig) mount() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, rt := range r.p.routes() {
		engine.Handle(rt.method, rt.path, withRealm("api", rt.handler))
	}
	return engine
}

// do sends one request through the engine with the identity the options build
// and, when a body is given, a JSON body.
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
	return r.send(method, target, reader, body != nil, authntest.Context(r.t, context.Background(), opts...))
}

// doRaw sends a body as written, so a case can put a body the handler cannot
// read in front of it.
func (r *rig) doRaw(method, target, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(method, target, strings.NewReader(raw), true, authntest.Context(r.t, context.Background()))
}

// doAnonymous sends one request with no identity on it, an optional-access
// anonymous request, the way one reaches a management route the middleware let
// through.
func (r *rig) doAnonymous(method, target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
	return r.send(method, target, nil, false, ctx)
}

// send builds the request, installs the context and drives the engine.
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

// fakeSignupVerification answers the acting account's self-registration state
// with what a case scripts, and counts the reads, so a case can hold that a
// granted reader was not asked. The zero value reports SignupNone with no
// failure, which is what an account created outside self-registration reads as.
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
	return "", false, errors.New("settings.tenantadmin: the pending-contact read is not part of this surface")
}

// answer scripts the state and the failure the next reads report.
func (v *fakeSignupVerification) answer(state authn.SignupState, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.state, v.err = state, err
}

// readCount is how many state reads the fake answered.
func (v *fakeSignupVerification) readCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reads
}

// lastAccount is the account the last state read named, which a case holds to
// the request's actor rather than its impersonation target.
func (v *fakeSignupVerification) lastAccount() authn.AccountID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastID
}

// identityNames stands in for the roster's id-to-name seam over fixtures
// whose identifiers double as their names.
type identityNames struct{}

func (identityNames) TenantNameOf(_ context.Context, id string) (string, error) {
	return id, nil
}

// nameStub answers the names a case maps and keeps the identifier as the
// name of every row the map does not name.
type nameStub struct{ byID map[string]string }

func (a nameStub) TenantNameOf(_ context.Context, id string) (string, error) {
	if name, ok := a.byID[id]; ok {
		return name, nil
	}
	return id, nil
}
