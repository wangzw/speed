// This file drives the authorization management module through the whole chain
// on a loopback port the operating system picks: the real entry point, the real
// authentication gateway, the real authorization middleware and this module,
// assembled by the real registry. The policy manager, the permission catalog,
// the audit capability, the enforcement engine and the credential backend are
// stand-ins.
//
// What only the assembled chain decides is here: the shape a refused management
// request gets on the socket, that an allowed write reaches the manager end to
// end, that a manager outage is a 503 and never a denial, and that an endpoint
// the module's configuration does not name carries none of its routes. The
// cases read the bound address off the entry point's own listening record and
// send real requests, so nothing stands in for the router.
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
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

// The endpoints the assembly declares. "public" carries the whole chain and the
// management routes; "plain" is served by the entry point and named by no
// module, which is the endpoint this module's own list leaves out.
const (
	publicEndpoint = "public"
	plainEndpoint  = "plain"
)

// tenantName is the policy domain the management paths address. The module reads
// it from the path as the domain identifier and holds no roster to look it up.
const tenantName = "acme"

// The management paths a case sends. The write path carries a natural key — the
// role — and never an opaque row identifier.
const (
	permissionsPath = "/api/v1/authz/admin/permissions"
	rolesPath       = "/api/v1/authz/admin/tenants/" + tenantName + "/roles"
	rolePath        = rolesPath + "/editor"
)

// listeningMsg is the record the entry point writes once an endpoint's address
// is bound. It is the only place outside the process that says which port the
// system picked for an endpoint configured on port zero.
const listeningMsg = "an HTTP listener is listening"

// waitLimit bounds every request and every wait, so that a request or a startup
// that never answers fails the case with a message and not the binary at the
// global timeout.
const waitLimit = 10 * time.Second

// reply is what a client got back.
type reply struct {
	status int
	body   string
}

// problemDoc is the default response body a case reads back.
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

// record is one log record a case reads back, with the attributes it carried.
type record struct {
	msg   string
	attrs map[string]string
}

// recorder collects every record, so that the entry point's own listening record
// — which carries the bound address — reaches the case.
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

// address returns the bound address of an endpoint, read off the entry point's
// own listening record, or the empty string when none was written.
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

// namedLogger delivers the logging capability over one logger, so that what the
// entry point writes reaches the case.
type namedLogger struct{ logger *slog.Logger }

func (l namedLogger) Named(string) *slog.Logger { return l.logger }
func (namedLogger) Redaction() log.Redaction    { return noRules{} }

// noRules takes the redaction registrations and masks nothing: what these cases
// read is the records, not their masking.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// credentials is the credential backend the gateway verifies through: the bearer
// token names the caller, and the account is the token unchanged.
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

// managerCall is one call the stub manager recorded: the method's name and the
// arguments that name a row, so a case can hold what the handler passed on.
type managerCall struct {
	op      string
	tenant  string
	role    authz.Role
	account string
}

// managerStub is a stand-in authz.Manager. It records every call and fails every
// method with the same error when one is set, which is how a case drives a
// manager outage up the handler's mapping.
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

// auditStub keeps the rows it was asked to write and can be told to fail.
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

// provide is a stand-in module delivering one capability over one instance.
func provide(name string, token core.Token, instance any) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// assembly is one drive of the real registry to Serve, with the stand-ins a case
// drives. A nil stand-in is replaced with a fresh one.
type assembly struct {
	enforcer *authztest.Enforcer
	catalog  *authztest.Catalog
	manager  *managerStub
	audit    *auditStub
	// resolve, when set, puts the real tenant resolution layer on the chain, so
	// the request's context carries the tenant its alias resolves to. It is how
	// a case holds that a management decision is made in the platform domain and
	// not in whatever tenant the request resolved to.
	resolve bool
}

// rosterStub is the tenant roster the resolution layer reads: one alias maps to
// an identifier distinct from the alias, so a query built from the alias and one
// built from the identifier cannot be confused, and neither can be confused with
// the platform domain.
type rosterStub struct{ tenant.Catalog }

