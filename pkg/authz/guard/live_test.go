package guard_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/admission"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

const (
	publicEndpoint   = "public"
	internalEndpoint = "internal"
	plainEndpoint    = "plain"

	tenantName = "acme"

	tenantIdentifier = "t-acme"

	listeningMsg = "an HTTP listener is listening"
)

func waitLimit() time.Duration {
	return coretest.Scale(60 * time.Second)
}

type record struct {
	level slog.Level
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

type namedLogger struct{ logger *slog.Logger }

func (l namedLogger) Named(string) *slog.Logger { return l.logger }
func (namedLogger) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type credentials struct{}

func (credentials) Name() string { return "stub" }

func (credentials) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{"bearer"}, Cookies: []string{"sid"}}
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

func (credentials) Challenge(realm string, _ bool) []string {
	return []string{`Bearer realm="` + realm + `"`, `Cookie realm="` + realm + `"`}
}

type roster struct{ tenant.Catalog }

func (roster) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	if name == tenantName {
		return tenant.Tenant{ID: tenant.ID(tenantIdentifier), Name: tenantName, Status: tenant.StatusActive}, true, nil
	}
	return tenant.Tenant{}, false, nil
}

type memberships struct{ authn.Memberships }

func (memberships) IsMember(_ context.Context, id authn.AccountID, tenantID string) (bool, error) {
	return string(id) == "alice" && tenantID == tenantIdentifier, nil
}

type chain struct {
	undeclared string
}

func (c chain) body() string {
	undeclared := ""
	if c.undeclared != "" {
		undeclared = `, "undeclared": "` + c.undeclared + `"`
	}
	return `{
		"http": {
			"listeners": {
				"public": {"address": "127.0.0.1:0"},
				"internal": {"address": "127.0.0.1:0"},
				"plain": {"address": "127.0.0.1:0"}
			},
			"endpoints": {
				"public": {"listener": "public"},
				"internal": {"listener": "internal"},
				"plain": {"listener": "plain"}
			}
		},
		"authn": {"gate": {"endpoints": ["public"]}},
		"tenant": {
			"resolve": {"endpoints": ["public"], "extractor": "header"},
			"admission": {"endpoints": ["public"]}
		},
		"authz": {"guard": {"endpoints": ["public", "internal"]` + undeclared + `}}
	}`
}

type reply struct {
	status int
	header nethttp.Header
	body   string
}

func (rp reply) reached() bool { return rp.status == nethttp.StatusOK && rp.body == "reached" }

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

func reached(c *gin.Context) {
	c.String(nethttp.StatusOK, "reached")
}

func provide(name string, token core.Token, instance any) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

func registrant(servedCh chan struct{}) core.Module {
	return core.Module{
		Name: "stand-in.registrant",
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*authz.Gate)(nil)},
			{Token: (*authn.Gate)(nil)},
			{Token: (*guard.Guard)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			declarations, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				return err
			}
			gateway, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			guardAPI, err := core.Resolve[guard.Guard](reg)
			if err != nil {
				return err
			}
			for _, name := range []string{publicEndpoint, internalEndpoint, plainEndpoint} {
				ep, err := router.Endpoint(name)
				if err != nil {
					return err
				}
				ep.Route(nethttp.MethodGet, "/orders", reached)
				ep.Route(nethttp.MethodGet, "/open", reached)
				ep.Route(nethttp.MethodGet, "/nobody", reached)
				ep.Route(nethttp.MethodGet, "/guard-api", func(c *gin.Context) {
					if !guardAPI.RequirePermission(c, authz.PermissionAccountRead) {
						return
					}
					c.String(nethttp.StatusOK, "reached")
				})
			}
			declarations.Require(nethttp.MethodGet, "/orders", authz.PermissionAccountRead)
			declarations.Open(nethttp.MethodGet, "/open")
			declarations.Open(nethttp.MethodGet, "/guard-api")
			gateway.Exempt(publicEndpoint, nethttp.MethodGet, "/open")
			return nil
		},
		Serve: func(context.Context, *core.Registry, any) error {
			close(servedCh)
			return nil
		},
	}
}

type live struct {
	t         *testing.T
	addresses map[string]string
	logs      *recorder
	client    *nethttp.Client
}

