package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/admin"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

const (
	publicEndpoint = "public"
	plainEndpoint  = "plain"
)

const tenantName = "acme"

const (
	permissionsPath = "/api/v1/system/permissions"
	rolesPath       = "/api/v1/system/tenants/" + tenantName + "/roles"
	rolePath        = rolesPath + "/editor"
)

const listeningMsg = "an HTTP listener is listening"

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

type reply struct {
	status int
	body   string
}

type problemDoc struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func decodeProblem(t *testing.T, body string) problemDoc {
	t.Helper()
	var p problemDoc
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("the body is not a problem details object: %v; body = %s", err, body)
	}
	return p
}

type record struct {
	msg   string
	attrs map[string]string
}

type recorder struct {
	mu      *sync.Mutex
	records *[]record
	bound   []slog.Attr
}

func newRecorder() *recorder { return &recorder{mu: &sync.Mutex{}, records: &[]record{}} }

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, len(r.bound)+rec.NumAttrs())
	for _, a := range r.bound {
		attrs[a.Key] = a.Value.String()
	}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.records = append(*r.records, record{msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recorder{mu: r.mu, records: r.records, bound: append(append([]slog.Attr(nil), r.bound...), attrs...)}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) address(endpoint string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range *r.records {
		if rec.msg == listeningMsg && rec.attrs["listener"] == endpoint {
			return rec.attrs["address"]
		}
	}
	return ""
}

type namedLogger struct{ logger *slog.Logger }

func (l namedLogger) Named(string) *slog.Logger { return l.logger }
func (namedLogger) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type credentials struct{}

func (credentials) Name() string { return "stub" }

func (credentials) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{"bearer"}}
}

func (credentials) Verify(_ context.Context, c authn.Credential) authn.Verdict {
	if c.Value == "" {
		return authn.Denied(nil)
	}
	return authn.Authenticated(authn.Principal{
		Account:   authn.AccountID(c.Value),
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  time.Now(),
	})
}

type managerCall struct {
	op      string
	tenant  string
	role    authz.Role
	account string
}

type managerStub struct {
	mu    sync.Mutex
	calls []managerCall
	err   error
}

var _ authz.Manager = (*managerStub)(nil)

func (m *managerStub) note(call managerCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

func (m *managerStub) recorded() []managerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]managerCall(nil), m.calls...)
}

func (m *managerStub) fail(err error) { m.mu.Lock(); m.err = err; m.mu.Unlock() }

func (m *managerStub) errNow() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *managerStub) Roles(_ context.Context, tenant string) ([]authz.RoleRecord, error) {
	m.note(managerCall{op: "Roles", tenant: tenant})
	return nil, m.errNow()
}

func (m *managerStub) Grants(_ context.Context, tenant string, role authz.Role) ([]authz.GrantRecord, error) {
	m.note(managerCall{op: "Grants", tenant: tenant, role: role})
	return nil, m.errNow()
}

func (m *managerStub) Bindings(_ context.Context, tenant string) ([]authz.Binding, error) {
	m.note(managerCall{op: "Bindings", tenant: tenant})
	return nil, m.errNow()
}

func (m *managerStub) BindingsOf(_ context.Context, tenant, account string) ([]authz.Binding, error) {
	m.note(managerCall{op: "BindingsOf", tenant: tenant, account: account})
	return nil, m.errNow()
}

func (m *managerStub) RoleSubsetHeld(_ context.Context, tenant string, role authz.Role,
	_ map[authz.Permission]bool,
) (bool, error) {
	m.note(managerCall{op: "RoleSubsetHeld", tenant: tenant, role: role})
	if err := m.errNow(); err != nil {
		return false, err
	}
	return true, nil
}

func (m *managerStub) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.note(managerCall{op: "PutRole", tenant: r.Tenant, role: r.Role})
	return m.errNow()
}

func (m *managerStub) MarkRoleBuiltin(_ context.Context, tenant string, role authz.Role) error {
	m.note(managerCall{op: "MarkRoleBuiltin", tenant: tenant, role: role})
	return m.errNow()
}