func (rosterStub) ByAlias(_ context.Context, alias string) (tenant.Tenant, bool, error) {
	if alias == "acme-alias" {
		return tenant.Tenant{ID: "t-acme", Alias: "acme-alias", Status: tenant.StatusActive}, true, nil
	}
	return tenant.Tenant{}, false, nil
}

// live is what a case holds while the assembly serves.
type live struct {
	t         *testing.T
	ctx       context.Context
	addresses map[string]string
	client    *nethttp.Client
}

// send issues one request against a bound endpoint and reads the answer off the
// socket. A non-empty body is sent as JSON.
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

// asAlice puts alice's bearer credential on a request, so the gateway stamps an
// identity the management handler reads.
func asAlice(r *nethttp.Request) { r.Header.Set("Authorization", "Bearer alice") }

// serve builds the assembly with the stand-ins, drives it to Serve on loopback
// ports the system picks, and hands the case the running endpoints. The run is
// stopped and closed when the case returns, and it has to come back clean.
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

	modules := []core.Module{entry, gateway, enforcement, management}
	participants := []string{
		speedhttp.ModuleName, gate.ModuleName, guard.ModuleName, admin.ModuleName,
		"stand-in.backend", "stand-in.logger", "stand-in.enforcer", "stand-in.catalog",
		"stand-in.manager", "stand-in.audit",
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
					Timeout: waitLimit,
					// A connection of its own per request, so no idle
					// connection holds the drain when the assembly stops.
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

// TestADeniedManagementRequestIsTheGuards403ShapeWithoutThePermission holds the
// refusal of a management decision on the wire: the guard's own 403 — its
// problem type, and a detail that never names the permission the route asked
// for. The engine denies every query, so the handler's decision shortfall ends
// the request before the manager is touched, and the query the guard put to the
// engine names the role object and the read action.
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

// TestAnAllowedWriteReachesTheManagerEndToEnd holds the pass through the whole
// chain: a caller the engine allows reaches the write handler, the mandatory
// audit row lands, and the manager is called with the natural keys from the
// path. A shortfall anywhere before the manager — a refusal, a missing route, a
// body the handler cannot read — would answer something other than 204 and leave
// the manager untouched.
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

// TestAManagerOutageIsA503NotADenial holds the closed mapping on the wire: a
// manager whose store is down is a 503 and never a 403. Reading the outage as a
// denial would turn a recoverable failure into a permanent permission
// conclusion, and letting the write land would be worse; the engine allows, so
// the guard is not what answers here.
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

// TestAReferenceToAnEndpointOutsideTheAdminListCarriesNoRoute holds the endpoint
// list: the management routes exist on the endpoint the module names and nowhere
// else. The same request reaches a route on the configured endpoint and the
// entry point's own 404 on the endpoint the module leaves out.
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

// withTenant puts the tenant alias on the request, so the resolution layer
// resolves it and the request's context carries the identifier it maps to.
func withTenant(alias string) func(*nethttp.Request) {
	return func(r *nethttp.Request) { r.Header.Set("X-Tenant", alias) }
}

// TestTheManagementDecisionIsMadeInThePlatformDomainNotTheResolvedTenant holds
// the domain a management decision is made in on a chain that resolves a tenant.
// The tenant in a management path is the data the route acts on, so the decision
// is made in the platform domain and is never steered by the tenant the request
// resolved to. Making it in the request's own tenant would let a caller
// authorized in one tenant reach another's roles, grants and bindings by naming
// that tenant in the path: the resolver puts a different tenant on the context —
// the header alias maps to the identifier t-acme while the path names acme — and
// the engine is still asked in the platform domain alone.
func TestTheManagementDecisionIsMadeInThePlatformDomainNotTheResolvedTenant(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	assembly{enforcer: enforcer, resolve: true}.serve(t, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, rolesPath, "", asAlice, withTenant("acme-alias"))
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
