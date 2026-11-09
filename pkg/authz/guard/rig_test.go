package guard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

// The cases built on this file drive the middleware the way a request reaches
// it: a real *gin.Context over an httptest recorder, a handler behind the layer
// that answers 200 when the request gets through, and a reply read back. The
// engine is a stub whose answer the case chooses and whose queries it reads
// back. This file also carries the decider's direct cases, the descriptor and
// middleware shape cases, the startup diagnostics, and one live case that runs
// the real entry point on a loopback port. The dedicated assembly harness is
// assembly_test.go and the dedicated live harness is live_test.go; the two
// overlap this file's own registry-driven and loopback cases, kept as a second
// channel over the same descriptor.

// guardedBody is a section that satisfies the module's required endpoint list,
// for a drive that does not care which endpoint it guards.
const guardedBody = `{"authz":{"guard":{"endpoints":["public"]}}}`

// base returns a plain context. The middleware reads the request's context, so
// every case builds one from here.
func base() context.Context { return context.Background() }

// caller returns a context holding an identity of alice, unless an option names
// another account, as the gateway leaves it on a request.
func caller(t *testing.T, opts ...authntest.Option) context.Context {
	t.Helper()
	opts = append([]authntest.Option{authntest.WithAccount("alice")}, opts...)
	return authntest.Context(t, base(), opts...)
}

// anonymous returns a context for a route that is exempt or optional and
// carried no credential, as the gateway leaves it. AccessRequired with no
// identity never reaches this layer — the gateway refuses such a request first —
// and AccessUnknown is what a context the gateway never decided anything for
// reads as, which a plain context builds.
func anonymous(t *testing.T, access authn.Access) context.Context {
	t.Helper()
	return authntest.Anonymous(t, base(), access)
}

// record is one log record a case reads back.
type record struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

// recorder is a log handler that keeps what was written.
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
	*r.records = append(*r.records, record{level: rec.Level, msg: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recorder{mu: r.mu, records: r.records, bound: append(append([]slog.Attr(nil), r.bound...), attrs...)}
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) all() []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]record(nil), *r.records...)
}

func (r *recorder) atLevel(level slog.Level) []record {
	var out []record
	for _, rec := range r.all() {
		if rec.level == level {
			out = append(out, rec)
		}
	}
	return out
}

// reply is what the client got back.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

// reached reports whether the request got through to the handler behind the
// layer.
func (rp reply) reached() bool { return rp.status == nethttp.StatusOK && rp.body == "reached" }

// rig is one product over a stub engine, with the request path helpers.
type rig struct {
	t        *testing.T
	p        *product
	enforcer *stubEnforcer
	logs     *recorder
}

// newRig builds a product guarding "public" with the closed default, over an
// engine that denies until a case says otherwise.
func newRig(t *testing.T) *rig {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	logs := newRecorder()
	enforcer := &stubEnforcer{}
	catalog := authztest.NewCatalog(
		authz.PermissionAccountRead,
		authz.PermissionAccountDelete,
		authz.PermissionAuditRead,
		authz.PermissionSettingsWrite,
	)
	return &rig{
		t:        t,
		enforcer: enforcer,
		logs:     logs,
		p: &product{
			enforcer: enforcer,
			catalog:  catalog,
			gate:     newGate(catalog),
			cfg:      resolved{endpoints: []string{"public"}, undeclared: undeclaredClosed},
			logger:   slog.New(logs),
		},
	}
}

// send runs one request through the layer. routePath is the path bound to a
// handler — empty to bind none, so the request matches no route — and reqPath is
// what the request asks for. declare, when set, writes the route's declaration
// before the request is served, the way a module's Init does.
func (r *rig) send(ctx context.Context, method, routePath, reqPath string, declare func(g *gate)) reply {
	r.t.Helper()
	engine := gin.New()
	engine.Use(r.p.middlewareFor("public").Handler)
	if routePath != "" {
		engine.Handle(method, routePath, func(c *gin.Context) { c.String(nethttp.StatusOK, "reached") })
	}
	if declare != nil {
		declare(r.p.gate)
	}
	ctx = log.WithLogger(ctx, slog.New(r.logs))
	req := httptest.NewRequestWithContext(ctx, method, reqPath, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		r.t.Fatalf("reading the reply: %v", err)
	}
	return reply{status: rec.Code, header: rec.Header().Clone(), body: string(body)}
}

// orders runs one GET /orders through the layer with the given declaration.
func (r *rig) orders(ctx context.Context, declare func(g *gate)) reply {
	r.t.Helper()
	return r.send(ctx, nethttp.MethodGet, "/orders", "/orders", declare)
}

// counts reads the decision counts by outcome name.
func (r *rig) counts() map[string]int64 {
	out := make(map[string]int64, outcomeCount)
	for o := range r.p.counts {
		out[outcome(o).String()] = r.p.counts[o].Load()
	}
	return out
}

// onlyCounted fails the case unless exactly one request was counted, under the
// outcome named.
func (r *rig) onlyCounted(want outcome) {
	r.t.Helper()
	got := r.counts()
	var total int64
	for _, n := range got {
		total += n
	}
	if total != 1 || got[want.String()] != 1 {
		r.t.Fatalf("the counts are %v, want exactly one request, counted as %s", got, want)
	}
}

