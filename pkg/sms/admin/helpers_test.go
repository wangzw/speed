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
	"github.com/vislake/speed/pkg/sms"
)

var (
	_ sms.Registry       = (*stubRegistry)(nil)
	_ sms.Provider       = (*stubProvider)(nil)
	_ guard.Guard        = (*stubGuard)(nil)
	_ audit.Audit        = (*recordingAudit)(nil)
	_ speedhttp.Router   = (*stubRouter)(nil)
	_ speedhttp.Endpoint = (*stubEndpoint)(nil)
	_ log.Logger         = loggerStub{}
)

type registryCall struct {
	op      string
	name    string
	spec    sms.TemplateSpec
	offer   sms.Submission
	enabled bool
}

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
	delay time.Duration
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	time.Sleep(a.delay)
	a.rows = append(a.rows, e)
	return nil
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func (a *recordingAudit) fail(err error) { a.mu.Lock(); a.err = err; a.mu.Unlock() }

func (a *recordingAudit) slow(d time.Duration) { a.mu.Lock(); a.delay = d; a.mu.Unlock() }

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

func (r *rig) mount() *gin.Engine {
	r.t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, rt := range r.p.routes() {
		engine.Handle(rt.method, rt.path, withRealm("public", rt.handler))
	}
	return engine
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
	return r.send(method, target, reader, body != nil, authntest.Context(r.t, context.Background(), opts...))
}

func (r *rig) doRaw(method, target, raw string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(method, target, strings.NewReader(raw), true, authntest.Context(r.t, context.Background()))
}

func (r *rig) doAnonymous(method, target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
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
