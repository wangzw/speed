package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

// The stubs implement the seams the module takes up, so a stub and an interface
// cannot drift apart without failing the build.
var (
	_ journal.Reader     = (*stubReader)(nil)
	_ guard.Guard        = (*stubGuard)(nil)
	_ speedhttp.Router   = (*stubRouter)(nil)
	_ speedhttp.Endpoint = (*stubEndpoint)(nil)
	_ log.Logger         = loggerStub{}
)

type readerCall struct {
	q   journal.Query
	ctx context.Context
}

type stubPage struct {
	rows []journal.Row
	next string
	err  error
}

type stubReader struct {
	mu    sync.Mutex
	calls []readerCall
	rows  []journal.Row
	next  string
	err   error
	pages []stubPage
	at    int
}

// script makes the reader answer the given pages in order. A read past the last
// scripted page answers an empty last page, so a loop that reads one page too
// many ends rather than blocking.
func (s *stubReader) script(pages ...stubPage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages = pages
	s.at = 0
}

func (s *stubReader) record(call readerCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubReader) recorded() []readerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]readerCall(nil), s.calls...)
}

func (s *stubReader) fail(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *stubReader) Query(ctx context.Context, q journal.Query) ([]journal.Row, string, error) {
	s.record(readerCall{q: q, ctx: ctx})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pages != nil {
		if s.at >= len(s.pages) {
			return nil, "", nil
		}
		page := s.pages[s.at]
		s.at++
		return append([]journal.Row(nil), page.rows...), page.next, page.err
	}
	if s.err != nil {
		return nil, "", s.err
	}
	return append([]journal.Row(nil), s.rows...), s.next, nil
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
		speedhttp.Fail(c, speedhttp.Problem{Status: 503})
		return false
	case !allow:
		speedhttp.Fail(c, speedhttp.Problem{Status: 403})
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

// noRules takes redaction registrations and masks nothing: a case reads the
// records themselves, not their masking.
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

const blankType = "about:blank"

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
	t      *testing.T
	p      *product
	reader *stubReader
	guard  *stubGuard
	router *stubRouter
	logs   *logBuffer
	engine *gin.Engine
	// reqCtx is the context the last request carried, so a case can hold that
	// the handler handed the read surface a context of its own rather than the
	// request's.
	reqCtx context.Context
}

func newRig(t *testing.T) *rig {
	t.Helper()
	logs := &logBuffer{}
	r := &rig{
		t:      t,
		reader: &stubReader{},
		guard:  &stubGuard{allow: true},
		router: newStubRouter("public"),
		logs:   logs,
	}
	r.p = &product{
		reader: r.reader,
		guard:  r.guard,
		names:  identityNames{},
		logger: logs.logger(),
		cfg:    resolved{endpoints: []string{"public"}},
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

func (r *rig) do(target string, opts ...authntest.Option) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.send(target, authntest.Context(r.t, context.Background(), opts...))
}

func (r *rig) doAnonymous(target string) *httptest.ResponseRecorder {
	r.t.Helper()
	ctx := authntest.Anonymous(r.t, context.Background(), authn.AccessOptional)
	return r.send(target, ctx)
}

func (r *rig) send(target string, ctx context.Context) *httptest.ResponseRecorder {
	r.t.Helper()
	r.reqCtx = ctx
	req := httptest.NewRequest("GET", target, strings.NewReader(""))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, req)
	return w
}

func (r *rig) server(t *testing.T, opts ...authntest.Option) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := authntest.Context(t, req.Context(), opts...)
		ctx = log.WithLogger(ctx, r.logs.logger())
		r.engine.ServeHTTP(w, req.WithContext(ctx))
	}))
	// A reply that abandons its connection makes net/http log the panic it
	// recovered; the case reads the module's own log instead.
	server.Config.ErrorLog = stdlog.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type identityNames struct{}

func (identityNames) NameOf(_ context.Context, id tenant.ID) (string, error) {
	return string(id), nil
}