// loggedCause reports whether an Error line carries the given value in its error
// attribute, which is how a case reads that a refusal emitted its diagnosis.
func (r *rig) loggedCause(cause string) bool {
	for _, rec := range r.logs.atLevel(slog.LevelError) {
		if rec.attrs["error"] == cause {
			return true
		}
	}
	return false
}

// requireAccountRead declares GET /orders as needing authz.PermissionAccountRead.
func requireAccountRead(g *gate) {
	g.Require(nethttp.MethodGet, "/orders", authz.PermissionAccountRead)
}

// openOrders declares GET /orders open.
func openOrders(g *gate) { g.Open(nethttp.MethodGet, "/orders") }

// A request that matched no route is handed on: the entry point answers its own
// 404 and the layer makes no decision, so nothing is counted.
func TestAMatchedNoRouteIsHandedOn(t *testing.T) {
	r := newRig(t)
	rp := r.send(base(), nethttp.MethodGet, "", "/missing", nil)
	if rp.status != nethttp.StatusNotFound {
		t.Fatalf("answered %d, want the entry point's 404", rp.status)
	}
	var total int64
	for _, n := range r.counts() {
		total += n
	}
	if total != 0 {
		t.Fatalf("the counts are %v, want no decision for a request that matched no route", r.counts())
	}
}

// An open route is let through, and the request is counted as allowed.
func TestAnOpenRouteIsLetThrough(t *testing.T) {
	r := newRig(t)
	rp := r.orders(caller(t), openOrders)
	if !rp.reached() {
		t.Fatalf("the request did not reach the handler: %+v", rp)
	}
	if got := r.enforcer.queries(); len(got) != 0 {
		t.Fatalf("the engine was asked %v for an open route, want nothing", got)
	}
	r.onlyCounted(allowed)
}

// A route that requires a permission and carries an identity the engine allows
// is let through, counted as allowed.
func TestARequiredRouteWithAnAllowedIdentityPasses(t *testing.T) {
	r := newRig(t)
	r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	rp := r.orders(caller(t), requireAccountRead)
	if !rp.reached() {
		t.Fatalf("the request did not reach the handler: %+v", rp)
	}
	r.onlyCounted(allowed)
}

// A route the engine denies is a 403 with this module's problem type, never a
// 503. Reading the denial as an outage, or the outage as a denial, fails the two
// cases that hold the boundary.
func TestARouteTheEngineDeniesIsA403(t *testing.T) {
	r := newRig(t)
	r.enforcer.answer = func(authz.Query) (bool, error) { return false, nil }
	rp := r.orders(caller(t), requireAccountRead)
	if rp.status != nethttp.StatusForbidden {
		t.Fatalf("answered %d, want 403", rp.status)
	}
	body := decodeProblem(t, []byte(rp.body))
	if body.Type != typePrefix+forbiddenCode {
		t.Errorf("type = %q, want %q", body.Type, typePrefix+forbiddenCode)
	}
	if strings.Contains(rp.body, string(authz.PermissionAccountRead)) {
		t.Errorf("the answer names the permission: %s", rp.body)
	}
	r.onlyCounted(denied)
	if len(r.logs.atLevel(slog.LevelInfo)) == 0 {
		t.Error("a policy denial left no Info line")
	}
}

// An engine error is a 503 and never a 403, and the engine's cause reaches
// neither the body nor a type that reads as a refusal.
func TestAnEngineErrorIsA503(t *testing.T) {
	r := newRig(t)
	cause := errors.New("the store is down")
	r.enforcer.answer = func(authz.Query) (bool, error) { return false, cause }
	rp := r.orders(caller(t), requireAccountRead)
	if rp.status != nethttp.StatusServiceUnavailable {
		t.Fatalf("answered %d, want 503", rp.status)
	}
	if rp.status == nethttp.StatusForbidden {
		t.Fatal("an outage was written as a denial")
	}
	if strings.Contains(rp.body, cause.Error()) {
		t.Errorf("the cause reaches the caller: %s", rp.body)
	}
	r.onlyCounted(outage)
	if len(r.logs.atLevel(slog.LevelError)) == 0 {
		t.Error("an outage left no Error line")
	}
}

// A route that requires identity and carries none is a 401 with the realm
// challenge. The reachable shape of that request on a guarded endpoint reads as
// AccessUnknown: AccessRequired with no identity is refused by the gateway
// before any handler, so it never reaches this layer, and the middleware's
// default branch answers 401 for it as it does for AccessUnknown.
func TestNoIdentityWhereItIsRequiredIsA401(t *testing.T) {
	r := newRig(t)
	rp := r.orders(base(), requireAccountRead)
	if rp.status != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", rp.status)
	}
	if got := rp.header.Get("WWW-Authenticate"); !strings.Contains(got, `Bearer realm="public"`) {
		t.Errorf("WWW-Authenticate = %q, want the realm challenge", got)
	}
	if got := r.enforcer.queries(); len(got) != 0 {
		t.Errorf("the engine was asked %v for a request with no identity, want nothing", got)
	}
	r.onlyCounted(noIdentity)
}