func (l *live) send(endpoint, method, path string, edits ...func(*nethttp.Request)) reply {
	l.t.Helper()
	addr, ok := l.addresses[endpoint]
	if !ok {
		l.t.Fatalf("endpoint %q has no bound address", endpoint)
	}
	req, err := nethttp.NewRequestWithContext(l.t.Context(), method, "http://"+addr+path, nil)
	if err != nil {
		l.t.Fatalf("building %s %s: %v", method, path, err)
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		l.t.Fatalf("%s %s on %q got no response: %v", method, path, endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.t.Fatalf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header.Clone(), body: string(body)}
}

func (l *live) decided(level slog.Level, outcome string) bool {
	for _, rec := range l.logs.all() {
		if strings.Contains(rec.msg, guard.ModuleName) && rec.attrs["outcome"] == outcome && rec.level == level {
			return true
		}
	}
	return false
}

func (l *live) outcomes() []string {
	var out []string
	for _, rec := range l.logs.all() {
		if strings.Contains(rec.msg, guard.ModuleName) {
			out = append(out, rec.attrs["outcome"])
		}
	}
	return out
}

func asAlice(r *nethttp.Request) { r.Header.Set("Authorization", "Bearer alice") }

func inAcme(r *nethttp.Request) { r.Header.Set("X-Tenant", tenantName) }

func run(t *testing.T, c chain, enforcer *authztest.Enforcer, use func(l *live)) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)

	entry := speedhttp.Module()
	gateway := gate.Module()
	resolver := resolve.Module()
	admitter := admission.Module()
	guardMod := guard.Module()
	reader := authntest.ConfigReaderAll(t, c.body(), entry, gateway, resolver, admitter, guardMod)

	logs := newRecorder()
	servedCh := make(chan struct{})
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(entry)
	reg.Register(gateway)
	reg.Register(resolver)
	reg.Register(admitter)
	reg.Register(guardMod)
	reg.Register(provide("stand-in.backend", (*authn.Verifier)(nil), credentials{}))
	reg.Register(provide("stand-in.roster", (*tenant.Catalog)(nil), roster{}))
	reg.Register(provide("stand-in.members", (*authn.Memberships)(nil), memberships{}))
	reg.Register(provide("stand-in.enforcer", (*authz.Enforcer)(nil), enforcer))
	reg.Register(provide("stand-in.catalog", (*authz.Catalog)(nil), authztest.NewCatalog(authz.PermissionAccountRead)))
	reg.Register(provide("stand-in.logger", (*log.Logger)(nil), namedLogger{logger: slog.New(logs)}))
	reg.Register(registrant(servedCh))

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
	}()
	limit := waitLimit()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(limit):
			t.Errorf("the assembly did not stop within %s", limit)
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
	case <-time.After(limit):
		t.Fatalf("the assembly did not reach Serve within %s", limit)
	}

	addresses := map[string]string{}
	for _, rec := range logs.all() {
		if rec.msg == listeningMsg {
			addresses[rec.attrs["listener"]] = rec.attrs["address"]
		}
	}
	for _, name := range []string{publicEndpoint, internalEndpoint, plainEndpoint} {
		if addresses[name] == "" {
			t.Fatalf("the entry point bound no address for endpoint %q", name)
		}
	}

	use(&live{
		t:         t,
		addresses: addresses,
		logs:      logs,
		client: &nethttp.Client{
			Timeout:   limit,
			Transport: &nethttp.Transport{DisableKeepAlives: true},
		},
	})
	stop()
}

func realmChallenge(endpoint string) string {
	return `Bearer realm="` + endpoint + `", Cookie realm="` + endpoint + `"`
}

// TestAnOpenRouteIsLetThroughForAnyIdentity checks that an open route is let through for any identity.
func TestAnOpenRouteIsLetThroughForAnyIdentity(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	run(t, chain{}, enforcer, func(l *live) {
		t.Run("an identity on the guarded endpoint", func(t *testing.T) {
			if got := l.send(publicEndpoint, nethttp.MethodGet, "/open", asAlice, inAcme); !got.reached() {
				t.Fatalf("answered %d %q, want the handler's", got.status, got.body)
			}
		})
		t.Run("no identity on the guarded endpoint, through the gateway's Exempt", func(t *testing.T) {
			if got := l.send(publicEndpoint, nethttp.MethodGet, "/open", inAcme); !got.reached() {
				t.Fatalf("answered %d %q, want the handler's", got.status, got.body)
			}
		})
		t.Run("no identity on the endpoint this module guards alone", func(t *testing.T) {
			if got := l.send(internalEndpoint, nethttp.MethodGet, "/open"); !got.reached() {
				t.Fatalf("answered %d %q, want the handler's", got.status, got.body)
			}
		})
		if asked := enforcer.Asked(); len(asked) != 0 {
			t.Fatalf("the engine was asked %v for an open route, want nothing", asked)
		}
	})
}

