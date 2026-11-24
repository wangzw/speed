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

var (
	_ authz.Manager               = (*stubManager)(nil)
	_ authz.Catalog               = (*stubCatalog)(nil)
	_ authz.SubjectPermissions    = (*stubSubjectPermissions)(nil)
	_ authz.AccountIdentityReader = (*stubIdentityReader)(nil)
	_ authz.WriteGuard            = (*stubWriteGuard)(nil)
	_ guard.Guard                 = (*stubGuard)(nil)
	_ audit.Audit                 = (*recordingAudit)(nil)
	_ speedhttp.Router            = (*stubRouter)(nil)
	_ speedhttp.Endpoint          = (*stubEndpoint)(nil)
)

type subjectCall struct {
	domain  authz.Domain
	subject string
}

type stubSubjectPermissions struct {
	mu    sync.Mutex
	held  []authz.Permission
	err   error
	calls []subjectCall
}

func (s *stubSubjectPermissions) Held(_ context.Context, domain authz.Domain, subject string) ([]authz.Permission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, subjectCall{domain: domain, subject: subject})
	if s.err != nil {
		return nil, s.err
	}
	return append([]authz.Permission(nil), s.held...), nil
}

func (s *stubSubjectPermissions) answer(held ...authz.Permission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held = append([]authz.Permission(nil), held...)
}

func (s *stubSubjectPermissions) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *stubSubjectPermissions) asked() []subjectCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]subjectCall(nil), s.calls...)
}

type identityCall struct {
	tenant   string
	accounts []string
}

type stubIdentityReader struct {
	mu         sync.Mutex
	identities map[string]authz.AccountIdentity
	err        error
	calls      []identityCall
}

func (r *stubIdentityReader) Identities(_ context.Context, tenant string, accounts []string) (map[string]authz.AccountIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, identityCall{tenant: tenant, accounts: append([]string(nil), accounts...)})
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]authz.AccountIdentity, len(r.identities))
	for account, identity := range r.identities {
		out[account] = identity
	}
	return out, nil
}

func (r *stubIdentityReader) answer(identities map[string]authz.AccountIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identities = identities
}

func (r *stubIdentityReader) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *stubIdentityReader) asked() []identityCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]identityCall(nil), r.calls...)
}

type managerCall struct {
	op      string
	tenant  string
	role    authz.Role
	account string
	grant   authz.GrantRecord
	binding authz.Binding
	roleRec authz.RoleRecord
}

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

func (m *stubManager) RoleSubsetHeld(_ context.Context, tenant string, role authz.Role,
	_ map[authz.Permission]bool,
) (bool, error) {
	m.record(managerCall{op: "RoleSubsetHeld", tenant: tenant, role: role})
	if err := m.errNow(); err != nil {
		return false, err
	}
	return true, nil
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

func (m *stubManager) GrantCounts(_ context.Context, tenant string) (map[authz.Role]int, error) {
	m.record(managerCall{op: "GrantCounts", tenant: tenant})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := make(map[authz.Role]int)
	for _, grant := range m.grants {
		counts[grant.Role]++
	}
	return counts, nil
}

func (m *stubManager) BindingsOfRole(_ context.Context, tenant string, role authz.Role, after string, limit int) ([]authz.Binding, error) {
	m.record(managerCall{op: "BindingsOfRole", tenant: tenant, role: role})
	if err := m.errNow(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]authz.Binding, 0, len(m.bindings))
	for _, binding := range m.bindings {
		if binding.Role != role || (after != "" && binding.Account <= after) {
			continue
		}
		out = append(out, binding)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

type writeGuardCall struct {
	tenant string
	op     authz.WriteOp
}

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

func (g *stubWriteGuard) asked() []writeGuardCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]writeGuardCall(nil), g.calls...)
}

func (g *stubWriteGuard) refuse() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied = true
}

func (g *stubWriteGuard) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

type stubCatalog struct {
	mu          sync.Mutex
	perms       []authz.Permission
	set         map[authz.Permission]bool
	keys        map[authz.Permission]string
	metadata    map[authz.Permission]authz.PermissionMeta
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
		metadata:    make(map[authz.Permission]authz.PermissionMeta),
		governedSet: make(map[authz.Permission]bool),
	}
}

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

func (c *stubCatalog) TenantGoverned() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.governed...)
}

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

func (c *stubCatalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

func (c *stubCatalog) PermissionOf(object, action string) (authz.Permission, bool) {
	for _, p := range c.Declared() {
		o, a, ok := translation.Lookup(p)
		if ok && o == object && a == action {
			return p, true
		}
	}
	return "", false
}

func (c *stubCatalog) DescriptionKey(p authz.Permission) (key string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set[p] {
		return "", false
	}
	return c.keys[p], true
}

func (c *stubCatalog) Metadata(p authz.Permission) (authz.PermissionMeta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set[p] {
		return authz.PermissionMeta{}, false
	}
	return c.metadata[p], true
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

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

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

func (v *fakeSignupVerification) PendingAccountByContact(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("authz.admin: the pending-contact read is not part of this surface")
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

func (v *fakeSignupVerification) lastAccount() authn.AccountID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastID
}