// A route that requires a permission but is declared open or optional to
// anonymous callers lets the request through: the access level the gateway
// recorded is what tells the two no-identity cases apart.
func TestNoIdentityOnAnOpenOrOptionalRoutePasses(t *testing.T) {
	for name, access := range map[string]authn.Access{
		"open":     authn.AccessOpen,
		"optional": authn.AccessOptional,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			rp := r.orders(anonymous(t, access), requireAccountRead)
			if !rp.reached() {
				t.Fatalf("the request did not reach the handler: %+v", rp)
			}
			r.onlyCounted(allowed)
		})
	}
}

// A route with no declaration at all is refused under the closed default, with
// this module's 403 and an Error line, and is let through only when the
// disposition is open.
func TestAnUndeclaredRouteIsRefusedByDefaultAndLetThroughWhenOpen(t *testing.T) {
	t.Run("closed by default", func(t *testing.T) {
		r := newRig(t)
		rp := r.orders(caller(t), nil)
		if rp.status != nethttp.StatusForbidden {
			t.Fatalf("answered %d, want 403", rp.status)
		}
		body := decodeProblem(t, []byte(rp.body))
		if body.Type != typePrefix+forbiddenCode {
			t.Errorf("type = %q, want %q", body.Type, typePrefix+forbiddenCode)
		}
		r.onlyCounted(unguarded)
		if len(r.logs.atLevel(slog.LevelError)) == 0 {
			t.Error("an undeclared route under the closed default left no Error line")
		}
		if !r.loggedCause(authz.ErrUndeclaredRoute.Error()) {
			t.Error("the refusal of an undeclared route did not emit authz.ErrUndeclaredRoute: the design's request-period sentinel reaches no line")
		}
	})

	t.Run("open lets it through", func(t *testing.T) {
		r := newRig(t)
		r.p.cfg.undeclared = undeclaredOpen
		rp := r.orders(caller(t), nil)
		if !rp.reached() {
			t.Fatalf("the request did not reach the handler under the open disposition: %+v", rp)
		}
		r.onlyCounted(allowed)
	})
}

// The query names the account the request is taken to be from, and the platform
// domain when the request resolved to no tenant: an impersonation decides for
// the impersonated account, not for the actor whose hands are on the request.
func TestTheQueryNamesTheEffectiveAccountAndThePlatformDomain(t *testing.T) {
	r := newRig(t)
	r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	ctx := caller(t,
		authntest.WithAccount("acct-bob"),
		authntest.WithActor("acct-alice"),
		authntest.WithSession("sess-1"),
		authntest.WithScope("acme"),
	)
	rp := r.orders(ctx, requireAccountRead)
	if !rp.reached() {
		t.Fatalf("the request did not reach the handler: %+v", rp)
	}
	want := authz.Query{Subject: "acct-bob", Domain: authz.PlatformDomain, Object: "account", Action: "read"}
	got := r.enforcer.queries()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the engine was asked %v, want exactly %v", got, want)
	}
}

// ginContext builds a real *gin.Context over a recorder with a request carrying
// ctx, the way a handler receives it.
func ginContext(t *testing.T, ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequestWithContext(ctx, nethttp.MethodGet, "/orders", nil)
	return c, rec
}

// Allowed answers the question and writes nothing: an identity the engine
// allows is true, a request with no identity and a permission this module cannot
// express are both refused without an error, and the engine is asked only for a
// request that can be decided.
func TestGuardAllowedAnswersWithoutWriting(t *testing.T) {
	t.Run("an allowed identity", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
		c, rec := ginContext(t, caller(t))
		ok, err := r.p.Allowed(c, authz.PermissionAccountRead)
		if err != nil || !ok {
			t.Fatalf("Allowed = (%t, %v), want (true, nil)", ok, err)
		}
		if rec.Code != nethttp.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("Allowed wrote a response: status %d body %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("no identity is refused and the engine is not asked", func(t *testing.T) {
		r := newRig(t)
		c, _ := ginContext(t, base())
		ok, err := r.p.Allowed(c, authz.PermissionAccountRead)
		if err != nil || ok {
			t.Fatalf("Allowed = (%t, %v), want (false, nil)", ok, err)
		}
		if got := r.enforcer.queries(); len(got) != 0 {
			t.Errorf("the engine was asked %v, want nothing", got)
		}
	})

	t.Run("a permission with no translation is refused", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
		c, _ := ginContext(t, caller(t))
		ok, err := r.p.Allowed(c, authz.Permission("authz.not.a.real.permission"))
		if err != nil || ok {
			t.Fatalf("Allowed = (%t, %v), want (false, nil)", ok, err)
		}
	})
}

// The Guard API puts the same query the middleware would for the same context:
// the subject is the account the request is taken to be from, the (object,
// action) pair is the translation's row, and
// the domain is the platform domain when the request carries no tenant. The
// engine is asked exactly this and nothing else.
func TestGuardAllowedNamesTheSameQuery(t *testing.T) {
	r := newRig(t)
	r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	ctx := caller(t,
		authntest.WithAccount("acct-bob"),
		authntest.WithActor("acct-alice"),
		authntest.WithSession("sess-1"),
		authntest.WithScope("acme"),
	)
	c, _ := ginContext(t, ctx)
	ok, err := r.p.Allowed(c, authz.PermissionAccountRead)
	if err != nil || !ok {
		t.Fatalf("Allowed = (%t, %v), want (true, nil)", ok, err)
	}
	want := authz.Query{Subject: "acct-bob", Domain: authz.PlatformDomain, Object: "account", Action: "read"}
	got := r.enforcer.queries()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the engine was asked %v, want exactly %v: the effective account and the translation's row", got, want)
	}
}

