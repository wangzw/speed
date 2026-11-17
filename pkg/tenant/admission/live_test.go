package admission_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/admission"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

// The cases built on this file hold what only the whole chain can decide: where
// the layer lands among the layers outside and inside it, which requests the
// real gateway lets reach it, and that its 404 is the resolution layer's. The
// entry point, the gateway, the resolution layer and the admission layer are the
// real modules on a loopback port the system picks, assembled by the real
// registry. The roster, the memberships, the credential backend and the audit,
// rate-limiting and authorization layers are stand-ins: the last three are
// layers that declare what the real ones declare, so that the placement the
// constraints give is the one under test.

const (
	acme   = "acme"
	globex = "globex"
	alice  = authn.AccountID("alice")
	bob    = authn.AccountID("bob")
	carol  = authn.AccountID("carol")
)

// waitLimit bounds every wait, so that a startup that never reaches Serve fails
// the case with a message and not the binary at the global timeout.
var waitLimit = coretest.Scale(60 * time.Second)

// record is a log record a case reads back.
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

func (r *recorder) withMessage(prefix string) []record {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []record
	for _, rec := range *r.records {
		if strings.HasPrefix(rec.msg, prefix) {
			out = append(out, rec)
		}
	}
	return out
}

type namedLogger struct{ logger *slog.Logger }

func (l namedLogger) Named(string) *slog.Logger { return l.logger }
func (namedLogger) Redaction() log.Redaction    { return noRules{} }

// noRules takes the redaction registrations of the gateway and masks nothing:
// what these cases read is the records, not their masking.
type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// rosterStub is the tenant roster: name is identifier, both tenants served.
type rosterStub struct{ tenant.Catalog }

func (rosterStub) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	return rosterStub{}.Get(context.Background(), tenant.ID(name))
}

func (rosterStub) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	switch string(id) {
	case acme, globex:
		return tenant.Tenant{ID: id, Name: string(id), Status: tenant.StatusActive}, true, nil
	}
	return tenant.Tenant{}, false, nil
}

// membersStub is the account module's memberships.
type membersStub struct {
	authn.Memberships
	members map[string]bool
}

func (m membersStub) IsMember(_ context.Context, id authn.AccountID, tenantID string) (bool, error) {
	return m.members[string(id)+"@"+tenantID], nil
}

// credentials is the credential backend: the bearer token names the caller.
type credentials struct{}

func (credentials) Name() string { return "stub" }

func (credentials) Accepts() authn.Accepts {
	return authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}
}

func (credentials) Verify(_ context.Context, c authn.Credential) authn.Verdict {
	p := authn.Principal{
		Account:   authn.AccountID(c.Value),
		Methods:   []authn.Method{authn.MethodPassword},
		Assurance: authn.AssuranceSingle,
		AuthTime:  time.Now(),
	}
	if c.Value == "impersonating" {
		// bob acting as alice, confined to acme.
		p.Account, p.Actor, p.Session, p.Scope = alice, bob, "session-1", acme
	}
	return authn.Authenticated(p)
}

// layerModule is a module that registers one layer of its own on an endpoint,
// declaring what a real layer of that kind declares.
func layerModule(name, endpoint string, mw speedhttp.Middleware) core.Module {
	mw.Name = name
	mw.Handler = func(c *gin.Context) { c.Next() }
	return core.Module{
		Name: "stand-in." + name,
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*authn.Gate)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			ep, err := router.Endpoint(endpoint)
			if err != nil {
				return err
			}
			ep.Use(mw)
			return nil
		},
	}
}

// chain is one assembly, and what a case varies in it.
type chain struct {
	// noAudit and noLimiter leave out the layers that stand for those
	// capabilities.
	noAudit, noLimiter bool
	// mountEndpoint is written to the gateway's ordinary mount endpoint when
	// set.
	mountEndpoint string
	// admissionEndpoints is the admission layer's list; the default guards
	// "public" alone.
	admissionEndpoints string
	// noAdmission leaves the admission layer out.
	noAdmission bool
	// lateOrders makes the audit and rate-limiting stand-ins carry an Order
	// that prefers the inner edge of their own stage, so that where they land
	// relative to admission is the stage order's doing rather than an Order.
	lateOrders bool
}