// TestARequiredRouteWithNoIdentityIsTheGateways401Shape checks that a required route with no identity is the gateways 401 shape.
func TestARequiredRouteWithNoIdentityIsTheGateways401Shape(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	run(t, chain{}, enforcer, func(l *live) {
		guardOnly := l.send(internalEndpoint, nethttp.MethodGet, "/orders")
		gatewayOnly := l.send(publicEndpoint, nethttp.MethodGet, "/orders", inAcme)

		for name, rp := range map[string]reply{
			"the endpoint this module guards alone": guardOnly,
			"the guarded endpoint":                  gatewayOnly,
		} {
			if rp.status != nethttp.StatusUnauthorized {
				t.Fatalf("%s answered %d, want 401", name, rp.status)
			}
			body := decodeProblem(t, rp.body)
			if body.Status != nethttp.StatusUnauthorized {
				t.Errorf("%s: the body says %d, want 401", name, body.Status)
			}
			if body.Type != "about:blank" {
				t.Errorf("%s: the no-identity answer carries a problem type: %q", name, body.Type)
			}
		}

		if got, want := guardOnly.header.Get("WWW-Authenticate"), realmChallenge(internalEndpoint); got != want {
			t.Errorf("this module's challenge = %q, want %q", got, want)
		}
		if got, want := gatewayOnly.header.Get("WWW-Authenticate"), realmChallenge(publicEndpoint); got != want {
			t.Errorf("the gateway's challenge = %q, want %q", got, want)
		}
	})
}

// TestAnAllowedIdentityReachesTheHandlerOnTheChain checks that an allowed identity reaches the handler on the chain.
func TestAnAllowedIdentityReachesTheHandlerOnTheChain(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	run(t, chain{}, enforcer, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, "/orders", asAlice, inAcme)
		if !got.reached() {
			t.Fatalf("answered %d %q, want the handler's", got.status, got.body)
		}
		want := authz.Query{Subject: "alice", Domain: authz.Domain(tenantIdentifier), Object: "account", Action: "read"}
		asked := enforcer.Asked()
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("the engine was asked %v, want exactly %v", asked, want)
		}
	})
}

// TestTheMiddlewareDecidesInTheTenantsOwnDomainAlone checks that the middleware decides in the tenants own domain alone.
func TestTheMiddlewareDecidesInTheTenantsOwnDomainAlone(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(q authz.Query) bool { return q.Domain == authz.PlatformDomain })
	run(t, chain{}, enforcer, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, "/orders", asAlice, inAcme)
		if got.status != nethttp.StatusForbidden {
			t.Fatalf("answered %d, want 403: the middleware decides in the request's domain alone", got.status)
		}
		want := authz.Query{Subject: "alice", Domain: authz.Domain(tenantIdentifier), Object: "account", Action: "read"}
		asked := enforcer.Asked()
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("the engine was asked %v, want exactly one query in the tenant's domain %v", asked, want)
		}
	})
}

// TestTheHandlerGuardDecidesInTheTenantsOwnDomain checks that the handler guard decides in the tenants own domain.
func TestTheHandlerGuardDecidesInTheTenantsOwnDomain(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(q authz.Query) bool { return q.Domain == authz.Domain(tenantIdentifier) })
	run(t, chain{}, enforcer, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, "/guard-api", asAlice, inAcme)
		if !got.reached() {
			t.Fatalf("answered %d %q, want the handler's: the Guard API allowed in the tenant's domain", got.status, got.body)
		}
		want := authz.Query{Subject: "alice", Domain: authz.Domain(tenantIdentifier), Object: "account", Action: "read"}
		asked := enforcer.Asked()
		if len(asked) != 1 || asked[0] != want {
			t.Fatalf("the engine was asked %v, want exactly %v: the handler API names the tenant identifier", asked, want)
		}
	})
}