func (m *managerStub) DeleteRole(_ context.Context, tenant string, role authz.Role) error {
	m.note(managerCall{op: "DeleteRole", tenant: tenant, role: role})
	return m.errNow()
}

func (m *managerStub) PutGrant(_ context.Context, g authz.GrantRecord) error {
	m.note(managerCall{op: "PutGrant", tenant: g.Tenant, role: g.Role})
	return m.errNow()
}

func (m *managerStub) DeleteGrant(_ context.Context, g authz.GrantRecord) error {
	m.note(managerCall{op: "DeleteGrant", tenant: g.Tenant, role: g.Role})
	return m.errNow()
}

func (m *managerStub) PutBinding(_ context.Context, b authz.Binding) error {
	m.note(managerCall{op: "PutBinding", tenant: b.Tenant, role: b.Role, account: b.Account})
	return m.errNow()
}

func (m *managerStub) DeleteBinding(_ context.Context, tenant, account string, role authz.Role) error {
	m.note(managerCall{op: "DeleteBinding", tenant: tenant, account: account, role: role})
	return m.errNow()
}

func (m *managerStub) ClearAccountBindings(_ context.Context, tenant, account string) error {
	m.note(managerCall{op: "ClearAccountBindings", tenant: tenant, account: account})
	return m.errNow()
}

func (m *managerStub) GrantCounts(_ context.Context, tenant string) (map[authz.Role]int, error) {
	m.note(managerCall{op: "GrantCounts", tenant: tenant})
	return nil, m.errNow()
}

func (m *managerStub) BindingsOfRole(_ context.Context, tenant string, role authz.Role, _ string, _ int) ([]authz.Binding, error) {
	m.note(managerCall{op: "BindingsOfRole", tenant: tenant, role: role})
	return nil, m.errNow()
}

type auditStub struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

var _ audit.Audit = (*auditStub)(nil)

func (a *auditStub) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *auditStub) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

func provide(name string, token core.Token, instance any) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type subjectStub struct {
	held []authz.Permission
}

func (s subjectStub) Held(context.Context, authz.Domain, string) ([]authz.Permission, error) {
	return append([]authz.Permission(nil), s.held...), nil
}

type signupStub struct {
	authn.SignupVerification
	state authn.SignupState
}

func (s signupStub) SignupStateOf(context.Context, authn.AccountID) (authn.SignupStatus, error) {
	return authn.SignupStatus{State: s.state}, nil
}

type assembly struct {
	enforcer *authztest.Enforcer
	catalog  *authztest.Catalog
	manager  *managerStub
	audit    *auditStub
	resolve  bool
}

type rosterStub struct{ tenant.Catalog }

func (rosterStub) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	if name == "acme-name" {
		return tenant.Tenant{ID: "t-acme", Name: "acme-name", Status: tenant.StatusActive}, true, nil
	}
	return tenant.Tenant{}, false, nil
}

type live struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	client    *nethttp.Client
}

