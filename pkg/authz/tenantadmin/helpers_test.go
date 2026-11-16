package tenantadmin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	_ authz.Manager                   = (*stubManager)(nil)
	_ authz.Catalog                   = (*stubCatalog)(nil)
	_ authz.SubjectPermissions        = (*stubSubjectPermissions)(nil)
	_ authz.MembershipChecker         = (*stubMembership)(nil)
	_ authz.AccountIdentityReader     = (*stubIdentities)(nil)
	_ authz.WriteGuard                = (*stubWriteGuard)(nil)
	_ guard.Guard                     = (*stubGuard)(nil)
	_ audit.Audit                     = (*recordingAudit)(nil)
	_ speedhttp.Router                = (*stubRouter)(nil)
	_ speedhttp.Endpoint              = (*stubEndpoint)(nil)
	_ speedhttp.TenantAdminRouteGuard = (*stubRouteGuard)(nil)
)

// managerCall is one call the stub manager recorded: the method's name and the
// arguments that name a row, so a case can hold what the handler passed on.
type managerCall struct {
	op      string
	tenant  string
	role    authz.Role
	account string
	after   string
	limit   int
	grant   authz.GrantRecord
	binding authz.Binding
	roleRec authz.RoleRecord
}

// stubManager is a stand-in authz.Manager. It records every call, answers reads
// from what a case set, and fails every method with the same error when one is
// set.
type stubManager struct {
	mu       sync.Mutex
	calls    []managerCall
	roles    []authz.RoleRecord
	grants   []authz.GrantRecord
	bindings []authz.Binding
	err      error
	// catalog maps a grant back to a permission, the way the product's catalog
	// does. The rig sets it to the catalog the product takes up.
	catalog authz.Catalog
}

// RoleSubsetHeld is the anti-escalation judgement the binding route asks the
// manager for. It maps the role's grants through the catalog the rig delivered,
// so a case holds the same answer the policy module gives.
func (m *stubManager) RoleSubsetHeld(_ context.Context, tenant string, role authz.Role,
	held map[authz.Permission]bool,
) (bool, error) {
	m.record(managerCall{op: "RoleSubsetHeld", tenant: tenant, role: role})
	if err := m.errNow(); err != nil {
		return false, err
	}
	m.mu.Lock()
	grants := append([]authz.GrantRecord(nil), m.grants...)
	catalog := m.catalog
	m.mu.Unlock()
	for _, grant := range grants {
		if grant.Tenant != tenant || grant.Role != role {
			continue
		}
		if catalog == nil {
			return false, nil
		}
		permission, ok := catalog.PermissionOf(grant.Resource, grant.Action)
		if !ok || !held[permission] {
			return false, nil
		}
	}
	return true, nil
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
	var out []authz.GrantRecord
	for _, g := range m.grants {
		if g.Role == role && g.Tenant == tenant {
			out = append(out, g)
		}
	}
	return out, nil
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

// GrantCounts counts the grants a case put in the stub, grouped by role.
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

// BindingsOfRole filters the stub's bindings the way the real manager does: by
// role, after the account cursor, and up to a positive limit.
func (m *stubManager) BindingsOfRole(_ context.Context, tenant string, role authz.Role, after string, limit int) ([]authz.Binding, error) {
	m.record(managerCall{op: "BindingsOfRole", tenant: tenant, role: role, after: after, limit: limit})
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

// stubCatalog is a stand-in authz.Catalog over a fixed set of permissions and a
// governed subset. It records every Governed and Has query.
type stubCatalog struct {
	mu          sync.Mutex
	perms       []authz.Permission
	set         map[authz.Permission]bool
	keys        map[authz.Permission]string
	metadata    map[authz.Permission]authz.PermissionMeta
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	has         []authz.Permission
	askedGovern []authz.Permission
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

// govern marks the permissions, which must already be declared, as governed.
func (c *stubCatalog) govern(perms ...authz.Permission) *stubCatalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range perms {
		c.governedSet[p] = true
		c.governed = append(c.governed, p)
	}
	return c
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
	c.askedGovern = append(c.askedGovern, p)
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

func (c *stubCatalog) governedAsked() []authz.Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]authz.Permission(nil), c.askedGovern...)
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

// Metadata answers the governance metadata a case gave a declared permission.
func (c *stubCatalog) Metadata(p authz.Permission) (authz.PermissionMeta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set[p] {
		return authz.PermissionMeta{}, false
	}
	return c.metadata[p], true
}

// stubSubjectPermissions answers Held from what a case scripts and records the
// domain and subject it was asked about.
type stubSubjectPermissions struct {
	mu       sync.Mutex
	held     []authz.Permission
	err      error
	calls    int
	domains  []authz.Domain
	subjects []string
}

func (s *stubSubjectPermissions) Held(_ context.Context, domain authz.Domain, subject string) ([]authz.Permission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.domains = append(s.domains, domain)
	s.subjects = append(s.subjects, subject)
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

func (s *stubSubjectPermissions) fail(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *stubSubjectPermissions) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubSubjectPermissions) asked() ([]authz.Domain, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authz.Domain(nil), s.domains...), append([]string(nil), s.subjects...)
}