// Require writes the refusal and answers false: a denial is a 403, an outage is
// a 503, and an allowed question writes nothing and answers true.
func TestGuardRequireWritesTheRefusal(t *testing.T) {
	t.Run("a denial is a 403", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return false, nil }
		c, rec := ginContext(t, caller(t))
		if r.p.RequirePermission(c, authz.PermissionAccountRead) {
			t.Fatal("Require allowed a question the engine denied")
		}
		if rec.Code != nethttp.StatusForbidden {
			t.Fatalf("wrote %d, want 403", rec.Code)
		}
	})

	t.Run("an outage is a 503", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return false, errors.New("down") }
		// The request's context carries the recorder, as a request on the chain
		// carries the layers' logger, so the case reads back what the refusal
		// logged.
		c, rec := ginContext(t, log.WithLogger(caller(t), slog.New(r.logs)))
		if r.p.RequirePermission(c, authz.PermissionAccountRead) {
			t.Fatal("Require allowed a question the engine could not answer")
		}
		if rec.Code != nethttp.StatusServiceUnavailable {
			t.Fatalf("wrote %d, want 503", rec.Code)
		}
		if !r.loggedCause("down") {
			t.Error("an outage through the handler API left no line carrying the cause: it was dropped")
		}
	})

	t.Run("an allowed question writes nothing", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
		c, rec := ginContext(t, caller(t))
		if !r.p.RequirePermission(c, authz.PermissionAccountRead) {
			t.Fatal("Require refused a question the engine allowed")
		}
		if rec.Code != nethttp.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("Require wrote a response: status %d body %q", rec.Code, rec.Body.String())
		}
	})
}

// The handler API's explicit-domain form decides in the domain it is given and
// not in the domain the request carries: a caller that names a domain of its own
// — a management surface whose path tenant is data and not a scope — is not
// steered by the tenant the request resolved to. The context here carries no
// tenant, so the context-based form reads the platform domain, while the
// explicit form decides in the domain it was handed.
func TestGuardRequirePermissionInDecidesInTheGivenDomain(t *testing.T) {
	t.Run("the explicit domain is used", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
		c, _ := ginContext(t, caller(t))
		if !r.p.RequirePermissionIn(c, authz.PermissionAccountRead, "t-other") {
			t.Fatal("RequirePermissionIn refused a question the engine allowed")
		}
		want := authz.Query{Subject: "alice", Domain: "t-other", Object: "account", Action: "read"}
		if got := r.enforcer.queries(); len(got) != 1 || got[0] != want {
			t.Fatalf("the engine was asked %v, want exactly %v", got, want)
		}
	})

	t.Run("the context-based form reads the platform domain", func(t *testing.T) {
		r := newRig(t)
		r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
		c, _ := ginContext(t, caller(t))
		if !r.p.RequirePermission(c, authz.PermissionAccountRead) {
			t.Fatal("RequirePermission refused a question the engine allowed")
		}
		want := authz.Query{Subject: "alice", Domain: authz.PlatformDomain, Object: "account", Action: "read"}
		if got := r.enforcer.queries(); len(got) != 1 || got[0] != want {
			t.Fatalf("the engine was asked %v, want exactly %v", got, want)
		}
	})
}

// decisionsNameForTest is the published instrument name, written as a literal
// and not through decisionsInstrument. The name is a stability commitment a
// dashboard or an alert is written against, so the case finds the instrument by
// the literal: were the constant's value to drift, the counter would be created
// under the drifted name and this case would find nothing.
const decisionsNameForTest = "authz.guard.decisions"

// decisionCounts reads the authz.guard.decisions counter out of a collection,
// by outcome name, with the meter name and the unit.
func decisionCounts(rm metricdata.ResourceMetrics) (map[string]int64, string, string) {
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != decisionsNameForTest {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				return nil, "", ""
			}
			out := make(map[string]int64, len(sum.DataPoints))
			for _, point := range sum.DataPoints {
				v, _ := point.Attributes.Value(attribute.Key(outcomeAttribute))
				out[v.AsString()] = point.Value
			}
			return out, scope.Scope.Name, m.Unit
		}
	}
	return nil, "", ""
}

