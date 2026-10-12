package admin

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The stubs implement the root interfaces and the handler-internal guard the
// module takes up, so a stub and an interface cannot drift apart without failing
// the build.
var (
	_ authz.Manager      = (*stubManager)(nil)
	_ authz.Catalog      = (*stubCatalog)(nil)
	_ guard.Guard        = (*stubGuard)(nil)
	_ audit.Audit        = (*recordingAudit)(nil)
	_ speedhttp.Router   = (*stubRouter)(nil)
	_ speedhttp.Endpoint = (*stubEndpoint)(nil)
)

// managerCall is one call the stub manager recorded: the method's name and the
// arguments that name a row, so a case can hold what the handler passed on.
type managerCall struct {
	op      string
	tenant  string
	role    authz.Role
	account string
	grant   authz.GrantRecord
	binding authz.Binding
	roleRec authz.RoleRecord
}

// stubManager is a stand-in authz.Manager. It records every call, answers reads
// from what a case set, and fails every method with the same error when one is
// set, which is how a case drives a sentinel up the handler's mapping.
type stubManager struct {
	mu       sync.Mutex
	calls    []managerCall
	roles    []authz.RoleRecord
	grants   []authz.GrantRecord
	bindings []authz.Binding
	err      error
}

func (m *stubManager) record(call managerCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

func (m *stubManager) recorded() []managerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]managerCall(nil), m.calls...)
}

func (m *stubManager) fail(err error) { m.mu.Lock(); m.err = err; m.mu.Unlock() }

func (m *stubManager) errNow() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *stubManager) Roles(_ context.Context, tenant string) ([]authz.RoleRecord, error) {
	m.record(managerCall{op: "Roles", tenant: tenant})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	return append([]authz.RoleRecord(nil), m.roles...), nil
}

func (m *stubManager) Grants(_ context.Context, tenant string, role authz.Role) ([]authz.GrantRecord, error) {
	m.record(managerCall{op: "Grants", tenant: tenant, role: role})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	return append([]authz.GrantRecord(nil), m.grants...), nil
}

func (m *stubManager) Bindings(_ context.Context, tenant string) ([]authz.Binding, error) {
	m.record(managerCall{op: "Bindings", tenant: tenant})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	return append([]authz.Binding(nil), m.bindings...), nil
}

func (m *stubManager) BindingsOf(_ context.Context, tenant, account string) ([]authz.Binding, error) {
	m.record(managerCall{op: "BindingsOf", tenant: tenant, account: account})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	return append([]authz.Binding(nil), m.bindings...), nil
}

func (m *stubManager) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.record(managerCall{op: "PutRole", tenant: r.Tenant, role: r.Role, roleRec: r})
	return m.errNow()
}

func (m *stubManager) DeleteRole(_ context.Context, tenant string, role authz.Role) error {
	m.record(managerCall{op: "DeleteRole", tenant: tenant, role: role})
	return m.errNow()
}

func (m *stubManager) PutGrant(_ context.Context, g authz.GrantRecord) error {
	m.record(managerCall{op: "PutGrant", tenant: g.Tenant, role: g.Role, grant: g})
	return m.errNow()
}

func (m *stubManager) DeleteGrant(_ context.Context, g authz.GrantRecord) error {
	m.record(managerCall{op: "DeleteGrant", tenant: g.Tenant, role: g.Role, grant: g})
	return m.errNow()
}

func (m *stubManager) PutBinding(_ context.Context, b authz.Binding) error {
	m.record(managerCall{op: "PutBinding", tenant: b.Tenant, role: b.Role, account: b.Account, binding: b})
	return m.errNow()
}

func (m *stubManager) DeleteBinding(_ context.Context, tenant, account string, role authz.Role) error {
	m.record(managerCall{op: "DeleteBinding", tenant: tenant, account: account, role: role})
	return m.errNow()
}

// stubCatalog is a stand-in authz.Catalog over a fixed set of permissions. It
// records every Has query, so a case can hold that the grant route checked the
// catalog before it called the manager.
type stubCatalog struct {
	mu    sync.Mutex
	perms []authz.Permission
	set   map[authz.Permission]bool
	has   []authz.Permission
}

func newStubCatalog(perms ...authz.Permission) *stubCatalog {
	set := make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return &stubCatalog{perms: append([]authz.Permission(nil), perms...), set: set}
}

func (c *stubCatalog) Declared() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.perms...)
}

func (c *stubCatalog) Has(p authz.Permission) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.has = append(c.has, p)
	return c.set[p]
}

func (c *stubCatalog) asked() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.has...)
}

// stubGuard is a stand-in guard.Guard. It records the permission every decision
// asked about and answers from one setting: allow, deny, or fail. The write-out
// matches the real guard's — a 403 for a denial and a 503 for an outage — so a
// handler that returns on a shortfall writes the same answer.
type stubGuard struct {
	mu    sync.Mutex
	allow bool
	err   error
	asked []authz.Permission
	// domains records the policy domain each decision was decided in, in step
	// with asked, so a case can hold that a route decided in the domain it is
	// meant to and not the request's own.
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

// recordingAudit keeps the rows it was asked to write and can be told to fail.
type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
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

// capturedRecord is one log record a case reads back.
type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// capturedLogs is a slog.Handler that keeps every record with its attributes, so
// a case can read the entry point's own listening record off it.
type capturedLogs struct {
	mu      *sync.Mutex
	records *[]capturedRecord
	bound   []slog.Attr
}

func newCapturedLogs() *capturedLogs {
	return &capturedLogs{mu: &sync.Mutex{}, records: &[]capturedRecord{}}
}

func (c *capturedLogs) Enabled(context.Context, slog.Level) bool { return true }

func (c *capturedLogs) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(c.bound)+rec.NumAttrs())
	for _, a := range c.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.records = append(*c.records, capturedRecord{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (c *capturedLogs) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &capturedLogs{mu: c.mu, records: c.records, bound: append(append([]slog.Attr(nil), c.bound...), attrs...)}
}

func (c *capturedLogs) WithGroup(string) slog.Handler { return c }

// address returns the bound address of an endpoint, read off the entry point's
// own listening record, or the empty string when none was written yet.
func (c *capturedLogs) address(endpoint string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range *c.records {
		if rec.msg == "an HTTP endpoint is listening" && rec.attrs["endpoint"] == endpoint {
			return rec.attrs["address"]
		}
	}
	return ""
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