func (l *live) send(endpoint, method, path, body string, edits ...func(*nethttp.Request)) reply {
	l.t.Helper()
	address, ok := l.addresses[endpoint]
	if !ok {
		l.t.Fatalf("endpoint %q has no bound address", endpoint)
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := nethttp.NewRequestWithContext(l.ctx, method, "http://"+address+path, reader)
	if err != nil {
		l.t.Fatalf("building %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("%s %s on %q got no response: %v", method, path, endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		l.t.Fatalf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, body: string(payload)}
}

func asAlice(r *nethttp.Request) { r.Header.Set("Authorization", "Bearer alice") }

func (a assembly) serve(t *testing.T, use func(l *live)) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)

	if a.enforcer == nil {
		a.enforcer = authztest.NewEnforcer()
	}
	if a.catalog == nil {
		a.catalog = authztest.NewCatalog(authz.PermissionRoleRead, authz.PermissionRoleManage)
	}
	if a.manager == nil {
		a.manager = &managerStub{}
	}
	if a.audit == nil {
		a.audit = &auditStub{}
	}

	body := `{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}, "plain": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}, "plain": {"listener": "plain"}}},
		"authn": {"gate": {"endpoints": ["public"]}},
		"authz": {"guard": {"endpoints": ["public"]}, "admin": {"endpoints": ["public"]}}
	}`
	entry := speedhttp.Module()
	gateway := gate.Module()
	enforcement := guard.Module()
	management := admin.Module()

	logs := newRecorder()
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(entry)
	reg.Register(gateway)
	reg.Register(enforcement)
	reg.Register(management)
	reg.Register(provide("stand-in.backend", (*authn.Verifier)(nil), credentials{}))
	reg.Register(provide("stand-in.logger", (*log.Logger)(nil), namedLogger{logger: slog.New(logs)}))
	reg.Register(provide("stand-in.enforcer", (*authz.Enforcer)(nil), a.enforcer))
	reg.Register(provide("stand-in.catalog", (*authz.Catalog)(nil), a.catalog))
	reg.Register(provide("stand-in.manager", (*authz.Manager)(nil), a.manager))
	reg.Register(provide("stand-in.audit", (*audit.Audit)(nil), a.audit))
	reg.Register(provide("stand-in.verification", (*authn.SignupVerification)(nil), signupStub{}))
	reg.Register(provide("stand-in.subject", (*authz.SubjectPermissions)(nil), subjectStub{}))

	modules := []core.Module{entry, gateway, enforcement, management}
	participants := []string{
		speedhttp.ModuleName, gate.ModuleName, guard.ModuleName, admin.ModuleName,
		"stand-in.backend", "stand-in.logger", "stand-in.enforcer", "stand-in.catalog",
		"stand-in.manager", "stand-in.audit", "stand-in.verification", "stand-in.subject",
	}
	if a.resolve {
		body = `{
			"http": {"listeners": {"public": {"address": "127.0.0.1:0"}, "plain": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}, "plain": {"listener": "plain"}}},
			"tenant": {"resolve": {"endpoints": ["public"], "extractor": "header"}},
			"authn": {"gate": {"endpoints": ["public"]}},
			"authz": {"guard": {"endpoints": ["public"]}, "admin": {"endpoints": ["public"]}}
		}`
		resolver := resolve.Module()
		reg.Register(resolver)
		reg.Register(provide("stand-in.roster", (*tenant.Catalog)(nil), rosterStub{}))
		modules = append(modules, resolver)
		participants = append(participants, resolve.ModuleName, "stand-in.roster")
	}

	reader := authntest.ConfigReaderAll(t, body, modules...)
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: participants,
		Action: func(ctx context.Context, _ *core.Registry) error {
			addresses := make(map[string]string, 2)
			for _, name := range []string{publicEndpoint, plainEndpoint} {
				address := logs.address(name)
				if address == "" {
					return fmt.Errorf("the entry point wrote no record of binding endpoint %q", name)
				}
				addresses[name] = address
			}
			use(&live{
				t:         t,
				ctx:       ctx,
				addresses: addresses,
				client: &nethttp.Client{
					Timeout:   waitLimit(),
					Transport: &nethttp.Transport{DisableKeepAlives: true},
				},
			})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the assembly did not run cleanly: %v", err)
	}
}

// TestADeniedManagementRequestIsTheGuards403ShapeWithoutThePermission checks that a denied management request is the guards 403 shape without the permission.
func TestADeniedManagementRequestIsTheGuards403ShapeWithoutThePermission(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	assembly{enforcer: enforcer}.serve(t, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, rolesPath, "", asAlice)
		if got.status != nethttp.StatusForbidden {
			t.Fatalf("a denied management request answered %d, want 403; the body was %q", got.status, got.body)
		}
		doc := decodeProblem(t, got.body)
		if doc.Type != "urn:speed:authz:forbidden" {
			t.Errorf("the problem type = %q, want urn:speed:authz:forbidden", doc.Type)
		}
		if doc.Status != nethttp.StatusForbidden {
			t.Errorf("the body says %d, want 403", doc.Status)
		}
		if strings.Contains(got.body, string(authz.PermissionRoleRead)) {
			t.Errorf("the answer names the permission it asked for, making it a probe of the policy: %q", got.body)
		}
		want := authz.Query{Subject: "alice", Domain: authz.PlatformDomain, Object: "role", Action: "read"}
		if asked := enforcer.Asked(); len(asked) != 1 || asked[0] != want {
			t.Errorf("the engine was asked %v, want exactly %v", asked, want)
		}
	})
}