// The decision counter reaches the metric capability: the instrument is on the
// authz meter, each count is the number of requests that got it, every outcome
// has a point from the first collection — the empty ones included — and Close
// withdraws the callback.
func TestTheDecisionCountReachesTheMetricCapability(t *testing.T) {
	collected := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(collected))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	r := newRig(t)
	r.p.meter = provider
	if err := r.p.registerMetrics(); err != nil {
		t.Fatalf("registering the counter: %v", err)
	}

	// One request of each outcome. The declaration table keeps what earlier
	// requests declared, so the unguarded one asks a route nothing ever
	// declared.
	r.enforcer.answer = func(authz.Query) (bool, error) { return true, nil }
	r.orders(caller(t), openOrders)                                            // allowed
	r.orders(caller(t), func(g *gate) { requireAccountRead(g); r.gateDeny() }) // denied
	r.orders(base(), nil)                                                      // no-identity (GET /orders still requires)
	r.send(caller(t), nethttp.MethodGet, "/plain", "/plain", nil)              // unguarded
	r.gateFail(errors.New("down"))
	r.orders(caller(t), requireAccountRead) // unavailable

	var rm metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting: %v", err)
	}
	counts, meterName, unit := decisionCounts(rm)
	if counts == nil {
		t.Fatalf("no %q instrument was collected: the counts are held in the process and reach no dashboard", decisionsNameForTest)
	}
	if meterName != meterNameForTest || unit != "{decision}" {
		t.Errorf("the counter is on meter %q with unit %q, want %q and {decision}", meterName, unit, meterNameForTest)
	}
	if got := r.counts(); got["allowed"] != counts["allowed"] {
		t.Errorf("the collected allowed count %d is not the layer's %d", counts["allowed"], got["allowed"])
	}
	for o := range r.p.counts {
		name := outcome(o).String()
		if _, present := counts[name]; !present {
			t.Errorf("no point for %s: a series that does not exist before its first request cannot be alerted on", name)
		}
	}
	if counts["denied"] == 0 || counts["no-identity"] == 0 || counts["unguarded"] == 0 || counts["unavailable"] == 0 {
		t.Errorf("the counts %v do not reach every refusing outcome", counts)
	}

	if err := r.p.dropMeter(); err != nil {
		t.Fatalf("withdrawing the counter: %v", err)
	}
	var after metricdata.ResourceMetrics
	if err := collected.Collect(context.Background(), &after); err != nil {
		t.Fatalf("collecting after the close: %v", err)
	}
	if again, _, _ := decisionCounts(after); again != nil {
		t.Fatalf("the counter is still collected after Close: %v: the callback was not withdrawn", again)
	}
}

const meterNameForTest = "authz"

// gateDeny makes the rig's engine refuse the next request and gateFail make it
// fail; both are helpers for the metric case, which needs one request of each
// outcome from one rig.
func (r *rig) gateDeny() { r.enforcer.answer = func(authz.Query) (bool, error) { return false, nil } }

func (r *rig) gateFail(err error) {
	r.enforcer.answer = func(authz.Query) (bool, error) { return false, err }
}

// The descriptor is what the design says: the module's name, what it requires,
// what it delivers with which claims, and that its callbacks are set.
func TestTheDescriptorIsWhatTheDesignSays(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ConfigNamespace != ModuleName {
		t.Fatalf("the module is %q and the namespace %q, want %q for both", m.Name, ConfigNamespace, ModuleName)
	}
	requires := map[core.Token]bool{}
	for _, req := range m.Requires {
		requires[req.Token] = req.Optional
	}
	if optional, ok := requires[(*authz.Enforcer)(nil)]; !ok || optional {
		t.Errorf("the engine is required and not optional: %v", m.Requires)
	}
	if optional, ok := requires[(*authz.Catalog)(nil)]; !ok || optional {
		t.Errorf("the catalog is required and not optional: %v", m.Requires)
	}
	if optional, ok := requires[(*log.Logger)(nil)]; !ok || !optional {
		t.Errorf("the logger is optional: %v", m.Requires)
	}
	if optional, ok := requires[(*observability.MeterProvider)(nil)]; !ok || !optional {
		t.Errorf("the meter provider is optional: %v", m.Requires)
	}
	exclusive := map[core.Token]bool{}
	for _, p := range m.Provides {
		exclusive[p.Token] = p.Exclusive
	}
	for _, tok := range []core.Token{(*authz.Gate)(nil), (*Guard)(nil), (*authn.Decider)(nil)} {
		if !exclusive[tok] {
			t.Errorf("the %T delivery is not exclusive: %v", tok, m.Provides)
		}
	}
	if m.Prepare == nil || m.New == nil || m.Init == nil || m.Start == nil || m.Close == nil {
		t.Fatal("a callback of the descriptor is missing")
	}
}

// The module registers itself with the process registry: importing the package
// is what puts the enforcement middleware and the decider in a binary's
// available set, and a host writes neither the descriptor nor a Register call.
// Removing the init registration leaves the module undiscoverable.
func TestTheProcessRegistryCarriesTheModule(t *testing.T) {
	registered, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("importing the package did not register a module named %q with the process registry", ModuleName)
	}
	if registered.Name != ModuleName {
		t.Errorf("the registered module is named %q, want %q", registered.Name, ModuleName)
	}
}

// The middleware declares what the design says: the name, the anchor it stands
// for, the authorizing stage, and the admission anchor it sits inside.
func TestTheMiddlewareDeclaresWhatTheDesignSays(t *testing.T) {
	mw := newRig(t).p.middlewareFor("public")
	if mw.Name != ModuleName || mw.Stage != speedhttp.StageAuthorize || mw.Handler == nil {
		t.Fatalf("the layer is %q in stage %v, want %q in the authorizing stage", mw.Name, mw.Stage, ModuleName)
	}
	if len(mw.Provides) != 1 || mw.Provides[0] != core.Token((*authz.Gate)(nil)) {
		t.Fatalf("the layer stands for %v, want the declaration anchor alone", mw.Provides)
	}
	if len(mw.After) != 1 || mw.After[0] != core.Token((*tenant.Admission)(nil)) {
		t.Fatalf("the layer sits after %v, want the admission anchor", mw.After)
	}
}