// stubMembership answers IsMember from what a case scripts and records the
// tenant and account it was asked about.
type stubMembership struct {
	mu       sync.Mutex
	member   bool
	err      error
	tenants  []string
	accounts []string
}

func (m *stubMembership) IsMember(_ context.Context, tenant, account string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenants = append(m.tenants, tenant)
	m.accounts = append(m.accounts, account)
	return m.member, m.err
}

func (m *stubMembership) allow(member bool) { m.mu.Lock(); m.member = member; m.mu.Unlock() }
func (m *stubMembership) fail(err error)    { m.mu.Lock(); m.err = err; m.mu.Unlock() }

func (m *stubMembership) asked() ([]string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.tenants...), append([]string(nil), m.accounts...)
}

// stubIdentities is a stand-in authz.AccountIdentityReader. It answers the
// identities a case scripted for the accounts a read names and records what it
// was asked.
type stubIdentities struct {
	mu        sync.Mutex
	byAccount map[string]authz.AccountIdentity
	err       error
	calls     int
	tenants   []string
	asked     [][]string
}

func (s *stubIdentities) Identities(_ context.Context, tenant string, accounts []string) (map[string]authz.AccountIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.tenants = append(s.tenants, tenant)
	s.asked = append(s.asked, append([]string(nil), accounts...))
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]authz.AccountIdentity, len(accounts))
	for _, account := range accounts {
		if identity, ok := s.byAccount[account]; ok {
			out[account] = identity
		}
	}
	return out, nil
}

func (s *stubIdentities) answer(byAccount map[string]authz.AccountIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byAccount = byAccount
}

func (s *stubIdentities) fail(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *stubIdentities) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubIdentities) lastAsked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.asked) == 0 {
		return nil
	}
	return append([]string(nil), s.asked[len(s.asked)-1]...)
}

// writeGuardCall is one question the stand-in write guard was asked.
type writeGuardCall struct {
	tenant string
	op     authz.WriteOp
}

// stubWriteGuard is a stand-in authz.WriteGuard. Its zero value permits every
// write and records what it was asked.
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

func (g *stubWriteGuard) refuse()        { g.mu.Lock(); g.denied = true; g.mu.Unlock() }
func (g *stubWriteGuard) fail(err error) { g.mu.Lock(); g.err = err; g.mu.Unlock() }

// stubGuard is a stand-in guard.Guard. It records the permission and domain
// every decision asked about and answers from one setting.
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

// stubRouter is a stand-in speedhttp.Router over a fixed set of endpoints.
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

// stubRouteGuard stands in for the tenant administration guard. It records that
// it wrapped a handler and answers with the status a case scripts, or passes
// the request through when refuse is false. The shared guard is recorded
// separately so a case can hold that this module applies the stricter one.
type stubRouteGuard struct {
	mu      sync.Mutex
	wrapped int
	shared  int
	refuse  bool
	status  int
}

// Guard is the shared identity guard. The tenant administration surface must
// not use it, because it would leave the platform domain addressable; a case
// asserts sharedCount stays zero.
func (g *stubRouteGuard) Guard(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.shared++
	g.mu.Unlock()
	return next
}

func (g *stubRouteGuard) GuardTenantAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	g.mu.Lock()
	g.wrapped++
	g.mu.Unlock()
	return func(c *gin.Context) {
		g.mu.Lock()
		refuse, status := g.refuse, g.status
		g.mu.Unlock()
		if refuse {
			c.Status(status)
			c.Abort()
			return
		}
		next(c)
	}
}

func (g *stubRouteGuard) wrapCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.wrapped
}

// sharedCount is how often the shared guard was applied. The tenant
// administration surface applies the stricter one alone.
func (g *stubRouteGuard) sharedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shared
}

func (g *stubRouteGuard) refuseWith(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refuse = true
	g.status = status
}

// loggerStub delivers the logging capability over a recorder.
type loggerStub struct{ logger *slog.Logger }

func (l loggerStub) Named(string) *slog.Logger { return l.logger }
func (loggerStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes redaction registrations and masks nothing.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// logBuffer collects what the module logs.
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

// stubLocalizer renders a message from an in-memory index the way the engine
// does.
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

// standIn is a module that delivers instance for the tokens named.
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
// with what a case scripts and counts the reads.
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
	return "", false, errors.New("authz.tenantadmin: the pending-contact read is not part of this surface")
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