// TestAnAllowedWriteReachesTheManagerEndToEnd checks that an allowed write reaches the manager end to end.
func TestAnAllowedWriteReachesTheManagerEndToEnd(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	manager := &managerStub{}
	audit := &auditStub{}
	assembly{enforcer: enforcer, manager: manager, audit: audit}.serve(t, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodPut, rolePath, "{}", asAlice)
		if got.status != nethttp.StatusNoContent {
			t.Fatalf("an allowed write answered %d, want 204; the body was %q", got.status, got.body)
		}
		calls := manager.recorded()
		if len(calls) != 1 || calls[0].op != "PutRole" || calls[0].tenant != tenantName || string(calls[0].role) != "editor" {
			t.Fatalf("the manager calls = %+v, want one PutRole for %s/editor", calls, tenantName)
		}
		rows := audit.entries()
		if len(rows) != 1 || rows[0].Action != "authz.role.put" || rows[0].Outcome != "success" {
			t.Fatalf("the audit rows = %+v, want one success row for authz.role.put", rows)
		}
	})
}

// TestAManagerOutageIsA503NotADenial checks that a manager outage is a 503 not a denial.
func TestAManagerOutageIsA503NotADenial(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	manager := &managerStub{}
	manager.fail(errors.New("the policy store is down"))
	assembly{enforcer: enforcer, manager: manager}.serve(t, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodPut, rolePath, "{}", asAlice)
		if got.status == nethttp.StatusForbidden {
			t.Fatal("a manager outage was written as a denial")
		}
		if got.status == nethttp.StatusNoContent {
			t.Fatal("a manager outage let the write land")
		}
		if got.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("a manager outage answered %d, want 503; the body was %q", got.status, got.body)
		}
		if strings.Contains(got.body, "urn:speed:authz:forbidden") {
			t.Errorf("the outage carries the denial's problem type: %q", got.body)
		}
	})
}

// TestAReferenceToAnEndpointOutsideTheAdminListCarriesNoRoute checks that a reference to an endpoint outside the admin list carries no route.
func TestAReferenceToAnEndpointOutsideTheAdminListCarriesNoRoute(t *testing.T) {
	assembly{}.serve(t, func(l *live) {
		configured := l.send(publicEndpoint, nethttp.MethodGet, permissionsPath, "", asAlice)
		if configured.status == nethttp.StatusNotFound {
			t.Fatal("the endpoint the module names answered 404: the route is not mounted where it was configured")
		}
		outside := l.send(plainEndpoint, nethttp.MethodGet, permissionsPath, "", asAlice)
		if outside.status != nethttp.StatusNotFound {
			t.Fatalf("an endpoint outside the module's list answered %d, want the entry point's 404", outside.status)
		}
	})
}

func withTenant(name string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.Header.Set("X-Tenant", name) }
}

// TestTheManagementDecisionIsMadeInThePlatformDomainNotTheResolvedTenant checks that the management decision is made in the platform domain not the resolved tenant.
func TestTheManagementDecisionIsMadeInThePlatformDomainNotTheResolvedTenant(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	assembly{enforcer: enforcer, resolve: true}.serve(t, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, rolesPath, "", asAlice, withTenant("acme-name"))
		if got.status != nethttp.StatusOK {
			t.Fatalf("a management read on a tenant-resolving chain answered %d, want 200; the body was %q", got.status, got.body)
		}
		want := authz.Query{Subject: "alice", Domain: authz.PlatformDomain, Object: "role", Action: "read"}
		asked := enforcer.Asked()
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("the engine was asked %v, want exactly %v: the platform domain and not the resolved tenant", asked, want)
		}
	})
}