// The registry delivers all three faces from one instance, holding what a
// product built by hand cannot: that New takes the engine and the catalog up
// from the registry the way a host's command does, and that the descriptor
// provides the three capabilities under the names the command tree uses.
func TestTheRegistryDeliversAllThreeFaces(t *testing.T) {
	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))

	var gate authz.Gate
	var guardAPI Guard
	var decider authn.Decider
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			if gate, err = core.Resolve[authz.Gate](reg); err != nil {
				return err
			}
			if guardAPI, err = core.Resolve[Guard](reg); err != nil {
				return err
			}
			if decider, err = core.Resolve[authn.Decider](reg); err != nil {
				return err
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the assembly did not drive: %v", err)
	}
	if gate == nil || guardAPI == nil || decider == nil {
		t.Fatalf("a face was not delivered: gate %v guard %v decider %v", gate, guardAPI, decider)
	}
	if any(gate) != any(guardAPI) || any(guardAPI) != any(decider) {
		t.Error("the three faces are not one instance of this module's product")
	}
}

// The module requires the engine and the catalog: a command that brings it in
// without either fails the startup, before anything is constructed.
func TestTheGuardNeedsAnEngineAndACatalog(t *testing.T) {
	t.Run("no engine", func(t *testing.T) {
		reg := authztest.Registry(t, guardedBody, Module())
		authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
		err := reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{ModuleName}})
		if !errors.Is(err, core.ErrMissingProvider) {
			t.Fatalf("a drive with no engine ended with %v, want ErrMissingProvider", err)
		}
	})
	t.Run("no catalog", func(t *testing.T) {
		reg := authztest.Registry(t, guardedBody, Module())
		authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
		err := reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{ModuleName}})
		if !errors.Is(err, core.ErrMissingProvider) {
			t.Fatalf("a drive with no catalog ended with %v, want ErrMissingProvider", err)
		}
	})
}

// A second provider of any of the three faces fails the startup: each is
// delivered exclusively, and the stand-ins claim nothing.
func TestASecondProviderOfAnyFaceFailsTheStartup(t *testing.T) {
	for name, provider := range map[string]authztest.Provider{
		"the declaration anchor": authztest.GateProvider(authztest.NewGate()),
		"the handler API":        {Name: "stand-in.guard", Token: (*Guard)(nil), Instance: guardStub{}},
	} {
		t.Run(name, func(t *testing.T) {
			reg := authztest.Registry(t, guardedBody, Module())
			authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
			authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
			authztest.AddProvider(reg, provider)
			err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart, Participants: []string{ModuleName}})
			if !errors.Is(err, core.ErrExclusiveViolated) {
				t.Fatalf("a second provider of %s ended the drive with %v, want ErrExclusiveViolated", name, err)
			}
		})
	}
}

// A command that guards routes and names no entry point fails wiring with
// ErrNoRouter, naming this module.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
	err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: []string{ModuleName}})
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// An undeclared disposition outside the closed set fails the startup at Prepare,
// reading the configuration before anything is constructed.
func TestAnIllegalUndeclaredValueFailsPrepare(t *testing.T) {
	body := `{"authz":{"guard":{"endpoints":["public"],"undeclared":"OPEN"}}}`
	reg := authztest.Registry(t, body, Module())
	err := reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{ModuleName}})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive ended with %v, want authn.ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), undeclaredKey) {
		t.Fatalf("the failure does not name the key: %v", err)
	}
}

// A route that declares a permission the catalog does not hold fails the
// startup at Start, naming the route and the permission: a route that decides on
// a permission nothing declares is a startup failure, not a route that decides
// nothing.
func TestARouteOnAnUndeclaredPermissionFailsStart(t *testing.T) {
	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
	authztest.AddProvider(reg, authztest.Provider{
		Name:     "stand-in.router",
		Token:    (*speedhttp.Router)(nil),
		Instance: routerStub{},
	})
	authztest.AddProvider(reg, authztest.Provider{
		Name:     "stand-in.logger",
		Token:    (*log.Logger)(nil),
		Instance: logStub{logger: slog.Default()},
	})
	reg.Register(registrantModule)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{ModuleName, registrantModule.Name},
	})
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownPermission", err)
	}
	for _, want := range []string{nethttp.MethodGet, "/orders", string(authz.PermissionAccountDelete)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// A failed Start leaves no resolved translation installed: the set Init built is
// given back when Start refuses the assembly, so a later assembly sharing the
// process does not read a set this one never accepted.
//
// The drive carries a module of its own that declares a row, so the set it
// installs differs from the built-in rows alone. Without that the two states
// this case tells apart — cleared, and left installed — would hold the same
// rows, and the assertion would pass whatever Start did.
func TestAFailedStartClearsTheProcessTranslation(t *testing.T) {
	translator := declaredModule("stand-in.translator", map[authz.Permission]authz.Translation{
		declaredPermission: {Object: "deal", Action: "read"},
	})
	t.Cleanup(translation.Reset)

	reg := authztest.Registry(t, guardedBody, Module())
	authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
	authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
	authztest.AddProvider(reg, authztest.Provider{
		Name:     "stand-in.router",
		Token:    (*speedhttp.Router)(nil),
		Instance: routerStub{},
	})
	authztest.AddProvider(reg, authztest.Provider{
		Name:     "stand-in.logger",
		Token:    (*log.Logger)(nil),
		Instance: logStub{logger: slog.Default()},
	})
	reg.Register(registrantModule)
	reg.Register(translator)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{ModuleName, registrantModule.Name, translator.Name},
	})
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownPermission", err)
	}
	if _, _, ok := translation.Lookup(declaredPermission); ok {
		t.Errorf("a failed Start left the resolved translation installed: %q still translates", declaredPermission)
	}
}