// served is a running assembly.
type served struct {
	logs      *recorder
	addresses map[string]string
}

// body is the configuration the assembly loads.
func (c chain) body() string {
	admitted := c.admissionEndpoints
	if admitted == "" {
		admitted = `["public"]`
	}
	mount := ""
	if c.mountEndpoint != "" {
		mount = fmt.Sprintf(`,"mount":{"endpoints":{"default":%q}}`, c.mountEndpoint)
	}
	return fmt.Sprintf(`{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}, "admin": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}, "admin": {"listener": "admin"}}},
		"authn": {"gate": {"endpoints": ["public", "admin"]%s}},
		"tenant": {"resolve": {"endpoints": ["public"], "extractor": "header"},
		           "admission": {"endpoints": %s}}
	}`, mount, admitted)
}

// run builds the assembly and runs it. With use set it waits for Serve and hands
// the case the addresses; the run is ended when the case is. It returns what the
// run returned when it ended before it served, which is how a startup failure
// is read.
func (c chain) run(t *testing.T, use func(s *served)) error {
	t.Helper()
	gateModule := gate.Module()
	entry := speedhttp.Module()
	resolver := resolve.Module()
	layer := admission.Module()
	reader := authntest.ConfigReaderAll(t, c.body(), gateModule, entry, resolver, layer)

	logs := newRecorder()
	servedCh := make(chan struct{})

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(gateModule)
	reg.Register(entry)
	reg.Register(resolver)
	if !c.noAdmission {
		reg.Register(layer)
	}
	reg.Register(core.Module{
		Name:     "stand-in.credentials",
		Provides: []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return credentials{}, nil },
	})
	reg.Register(core.Module{
		Name:     "stand-in.roster",
		Provides: []core.Provision{{Token: (*tenant.Catalog)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return rosterStub{}, nil },
	})
	reg.Register(core.Module{
		Name:     "stand-in.memberships",
		Provides: []core.Provision{{Token: (*authn.Memberships)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return membersStub{members: map[string]bool{"alice@acme": true, "bob@globex": true}}, nil
		},
	})
	reg.Register(core.Module{
		Name:     "stand-in.logger",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return namedLogger{logger: slog.New(logs)}, nil
		},
	})
	auditOrder, limiterOrder := 0, 0
	if c.lateOrders {
		auditOrder, limiterOrder = 100, 100
	}
	if !c.noAudit {
		reg.Register(layerModule("audit", "public", speedhttp.Middleware{
			Provides: []core.Token{(*audit.Audit)(nil)},
			Stage:    speedhttp.StageRecord,
			Order:    auditOrder,
		}))
	}
	if !c.noLimiter {
		reg.Register(layerModule("ratelimit", "public", speedhttp.Middleware{
			Provides: []core.Token{(*ratelimit.Limiter)(nil)},
			Stage:    speedhttp.StageThrottle,
			Order:    limiterOrder,
		}))
	}
	// The authorization layer of the design: it shares admission's stage and
	// points at the admission anchor, so the constraint is what puts it inside.
	reg.Register(layerModule("authorization", "public", speedhttp.Middleware{
		Provides: []core.Token{(*authorization)(nil)},
		Stage:    speedhttp.StageAuthorize,
		After:    []core.Token{(*tenant.Admission)(nil)},
	}))
	reg.Register(core.Module{
		Name: "registrant",
		Requires: []core.Requirement{
			{Token: (*speedhttp.Router)(nil)},
			{Token: (*authn.Gate)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			guard, err := core.Resolve[authn.Gate](reg)
			if err != nil {
				return err
			}
			for _, name := range []string{"public", "admin"} {
				ep, err := router.Endpoint(name)
				if err != nil {
					return err
				}
				ep.Route(nethttp.MethodGet, "/orders", reached)
				ep.Route(nethttp.MethodGet, "/impersonable", reached)
			}
			guard.Impersonable("admin", nethttp.MethodGet, "/impersonable")
			guard.Impersonable("public", nethttp.MethodGet, "/impersonable")
			return nil
		},
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
	stopped := false
	stop := func() error {
		if stopped {
			return runErr
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
		return runErr
	}
	t.Cleanup(func() { _ = stop() })

	select {
	case <-servedCh:
	case <-done:
		return runErr
	case <-time.After(waitLimit):
		t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
	}
	addresses := map[string]string{}
	for _, rec := range logs.withMessage("an HTTP listener is listening") {
		addresses[rec.attrs["listener"]] = rec.attrs["address"]
	}
	if use != nil {
		use(&served{logs: logs, addresses: addresses})
	}
	if err := stop(); err != nil {
		t.Errorf("the assembly did not stop cleanly: %v", err)
	}
	return nil
}

// authorization is the anchor of the authorization layer, which does not exist
// yet; only its placement is under test.
type authorization interface{ authorizing() }

func reached(c *gin.Context) {
	id, ok := authn.FromContext(c.Request.Context())
	who := "nobody"
	if ok {
		who = string(id.Account())
		if id.Impersonating() {
			who += " (impersonated by " + string(id.Actor()) + ")"
		}
	}
	tenantID, _ := tenant.FromContext(c.Request.Context())
	c.String(nethttp.StatusOK, "reached as %s in %q", who, tenantID)
}

// reply is what a client got back.
type reply struct {
	status int
	header nethttp.Header
	body   string
}

func (s *served) get(t *testing.T, endpoint, path, token, tenantName string) reply {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(t.Context(), nethttp.MethodGet, "http://"+s.addresses[endpoint]+path, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if tenantName != "" {
		req.Header.Set("X-Tenant", tenantName)
	}
	client := &nethttp.Client{Timeout: waitLimit, Transport: &nethttp.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	return reply{status: resp.StatusCode, header: resp.Header.Clone(), body: string(body)}
}

// chainOf returns the layer names of one endpoint's chain listing, outermost
// first, with the two layers the entry point puts outside everything left out.
func chainOf(t *testing.T, logs *recorder, endpoint string) []string {
	t.Helper()
	for _, rec := range logs.withMessage("an HTTP endpoint's chain is assembled") {
		if rec.attrs["endpoint"] != endpoint {
			continue
		}
		var names []string
		for _, part := range strings.Split(rec.attrs["chain"], " -> ") {
			name, _, _ := strings.Cut(part, "(")
			// The listing prefixes the first layer of a stage with the stage
			// marker, joined by a space: "[identify] authn.gate".
			if strings.HasPrefix(name, "[") {
				_, rest, found := strings.Cut(name, "] ")
				if found {
					name = rest
				}
			}
			if name == "http:base" || name == "http:fallback" {
				continue
			}
			names = append(names, name)
		}
		return names
	}
	t.Fatalf("no chain listing for the endpoint %q", endpoint)
	return nil
}

// TestTheLayerSitsInsideResolutionAuditAndRateLimitingAndOutsideAuthorization
// reads the chain the real entry point assembled, the check the design lists as
// not yet made: the position is derived from the After entries by the ordering
// algorithm, and the algorithm was not run against an assembly when it was
// written down. The authorization stand-in prefers the outermost position. The
// audit and rate-limiting stand-ins carry the Orders of the real layers in the
// first case and an Order that prefers to sit inside admission in the second, so
// that no position asserted here follows from an Order alone; the third and
// fourth leave one of them out, which is the assembly in which the other's
// constraint is the only thing placing the layer.
func TestTheLayerSitsInsideResolutionAuditAndRateLimitingAndOutsideAuthorization(t *testing.T) {
	for name, c := range map[string]chain{
		"the Orders of the real layers": {},
		"Orders that prefer the inside": {lateOrders: true},
		"no rate limiting":              {noLimiter: true},
		"no rate limiting, late audit":  {noLimiter: true, lateOrders: true},
		"no audit capture":              {noAudit: true, lateOrders: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.run(t, func(s *served) {
				names := chainOf(t, s.logs, "public")
				at := func(name string) int { return slices.Index(names, name) }
				admitAt := at("tenant.admission")
				if admitAt < 0 {
					t.Fatalf("the chain %v has no admission layer", names)
				}
				outside := []string{"authn.gate", "tenant.resolve"}
				if !c.noAudit {
					outside = append(outside, "audit")
				}
				if !c.noLimiter {
					outside = append(outside, "ratelimit")
				}
				for _, layer := range outside {
					if at(layer) < 0 || at(layer) >= admitAt {
						t.Errorf("the chain %v does not put %s outside admission", names, layer)
					}
				}
				if at("authorization") <= admitAt {
					t.Errorf("the chain %v does not put admission outside authorization", names)
				}
				// The layers that stand outside admission stand in the order
				// the stages give: the gateway, then resolution, then rate
				// limiting in the throttling stage, then the audit capture in
				// the recording stage inside it.
				order := []string{"authn.gate", "tenant.resolve"}
				if !c.noLimiter {
					order = append(order, "ratelimit")
				}
				if !c.noAudit {
					order = append(order, "audit")
				}
				for i := 1; i < len(order); i++ {
					if at(order[i-1]) >= at(order[i]) {
						t.Errorf("the chain %v does not put %s outside %s", names, order[i-1], order[i])
					}
				}
			})
			if err != nil {
				t.Fatalf("the assembly did not start: %v", err)
			}
		})
	}
}

// TestTheStageOrderPlacesAdmissionWithoutTheOuterStages holds the placement in
// an assembly that leaves out the recording stage and the throttling stage. The
// stage order alone puts admission inside identity and outside authorization, so
// the layer keeps that position without a constraint of its own and reports no
// dropped one.
func TestTheStageOrderPlacesAdmissionWithoutTheOuterStages(t *testing.T) {
	err := chain{noAudit: true, noLimiter: true}.run(t, func(s *served) {
		names := chainOf(t, s.logs, "public")
		if slices.Contains(names, "audit") || slices.Contains(names, "ratelimit") {
			t.Fatalf("the chain %v carries a layer the assembly left out", names)
		}
		gateAt, resolveAt := slices.Index(names, "authn.gate"), slices.Index(names, "tenant.resolve")
		admitAt, authzAt := slices.Index(names, "tenant.admission"), slices.Index(names, "authorization")
		if gateAt < 0 || gateAt >= resolveAt || resolveAt >= admitAt || admitAt >= authzAt {
			t.Fatalf("the chain %v does not put admission after identity and before authorization", names)
		}
		for _, rec := range s.logs.withMessage("an HTTP middleware constraint landed on no layer") {
			if rec.attrs["middleware"] == "tenant.admission" {
				t.Errorf("the layer reports a dropped constraint %v, and it declares none", rec.attrs)
			}
		}
	})
	if err != nil {
		t.Fatalf("the assembly did not start: %v", err)
	}
}

// TestRealRequestsThroughTheWholeChain sends requests over the socket through
// the real gateway, the real resolution layer and this layer, and reads the
// answers.
func TestRealRequestsThroughTheWholeChain(t *testing.T) {
	err := chain{}.run(t, func(s *served) {
		t.Run("a member is let through to the handler with the identity and the tenant", func(t *testing.T) {
			got := s.get(t, "public", "/orders", "alice", acme)
			if got.status != 200 || got.body != `reached as alice in "acme"` {
				t.Fatalf("answered %d %q, want the handler's", got.status, got.body)
			}
		})
		t.Run("a member of another tenant gets the resolution layer's 404, byte for byte", func(t *testing.T) {
			notMember := s.get(t, "public", "/orders", "alice", globex)
			unknown := s.get(t, "public", "/orders", "alice", "nowhere")
			if notMember.status != 404 || unknown.status != 404 {
				t.Fatalf("answered %d and %d, want 404 for both", notMember.status, unknown.status)
			}
			if notMember.body != unknown.body || !headersEqual(notMember.header, unknown.header, "Date") {
				t.Fatalf("a non-member's 404 (%v %q) differs from an unknown tenant's (%v %q): the pair is an oracle for which tenants exist",
					notMember.header, notMember.body, unknown.header, unknown.body)
			}
		})
		t.Run("a request naming no tenant is the resolution layer's, not this layer's", func(t *testing.T) {
			if got := s.get(t, "public", "/orders", "alice", ""); got.status != 404 {
				t.Fatalf("answered %d, want 404", got.status)
			}
		})
		t.Run("no credential is the gateway's 401, and never reaches this layer", func(t *testing.T) {
			got := s.get(t, "public", "/orders", "", acme)
			if got.status != 401 || strings.Contains(got.header.Get("WWW-Authenticate"), "insufficient_user_authentication") {
				t.Fatalf("answered %d with %q, want the gateway's plain 401", got.status, got.header.Get("WWW-Authenticate"))
			}
		})
		t.Run("an endpoint the layer does not guard has no membership check", func(t *testing.T) {
			got := s.get(t, "admin", "/orders", "carol", "")
			if got.status != 200 {
				t.Fatalf("answered %d on the unguarded endpoint, want the handler's", got.status)
			}
		})
	})
	if err != nil {
		t.Fatalf("the assembly did not start: %v", err)
	}
}

func headersEqual(a, b nethttp.Header, ignore ...string) bool {
	a, b = a.Clone(), b.Clone()
	for _, k := range ignore {
		a.Del(k)
		b.Del(k)
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// TestAnImpersonationIsOnlyLetIntoTheGuardedEndpointAndImpersonableRoutes holds
// the contract between the layer and the gateway: the gateway reads the guard
// and admits an impersonating identity to any route of an endpoint the guard
// covers and to the routes registered Impersonable elsewhere, and this layer
// confines it to its tenant there.
func TestAnImpersonationIsOnlyLetIntoTheGuardedEndpointAndImpersonableRoutes(t *testing.T) {
	err := chain{}.run(t, func(s *served) {
		t.Run("any route of the guarded endpoint, in its own tenant", func(t *testing.T) {
			got := s.get(t, "public", "/orders", "impersonating", acme)
			if got.status != 200 || !strings.Contains(got.body, "alice (impersonated by bob)") {
				t.Fatalf("answered %d %q, want it admitted as alice, impersonated by bob", got.status, got.body)
			}
		})
		t.Run("the guarded endpoint in another tenant is the 404 every refusal has", func(t *testing.T) {
			got := s.get(t, "public", "/orders", "impersonating", globex)
			if got.status != 404 {
				t.Fatalf("answered %d, want 404", got.status)
			}
		})
		t.Run("an endpoint the guard does not cover admits it on Impersonable routes alone", func(t *testing.T) {
			if got := s.get(t, "admin", "/impersonable", "impersonating", ""); got.status != 200 {
				t.Fatalf("answered %d on an Impersonable route, want it admitted", got.status)
			}
			if got := s.get(t, "admin", "/orders", "impersonating", ""); got.status == 200 {
				t.Fatalf("an impersonating identity reached an ordinary route of an unguarded endpoint: %q", got.body)
			}
		})
	})
	if err != nil {
		t.Fatalf("the assembly did not start: %v", err)
	}
}

// TestWithoutTheLayerAnImpersonationReachesImpersonableRoutesAlone holds the
// control: with no guard in the assembly the gateway admits an impersonating
// identity to Impersonable routes alone, on every endpoint.
func TestWithoutTheLayerAnImpersonationReachesImpersonableRoutesAlone(t *testing.T) {
	err := chain{noAdmission: true}.run(t, func(s *served) {
		if got := s.get(t, "public", "/orders", "impersonating", acme); got.status == 200 {
			t.Fatalf("an impersonating identity reached an ordinary route with no guard in the assembly: %q", got.body)
		}
		if got := s.get(t, "public", "/impersonable", "impersonating", acme); got.status != 200 {
			t.Fatalf("answered %d on an Impersonable route, want it admitted", got.status)
		}
	})
	if err != nil {
		t.Fatalf("the assembly did not start: %v", err)
	}
}

// TestTheEndpointTheAuthenticationRoutesAreMountedOnIsNotOneTheLayerGuards
// reproduces the gateway's refusal to start: the layer answers an impersonating
// request that has no tenant with 404, so guarding the mount endpoint would leave
// an impersonating caller unable to end the impersonation.
func TestTheEndpointTheAuthenticationRoutesAreMountedOnIsNotOneTheLayerGuards(t *testing.T) {
	t.Run("the mount endpoint is guarded by the layer", func(t *testing.T) {
		err := chain{mountEndpoint: "public"}.run(t, nil)
		if !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("the run ended with %v, want authn.ErrInvalidConfig", err)
		}
		if !strings.Contains(err.Error(), "admission") {
			t.Fatalf("the failure does not say the admission layer is the cause: %v", err)
		}
	})
	t.Run("a mount endpoint the layer does not guard starts", func(t *testing.T) {
		if err := (chain{mountEndpoint: "admin"}).run(t, nil); err != nil {
			t.Fatalf("the assembly did not start: %v", err)
		}
	})
}
