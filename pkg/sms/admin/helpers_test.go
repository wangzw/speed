package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

// The stubs implement the seams the module takes up, so a stub and an interface
// cannot drift apart without failing the build.
var (
	_ sms.Registry       = (*stubRegistry)(nil)
	_ sms.Provider       = (*stubProvider)(nil)
	_ guard.Guard        = (*stubGuard)(nil)
	_ audit.Audit        = (*recordingAudit)(nil)
	_ speedhttp.Router   = (*stubRouter)(nil)
	_ speedhttp.Endpoint = (*stubEndpoint)(nil)
	_ log.Logger         = loggerStub{}
)

// registryCall is one call the stub registry recorded: the method's name and the
// arguments that name a row, so a case can hold what the handler passed on and
// that an action the module refused never reached the registry.
type registryCall struct {
	op      string
	name    string
	spec    sms.TemplateSpec
	offer   sms.Submission
	enabled bool
}

// stubRegistry is a stand-in sms.Registry. It records every call, answers reads
// from what a case set, and fails every method with the error a case set, which
// is how a case drives a sentinel up the handler's mapping.
type stubRegistry struct {
	mu       sync.Mutex
	calls    []registryCall
	records  []sms.Record
	register sms.Record
	offer    sms.Record
	err      error
}

func (s *stubRegistry) record(call registryCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubRegistry) recorded() []registryCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]registryCall(nil), s.calls...)
}

func (s *stubRegistry) fail(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *stubRegistry) errNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stubRegistry) Declared() []sms.Template { return nil }

func (*stubRegistry) Resolve(sms.Message) (sms.Resolved, error) {
	return sms.Resolved{}, nil
}

func (s *stubRegistry) List(context.Context) ([]sms.Record, error) {
	s.record(registryCall{op: "List"})
	if err := s.errNow(); err != nil {
		return nil, err
	}
	return append([]sms.Record(nil), s.records...), nil
}

func (s *stubRegistry) Register(_ context.Context, name string, spec sms.TemplateSpec) (sms.Record, error) {
	s.record(registryCall{op: "Register", name: name, spec: spec})
	if err := s.errNow(); err != nil {
		return sms.Record{}, err
	}
	return s.register, nil
}

func (s *stubRegistry) Offer(_ context.Context, submission sms.Submission) (sms.Record, error) {
	s.record(registryCall{op: "Offer", name: submission.Name, offer: submission})
	if err := s.errNow(); err != nil {
		return sms.Record{}, err
	}
	return s.offer, nil
}

func (s *stubRegistry) SetEnabled(_ context.Context, name string, enabled bool) error {
	s.record(registryCall{op: "SetEnabled", name: name, enabled: enabled})
	return s.errNow()
}

func (s *stubRegistry) Remove(_ context.Context, name string) error {
	s.record(registryCall{op: "Remove", name: name})
	return s.errNow()
}

// stubProvider is a stand-in sms.Provider. It answers the name and the carrier
// model a case set, which is what the carrier route reads; the template
// operations are never called from the management surface, so they answer
// nothing and succeed.
type stubProvider struct {
	mu    sync.Mutex
	name  string
	model sms.CarrierModel
	calls int
}

func (s *stubProvider) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

func (s *stubProvider) Model() sms.CarrierModel {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.model
}

func (s *stubProvider) modeled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (*stubProvider) Offer(context.Context, sms.Submission) (string, error) { return "", nil }

func (*stubProvider) Approval(context.Context, string) (sms.Approval, error) {
	return sms.Approval{}, nil
}

func (*stubProvider) Retire(context.Context, string) error { return nil }

// stubGuard is a stand-in guard.Guard. It records the permission every decision
// asked about, and the domain it decided in, and answers from one setting:
// allow, deny, or fail. The write-out matches the real guard's — a 403 for a
// denial and a 503 for an outage — so a handler that returns on a shortfall
// writes the same answer.
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
// reached no registry.
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
// startup diagnostic lands in a case's buffer rather than on the test's output.
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
// real path parameters and the real JSON binding, with the registry and the
// other capabilities stood in for.
type rig struct {
	t        *testing.T
	p        *product
	registry *stubRegistry
	provider *stubProvider
	guard    *stubGuard
	audit    *recordingAudit
	router   *stubRouter
	logs     *logBuffer
	engine   *gin.Engine
}

// newRig builds the module over the stubs and mounts its route set on a real
// engine, with one endpoint declared on the router.
func newRig(t *testing.T) *rig {
	t.Helper()
	logs := &logBuffer{}
	r := &rig{
		t:        t,
		registry: &stubRegistry{},
		provider: &stubProvider{name: "stub-carrier"},
		guard:    &stubGuard{allow: true},
		audit:    &recordingAudit{},
		router:   newStubRouter("public"),
		logs:     logs,
	}
	r.p = &product{
		registry: r.registry,
		provider: r.provider,
		guard:    r.guard,
		audit:    r.audit,
		logger:   logs.logger(),
		cfg:      resolved{endpoints: []string{"public"}},
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
		engine.Handle(rt.method, rt.path, withRealm("public", rt.handler))
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