// registrantModule declares GET /orders as needing a permission the policy
// stand-in does not hold, the way a module writes its routes from its own Init.
var registrantModule = core.Module{
	Name:     "stand-in.registrant",
	Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
	New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
	Init: func(_ context.Context, reg *core.Registry, _ any) error {
		gate, err := core.Resolve[authz.Gate](reg)
		if err != nil {
			return err
		}
		gate.Require(nethttp.MethodGet, "/orders", authz.PermissionAccountDelete)
		return nil
	},
}

// guardStub is a second implementation of the handler API for the exclusivity
// cases.
type guardStub struct{}

func (guardStub) Allowed(*gin.Context, authz.Permission) (bool, error)  { return false, nil }
func (guardStub) RequirePermission(*gin.Context, authz.Permission) bool { return false }
func (guardStub) RequirePermissionIn(*gin.Context, authz.Permission, authz.Domain) bool {
	return false
}

// routerStub stands for an entry point module: an endpoint whose registrations
// are dropped, so wiring succeeds without a listener.
type routerStub struct{}

func (routerStub) Endpoint(string) (speedhttp.Endpoint, error) { return endpointStub{}, nil }
func (routerStub) Endpoints() []string                         { return []string{"public"} }

// endpointStub accepts registrations and does nothing with them.
type endpointStub struct{}

func (endpointStub) Route(string, string, ...gin.HandlerFunc) {}
func (endpointStub) Use(speedhttp.Middleware)                 {}
func (endpointStub) Accepting() bool                          { return true }

// logStub delivers the logging capability over one logger.
type logStub struct{ logger *slog.Logger }

func (l logStub) Named(string) *slog.Logger { return l.logger }
func (logStub) Redaction() log.Redaction    { return noRules{} }

// noRules takes the redaction registrations and masks nothing.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// The startup diagnostics name the guarded endpoints and the undeclared
// disposition, and the open disposition — the one configuration that lets an
// undeclared route through — is written at Warn, so an assembly that switched
// the enforcement off has a line saying so.
func TestStartupWritesTheGuardedEndpointsAndWarnsWhenOpen(t *testing.T) {
	drive := func(t *testing.T, undeclared string) []record {
		t.Helper()
		logs := newRecorder()
		body := `{"authz":{"guard":{"endpoints":["public","admin"],"undeclared":"` + undeclared + `"}}}`
		reg := authztest.Registry(t, body, Module())
		authztest.AddProvider(reg, authztest.EnforcerProvider(authztest.NewEnforcer()))
		authztest.AddProvider(reg, authztest.CatalogProvider(authztest.NewCatalog(authz.PermissionAccountRead)))
		authztest.AddProvider(reg, authztest.Provider{Name: "stand-in.router", Token: (*speedhttp.Router)(nil), Instance: routerStub{}})
		authztest.AddProvider(reg, authztest.Provider{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: slog.New(logs)}})
		if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: []string{ModuleName}}); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		var out []record
		for _, rec := range logs.all() {
			if strings.Contains(rec.msg, ModuleName) {
				out = append(out, rec)
			}
		}
		return out
	}

	t.Run("closed names the endpoints and does not warn", func(t *testing.T) {
		lines := drive(t, closedValue)
		var in []record
		for _, rec := range lines {
			if strings.Contains(rec.msg, "in force") {
				in = append(in, rec)
			}
			if rec.level == slog.LevelWarn {
				t.Errorf("a closed disposition warned: %q", rec.msg)
			}
		}
		if len(in) != 1 {
			t.Fatalf("%d lines say the layer is in force, want 1: %+v", len(in), lines)
		}
		if g := in[0].attrs["guarded"]; !strings.Contains(g, "public") || !strings.Contains(g, "admin") {
			t.Errorf("the line lists the guarded endpoints as %q, want both", g)
		}
		if in[0].attrs["undeclared"] != closedValue {
			t.Errorf("the line says undeclared is %q, want %q", in[0].attrs["undeclared"], closedValue)
		}
	})

	t.Run("open warns", func(t *testing.T) {
		lines := drive(t, openValue)
		warned := false
		var in []record
		for _, rec := range lines {
			if rec.level == slog.LevelWarn && strings.Contains(rec.msg, undeclaredKey) {
				warned = true
			}
			if strings.Contains(rec.msg, "in force") {
				in = append(in, rec)
			}
		}
		if !warned {
			t.Fatalf("the open disposition left no Warn naming %s: %+v", undeclaredKey, lines)
		}
		if len(in) != 1 {
			t.Fatalf("%d lines say the layer is in force, want 1: %+v", len(in), lines)
		}
		if in[0].attrs["undeclared"] != openValue {
			t.Errorf("the line says undeclared is %q, want %q", in[0].attrs["undeclared"], openValue)
		}
	})
}

// waitLimit bounds every wait in the live case, so a startup that never reaches
// Serve fails the case with a message rather than the binary at the global
// timeout.
const waitLimit = 10 * time.Second

