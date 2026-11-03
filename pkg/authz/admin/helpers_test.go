package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

// The stubs implement the root interfaces and the handler-internal guard the
// module takes up, so a stub and an interface cannot drift apart without failing
// the build.
var (
	_ authz.Manager      = (*stubManager)(nil)
	_ authz.Catalog      = (*stubCatalog)(nil)
	_ authz.WriteGuard   = (*stubWriteGuard)(nil)
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

func (m *stubManager) MarkRoleBuiltin(_ context.Context, tenant string, role authz.Role) error {
	m.record(managerCall{op: "MarkRoleBuiltin", tenant: tenant, role: role})
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

func (m *stubManager) ClearAccountBindings(_ context.Context, tenant, account string) error {
	m.record(managerCall{op: "ClearAccountBindings", tenant: tenant, account: account})
	return m.errNow()
}

// writeGuardCall is one question the stand-in write guard was asked: the tenant
// the write named and the kind of write it was.
type writeGuardCall struct {
	tenant string
	op     authz.WriteOp
}

// stubWriteGuard is a stand-in authz.WriteGuard. Its zero value permits every
// write and records what it was asked, so a case can hold both that a route
// consults the seam and what the route named. A case refuses a write with
// refuse and breaks the answer with fail.
type stubWriteGuard struct {
	mu     sync.Mutex
	calls  []writeGuardCall
	denied bool
	err    error
}

func (g *stubWriteGuard) Denied(_ context.Context, tenant string, op authz.WriteOp) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, writeGuardCall{tenant: tenant, op: op})
	return g.denied, g.err
}

// asked returns the questions the guard recorded.
func (g *stubWriteGuard) asked() []writeGuardCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]writeGuardCall(nil), g.calls...)
}

// refuse makes the guard deny every write.
func (g *stubWriteGuard) refuse() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied = true
}

// fail makes the guard unable to answer.
func (g *stubWriteGuard) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

// stubCatalog is a stand-in authz.Catalog over a fixed set of permissions. It
// records every Has query, so a case can hold that the grant route checked the
// catalog before it called the manager.
type stubCatalog struct {
	mu          sync.Mutex
	perms       []authz.Permission
	set         map[authz.Permission]bool
	keys        map[authz.Permission]string
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	has         []authz.Permission
}

func newStubCatalog(perms ...authz.Permission) *stubCatalog {
	set := make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return &stubCatalog{
		perms:       append([]authz.Permission(nil), perms...),
		set:         set,
		keys:        make(map[authz.Permission]string),
		governedSet: make(map[authz.Permission]bool),
	}
}

// describe gives one declared permission the description key a declaring module
// would have put in its PermissionSpec.
func (c *stubCatalog) describe(p authz.Permission, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[p] = key
}

func (c *stubCatalog) Declared() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.perms...)
}

// TenantGoverned implements authz.Catalog.
func (c *stubCatalog) TenantGoverned() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.governed...)
}

// Governed implements authz.Catalog.
func (c *stubCatalog) Governed(p authz.Permission) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.governedSet[p] && c.set[p]
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

// ObjectAction answers from the process's translation, as the real catalog does:
// the pair a permission translates to is one thing for the whole process, and a
// case asserting against a table of its own would be asserting against a
// translation no decision is made by.
func (c *stubCatalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

// DescriptionKey answers the key a case gave a declared permission. A declared
// permission a case did not describe answers an empty key and true, which is the
// shape a module that left one permission out of DescriptionKeys declares.
func (c *stubCatalog) DescriptionKey(p authz.Permission) (key string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set[p] {
		return "", false
	}
	return c.keys[p], true
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
		if rec.msg == "an HTTP listener is listening" && rec.attrs["listener"] == endpoint {
			return rec.attrs["address"]
		}
	}
	return ""
}

// stubLocalizer renders a message from an in-memory index the way the engine
// does: it reads the locale from the context, falls back to a default when the
// context carries none or the locale misses the key, and writes the key itself
// back for a key no locale carries. That last rule is the engine's fallback, and
// a case holds the catalog route to it.
type stubLocalizer struct {
	index         map[i18n.Locale]map[i18n.Key]string
	defaultLocale i18n.Locale
}

func (s stubLocalizer) Localize(ctx context.Context, key i18n.Key, _ i18n.Args) string {
	locale, ok := i18n.FromContext(ctx)
	if !ok {
		locale = s.defaultLocale
	}
	if text, ok := s.index[locale][key]; ok {
		return text
	}
	if locale != s.defaultLocale {
		if text, ok := s.index[s.defaultLocale][key]; ok {
			return text
		}
	}
	return string(key)
}

func (s stubLocalizer) Translate(locale i18n.Locale, key i18n.Key, _ i18n.Args) (string, bool) {
	text, ok := s.index[locale][key]
	return text, ok
}

var _ i18n.Localizer = stubLocalizer{}

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
	return "", false, errors.New("authz.admin: the pending-contact read is not part of this surface")
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