// TestADenialOnTheChainIsA403WithoutThePermission checks that a denial on the chain is a 403 without the permission.
func TestADenialOnTheChainIsA403WithoutThePermission(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return false })
	run(t, chain{}, enforcer, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, "/orders", asAlice, inAcme)
		if got.status != nethttp.StatusForbidden {
			t.Fatalf("answered %d, want 403", got.status)
		}
		body := decodeProblem(t, got.body)
		if body.Type != "urn:speed:authz:forbidden" {
			t.Errorf("type = %q, want urn:speed:authz:forbidden", body.Type)
		}
		if body.Status != nethttp.StatusForbidden {
			t.Errorf("the body says %d, want 403", body.Status)
		}
		if body.Detail == "" || strings.Contains(body.Detail, string(authz.PermissionAccountRead)) {
			t.Errorf("the detail is %q: it has to say why without naming the permission", body.Detail)
		}
		if !l.decided(slog.LevelInfo, "denied") {
			t.Errorf("the denial left no Info line naming the outcome; the module's records are %v", l.outcomes())
		}
	})
}

// TestAnEngineErrorOnTheChainIsA503AndNotADenial checks that an engine error on the chain is a 503 and not a denial.
func TestAnEngineErrorOnTheChainIsA503AndNotADenial(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Failing(errors.New("the engine is down"))
	run(t, chain{}, enforcer, func(l *live) {
		got := l.send(publicEndpoint, nethttp.MethodGet, "/orders", asAlice, inAcme)
		if got.status == nethttp.StatusForbidden {
			t.Fatal("an outage was written as a denial")
		}
		if got.status == nethttp.StatusOK {
			t.Fatal("an outage was written as a pass")
		}
		if got.status != nethttp.StatusServiceUnavailable {
			t.Fatalf("answered %d, want 503", got.status)
		}
		if body := decodeProblem(t, got.body); body.Type == "urn:speed:authz:forbidden" {
			t.Errorf("the outage is written as a denial: type = %q", body.Type)
		}
	})
}

// TestAnUndeclaredRouteIsRefusedByDefaultAndLetThroughWhenOpen checks that an undeclared route is refused by default and let through when open.
func TestAnUndeclaredRouteIsRefusedByDefaultAndLetThroughWhenOpen(t *testing.T) {
	t.Run("closed by default", func(t *testing.T) {
		enforcer := authztest.NewEnforcer()
		enforcer.Answer(func(authz.Query) bool { return true })
		run(t, chain{}, enforcer, func(l *live) {
			got := l.send(publicEndpoint, nethttp.MethodGet, "/nobody", asAlice, inAcme)
			if got.status != nethttp.StatusForbidden {
				t.Fatalf("answered %d, want 403", got.status)
			}
			if body := decodeProblem(t, got.body); body.Type != "urn:speed:authz:forbidden" {
				t.Errorf("type = %q, want urn:speed:authz:forbidden", body.Type)
			}
		})
	})
	t.Run("open lets it through", func(t *testing.T) {
		enforcer := authztest.NewEnforcer()
		enforcer.Answer(func(authz.Query) bool { return true })
		run(t, chain{undeclared: "open"}, enforcer, func(l *live) {
			got := l.send(publicEndpoint, nethttp.MethodGet, "/nobody", asAlice, inAcme)
			if !got.reached() {
				t.Fatalf("answered %d %q, want the handler's under the open disposition", got.status, got.body)
			}
		})
	})
}

// TestAGuardedEndpointDecidesAndAnEndpointLeftOutOfTheListDoesNot checks that a guarded endpoint decides and an endpoint left out of the list does not.
func TestAGuardedEndpointDecidesAndAnEndpointLeftOutOfTheListDoesNot(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	run(t, chain{}, enforcer, func(l *live) {
		decided := l.send(internalEndpoint, nethttp.MethodGet, "/orders")
		if decided.status != nethttp.StatusUnauthorized {
			t.Fatalf("the guarded endpoint answered %d, want this module's 401", decided.status)
		}
		undecided := l.send(plainEndpoint, nethttp.MethodGet, "/orders")
		if !undecided.reached() {
			t.Fatalf("the endpoint left out of the list answered %d %q, want the handler's", undecided.status, undecided.body)
		}
	})
}