// liveBody is the configuration the live assembly loads: one endpoint, the
// resolution layer guarding it with the header extractor, and this module
// guarding the same endpoint.
const liveBody = `{
	"http": {"listeners": {"public": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}}},
	"tenant": {"resolve": {"endpoints": ["public"], "extractor": "header"}},
	"authz": {"guard": {"endpoints": ["public"]}}
}`

// rosterStub is the tenant roster, with the name a request carries mapped to a
// distinct identifier, so that a query built from the name and one built from
// the identifier cannot be confused.
type rosterStub struct{ tenant.Catalog }

func (rosterStub) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	if name == "acme-name" {
		return tenant.Tenant{ID: "t-acme", Name: "acme-name", Status: tenant.StatusActive}, true, nil
	}
	return tenant.Tenant{}, false, nil
}

// identifyModule is a stand-in for the gateway: it injects an identity into
// every request on the endpoint, the way the real gateway does, so that the
// request this layer decides carries the identity and access level its branches
// read.
func identifyModule(t *testing.T) core.Module {
	opts := []authntest.Option{
		authntest.WithAccount("alice"),
		authntest.WithAccess(authn.AccessRequired),
	}
	return core.Module{
		Name:     "stand-in.identify",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint("public")
			if err != nil {
				return err
			}
			ep.Use(speedhttp.Middleware{
				Name:  "stand-in.identify",
				Stage: speedhttp.StageIdentify,
				Handler: func(c *gin.Context) {
					c.Request = c.Request.WithContext(authntest.Context(t, c.Request.Context(), opts...))
					c.Next()
				},
			})
			return nil
		},
	}
}

// liveRegistrant registers GET /orders and declares it as needing
// authz.PermissionAccountRead, the way a module writes its routes from its own
// Init.
var liveRegistrant = core.Module{
	Name: "stand-in.registrant",
	Requires: []core.Requirement{
		{Token: (*speedhttp.Router)(nil)},
		{Token: (*authz.Gate)(nil)},
	},
	New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
	Init: func(_ context.Context, reg *core.Registry, _ any) error {
		router, err := core.Resolve[speedhttp.Router](reg)
		if err != nil {
			return err
		}
		ep, err := router.Endpoint("public")
		if err != nil {
			return err
		}
		ep.Route(nethttp.MethodGet, "/orders", func(c *gin.Context) { c.String(nethttp.StatusOK, "reached") })
		g, err := core.Resolve[authz.Gate](reg)
		if err != nil {
			return err
		}
		g.Require(nethttp.MethodGet, "/orders", authz.PermissionAccountRead)
		return nil
	},
}

// The live case runs the real entry point on a loopback port with the real
// tenant resolution layer and this module on the chain, and reads one request's
// decision end to end: the query this module puts to the engine names the tenant
// identifier the roster maps the request's name to — never the name — and the
// account whose hands are on the request.
func TestTheQueryNamesTheTenantIdentifierOnTheChain(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	logs := newRecorder()

	entry := speedhttp.Module()
	resolver := resolve.Module()
	guardMod := Module()
	reader := authntest.ConfigReaderAll(t, liveBody, entry, resolver, guardMod)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(entry)
	reg.Register(resolver)
	reg.Register(guardMod)
	reg.Register(core.Module{
		Name:     "stand-in.enforcer",
		Provides: []core.Provision{{Token: (*authz.Enforcer)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return enforcer, nil },
	})
	reg.Register(core.Module{
		Name:     "stand-in.catalog",
		Provides: []core.Provision{{Token: (*authz.Catalog)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return authztest.NewCatalog(authz.PermissionAccountRead), nil
		},
	})
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return logStub{logger: slog.New(logs)}, nil },
	})
	reg.Register(core.Module{
		Name:     "stand-in.roster",
		Provides: []core.Provision{{Token: (*tenant.Catalog)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return rosterStub{}, nil },
	})
	reg.Register(identifyModule(t))
	reg.Register(liveRegistrant)
	servedCh := make(chan struct{})
	reg.Register(core.Module{
		Name: "stand-in.served",
		Serve: func(context.Context, *core.Registry, any) error {
			close(servedCh)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
		if runErr != nil {
			t.Errorf("the assembly ended with %v", runErr)
		}
	}
	t.Cleanup(stop)

	select {
	case <-servedCh:
	case <-done:
		t.Fatalf("the run ended before it served: %v", runErr)
	case <-time.After(waitLimit):
		t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
	}

	var address string
	for _, rec := range logs.all() {
		if rec.msg == "an HTTP listener is listening" && rec.attrs["listener"] == "public" {
			address = rec.attrs["address"]
		}
	}
	if address == "" {
		t.Fatal("no listening address for the public endpoint")
	}

	req, err := nethttp.NewRequestWithContext(t.Context(), nethttp.MethodGet, "http://"+address+"/orders", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("X-Tenant", "acme-name")
	client := &nethttp.Client{Timeout: waitLimit, Transport: &nethttp.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("answered %d, want 200", resp.StatusCode)
	}

	want := authz.Query{Subject: "alice", Domain: "t-acme", Object: "account", Action: "read"}
	got := enforcer.Asked()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the engine was asked %v, want exactly %v: the tenant identifier and not its name", got, want)
	}
}
