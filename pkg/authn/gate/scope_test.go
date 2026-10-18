package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// impersonatingBackend is a backend that declared the impersonation capability
// and hands back an impersonating identity for every credential.
func impersonatingBackend() *fakeVerifier {
	b := &fakeVerifier{name: "imp", accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}}
	b.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(b, "customer-1")
		p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
		return authn.Authenticated(p)
	}
	return b
}

// TestAnImpersonatingIdentityIsAdmittedOnlyWhereTheRuleSaysSo holds the
// impersonation scope rule from every side it has: the route is registered
// Impersonable, or the endpoint is one the scope guard covers, and nothing else
// admits it. Anywhere else the request is refused with a 401 charged to the
// backend, and with no scope guard in the assembly only the first can hold.
func TestAnImpersonatingIdentityIsAdmittedOnlyWhereTheRuleSaysSo(t *testing.T) {
	for _, c := range []struct {
		name         string
		scope        authn.ScopeGuard
		impersonable []string // routes on "public" registered Impersonable
		endpoint     string
		route        string
		admitted     bool
	}{
		{"a route registered Impersonable, no scope guard", nil, []string{"/end"}, "public", "/end", true},
		{"a route not registered, no scope guard", nil, []string{"/end"}, "public", "/orders", false},
		{"a route not registered, an endpoint the guard covers", fakeScope{"public": true}, nil, "public", "/orders", true},
		{"a route registered Impersonable, an endpoint the guard covers", fakeScope{"public": true}, []string{"/end"}, "public", "/end", true},
		{"a route not registered, an endpoint the guard does not cover", fakeScope{"other": true}, nil, "public", "/orders", false},
		{"a route not registered, a guard that covers nothing", fakeScope{}, nil, "public", "/orders", false},
		{"a route registered Impersonable, a guard that covers nothing", fakeScope{}, []string{"/end"}, "public", "/end", true},
		{"a route registered Impersonable on another endpoint", nil, nil, "other", "/end", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, []string{"other", "public"}, impersonatingBackend())
			g.scope = c.scope
			for _, route := range c.impersonable {
				g.Impersonable("public", "GET", route)
			}
			sealed(t, g, routerWith("other", "public"))

			engine, seen := routedEngine(g, c.endpoint, [2]string{"GET", c.route})
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, withBearer(request("GET", c.route), "t"))

			if c.admitted {
				if w.Code != nethttp.StatusOK || !seen.carried || !seen.id.Impersonating() {
					t.Fatalf("answered %d (identity %v), want the impersonation admitted", w.Code, seen.carried)
				}
				if got := g.tally()["authenticated"]; got != 1 {
					t.Errorf("counted %d time(s) as authenticated, want 1", got)
				}
				return
			}
			if w.Code != nethttp.StatusUnauthorized || seen.reached {
				t.Fatalf("answered %d (reached %v), want a 401 that ends the request", w.Code, seen.reached)
			}
			if got := g.tally()["denied"]; got != 1 {
				t.Errorf("counted %d time(s) as denied, want 1: %v", got, g.tally())
			}
			if w.Header().Get(challengeHeader) == "" {
				t.Errorf("the refusal carries no %s header", challengeHeader)
			}
		})
	}
}

// TestAnIdentityThatIsNotImpersonatingIsNotHeldToTheScopeRule holds the other
// half of the rule: it applies to an actor that differs from the account and to
// nothing else, so an ordinary pass reaches a route that is not registered
// Impersonable on an endpoint the guard does not cover.
func TestAnIdentityThatIsNotImpersonatingIsNotHeldToTheScopeRule(t *testing.T) {
	plain := passes(basicBackend("static", "static."))
	g := testGateway(t, []string{"public"}, plain, impersonatingBackend())
	g.scope = fakeScope{}
	sealed(t, g, routerWith("public"))

	engine, seen := routedEngine(g, "public", [2]string{"GET", "/orders"})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withBasic(request("GET", "/orders"), "alice", "s"))

	if w.Code != nethttp.StatusOK || !seen.carried || seen.id.Impersonating() {
		t.Errorf("answered %d (identity %v), want an ordinary pass", w.Code, seen.carried)
	}
}

// TestAnImpersonationOutOfScopeIsRefusedAfterTheBackendAnswered holds the order
// of the conclusion: the backend is asked, its pass is read, and only then is the
// scope rule applied. The refusal is a 401 and not a 403, since the gateway
// writes no 403, and it is charged to the backend whose pass it declines.
func TestAnImpersonationOutOfScopeIsRefusedAfterTheBackendAnswered(t *testing.T) {
	imp := impersonatingBackend()
	g := testGateway(t, []string{"public"}, imp)
	sealed(t, g, routerWith("public"))

	w, rec := conclusionOf(t, g, "public", func(r *nethttp.Request) *nethttp.Request { return withBearer(r, "t") })

	if w.Code != nethttp.StatusUnauthorized {
		t.Fatalf("answered %d, want %d", w.Code, nethttp.StatusUnauthorized)
	}
	if got := len(imp.credentials()); got != 1 {
		t.Errorf("the backend was asked %d time(s), want once: the scope rule applies to its answer", got)
	}
	if got := attrText(rec, "attributed_to"); got != "imp" {
		t.Errorf("charged to %q, want the backend", got)
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("written at %v, want %v", rec.Level, slog.LevelInfo)
	}
}

// TestAnImpersonationReachesAnOptionalRouteOnlyWhenItIsAlsoImpersonable holds
// that Optional and Impersonable do not stand in for one another: an optional
// route admits a request with no credential, and an impersonating identity is a
// credential that held.
func TestAnImpersonationReachesAnOptionalRouteOnlyWhenItIsAlsoImpersonable(t *testing.T) {
	g := testGateway(t, []string{"public"}, impersonatingBackend())
	g.Optional("public", "GET", "/catalog")
	g.Optional("public", "GET", "/either")
	g.Impersonable("public", "GET", "/either")
	sealed(t, g, routerWith("public"))

	engine, _ := routedEngine(g, "public", [2]string{"GET", "/catalog"}, [2]string{"GET", "/either"})
	for path, want := range map[string]int{"/catalog": nethttp.StatusUnauthorized, "/either": nethttp.StatusOK} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, withBearer(request("GET", path), "t"))
		if w.Code != want {
			t.Errorf("an impersonation on %s answered %d, want %d", path, w.Code, want)
		}
	}
}

// TestAnExemptRouteDoesNotApplyTheScopeRuleBecauseItReadsNoCredential holds that
// an exempt route is reached with no identity at all, so there is nothing for the
// scope rule to apply to, even with an impersonating credential on the request.
func TestAnExemptRouteDoesNotApplyTheScopeRuleBecauseItReadsNoCredential(t *testing.T) {
	imp := impersonatingBackend()
	g := testGateway(t, []string{"public"}, imp)
	g.Exempt("public", "GET", "/status")
	sealed(t, g, routerWith("public"))

	engine, seen := routedEngine(g, "public", [2]string{"GET", "/status"})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withBearer(request("GET", "/status"), "t"))

	if w.Code != nethttp.StatusOK || seen.carried {
		t.Errorf("answered %d (identity %v), want a pass with no identity", w.Code, seen.carried)
	}
	if len(imp.credentials()) != 0 {
		t.Error("an exempt route asked a backend")
	}
}

// TestTheMountEndpointIsHeldToBeingGuardedAndOutsideTheAdmissionLayer holds the
// checks Start makes of the endpoint the capability's own routes are mounted on,
// each with its own sentinel: it has to be an endpoint the entry point has, one
// this gateway guards, and not one the admission layer guards, since that layer
// answers an impersonating request with no tenant context with 404 and an
// impersonating caller could not even end the impersonation. An endpoint the
// configuration names is held to them whether or not a module mounts routes; an
// empty key is held to them only when one does, and then it is the only endpoint
// the entry point declares.
func TestTheMountEndpointIsHeldToBeingGuardedAndOutsideTheAdmissionLayer(t *testing.T) {
	for _, c := range []struct {
		name      string
		guarded   []string
		endpoints []string
		mount     string
		mounted   bool
		scope     authn.ScopeGuard
		want      error
		mentions  string
	}{
		{"an endpoint the entry point does not have", []string{"public"}, []string{"public"}, "typo", false, nil, authn.ErrUnknownEndpoint, "typo"},
		{"an endpoint this gateway does not guard", []string{"public"}, []string{"admin", "public"}, "admin", false, nil, authn.ErrRoutesUnguarded, "admin"},
		{"an endpoint the admission layer guards", []string{"admin", "public"}, []string{"admin", "public"}, "admin", false, fakeScope{"admin": true}, authn.ErrInvalidConfig, "admin"},
		{"a guarded endpoint the admission layer does not", []string{"admin", "public"}, []string{"admin", "public"}, "admin", false, fakeScope{"public": true}, nil, ""},
		{"a guarded endpoint with no admission layer", []string{"admin", "public"}, []string{"admin", "public"}, "admin", false, nil, nil, ""},
		{"an endpoint the admission layer guards, with a module that mounts routes", []string{"admin", "public"}, []string{"admin", "public"}, "admin", true, fakeScope{"admin": true}, authn.ErrInvalidConfig, "admin"},

		// With the key empty and no module that mounts routes there is no endpoint
		// of theirs to judge, and the only endpoint a command has is not refused
		// for what covers it.
		{"no endpoint configured with several declared", []string{"admin", "public"}, []string{"admin", "public"}, "", false, fakeScope{"admin": true, "public": true}, nil, ""},
		{"no endpoint configured, no module mounts routes, the only endpoint covered", []string{"public"}, []string{"public"}, "", false, fakeScope{"public": true}, nil, ""},
		{"no endpoint configured, no module mounts routes, the only endpoint left unguarded", nil, []string{"public"}, "", false, nil, nil, ""},

		// With a module that mounts routes the empty key is the only endpoint, held
		// to every check a written one is.
		{"no endpoint configured, routes mounted, the only endpoint covered", []string{"public"}, []string{"public"}, "", true, fakeScope{"public": true}, authn.ErrInvalidConfig, "public"},
		{"no endpoint configured, routes mounted, the only endpoint not covered", []string{"public"}, []string{"public"}, "", true, fakeScope{}, nil, ""},
		{"no endpoint configured, routes mounted, no admission layer", []string{"public"}, []string{"public"}, "", true, nil, nil, ""},
		{"no endpoint configured, routes mounted, the only endpoint left unguarded", nil, []string{"public"}, "", true, nil, authn.ErrRoutesUnguarded, "public"},
		{"no endpoint configured, routes mounted, several declared", []string{"admin", "public"}, []string{"admin", "public"}, "", true, fakeScope{"admin": true}, authn.ErrRoutesUnconfigured, "admin, public"},
		{"no endpoint configured, routes mounted, none declared", []string{"public"}, nil, "", true, nil, authn.ErrRoutesUnconfigured, mountEndpointItem},
	} {
		t.Run(c.name, func(t *testing.T) {
			set := settings{endpoints: c.guarded, budget: 1, inFlight: 1, mount: mountConfig{Endpoint: c.mount}}
			g := gatewayWith(t, set, basicBackend("static", "static."))
			g.scope = c.scope
			g.mounted = c.mounted
			router := routerWith(c.endpoints...)
			sealed(t, g, router)

			err := g.checkMount(nil)
			if c.want == nil {
				if err != nil {
					t.Fatalf("the mount endpoint was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("the check gave %v, want one wrapping %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure reads %q and does not carry %q", err, c.mentions)
			}
		})
	}
}

// resolutionLayer is a stand-in for the tenant resolution layer's section of the
// configuration, which the gateway reads the endpoints of at Start. The
// endpoint list is required, as it is in the real layer's schema: a command
// that takes the layer in and does not write the list is a case the gateway meets.
func resolutionLayer(endpoints string) core.Module {
	type shape struct {
		Endpoints []string `config:"endpoints"`
	}
	return core.Module{
		Name: "stand-in.resolution",
		Resources: []any{config.Schema{
			Namespace: "tenant.resolve",
			Mounts:    []config.Mount{{Value: &shape{}}},
			Items:     map[string]config.Item{"endpoints": {Origins: config.OriginPrimary, Required: true}},
		}},
	}
}

// TestTheMountEndpointIsNotOneTheResolutionLayerResolvesATenantOn holds the
// last check of Start, through the descriptor: a global account's request
// carries no tenant name, and the resolution layer answers it 404, so the
// authentication routes cannot be on an endpoint that layer resolves. The list is
// another module's, read when its shape can be read.
func TestTheMountEndpointIsNotOneTheResolutionLayerResolvesATenantOn(t *testing.T) {
	for _, c := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"the mount endpoint is one the layer resolves on", `{"authn":{"gate":{"mount":{"endpoint":"public"}}},"tenant":{"resolve":{"endpoints":["public"]}}}`, true},
		{"the mount endpoint is among several it resolves on", `{"authn":{"gate":{"mount":{"endpoint":"admin"}}},"tenant":{"resolve":{"endpoints":["public","admin"]}}}`, true},
		{"the layer resolves on another endpoint", `{"authn":{"gate":{"mount":{"endpoint":"admin"}}},"tenant":{"resolve":{"endpoints":["public"]}}}`, false},
		{"no resolution layer is configured", `{"authn":{"gate":{"mount":{"endpoint":"admin"}}}}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := assembly{
				body:     c.body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public", "admin"),
				modules:  []core.Module{resolutionLayer("")},
			}.drive(t, nothing)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("the drive failed: %v", err)
				}
				return
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the drive returned %v, want one wrapping %v", err, authn.ErrInvalidConfig)
			}
			if !strings.Contains(err.Error(), "tenant.resolve.endpoints") {
				t.Errorf("the failure reads %q and does not name the list it conflicts with", err)
			}
		})
	}
}

// TestAnImpersonatingRequestThroughTheRealEntryPoint holds the scope rule against
// the real entry point and the real chain: an impersonation is let into a route
// registered Impersonable and into every route of an endpoint the scope guard
// covers, and refused on a route of an endpoint it does not.
func TestAnImpersonatingRequestThroughTheRealEntryPoint(t *testing.T) {
	var ended, orders, adminOrders, publicOrders reply
	liveAssembly{
		endpoints: []string{"public", "admin", "tenanted"},
		scope:     fakeScope{"tenanted": true},
		backends:  []authn.Verifier{impersonatingBackend()},
		routes: []liveRoute{
			{endpoint: "public", method: "POST", path: "/api/v1/auth/impersonation/end"},
			{endpoint: "public", method: "GET", path: "/orders"},
			{endpoint: "admin", method: "GET", path: "/orders"},
			{endpoint: "tenanted", method: "GET", path: "/orders"},
		},
		impersonable: []liveRoute{{endpoint: "public", method: "POST", path: "/api/v1/auth/impersonation/end"}},
	}.serve(t, func(l *live) {
		ended = l.send("public", "POST", "/api/v1/auth/impersonation/end", asBearer("t"))
		publicOrders = l.send("public", "GET", "/orders", asBearer("t"))
		adminOrders = l.send("admin", "GET", "/orders", asBearer("t"))
		orders = l.send("tenanted", "GET", "/orders", asBearer("t"))
	})

	requireStatus(t, "an impersonation on a route registered Impersonable", ended, nethttp.StatusOK)
	requireStatus(t, "an impersonation on a route that is not, on an endpoint the guard does not cover", publicOrders, nethttp.StatusUnauthorized)
	requireStatus(t, "an impersonation on another endpoint the guard does not cover", adminOrders, nethttp.StatusUnauthorized)
	requireStatus(t, "an impersonation on an endpoint the guard covers", orders, nethttp.StatusOK)
	// The Key of an impersonation is the actor's, which is what rate limiting
	// and the audit trail count under.
	if want := "reached as admin-9 [required]"; orders.body != want {
		t.Errorf("the handler on the covered endpoint saw %q, want %q", orders.body, want)
	}
}

// TestTheScopeGuardTheGatewayReadsIsTheOneTheAssemblyDelivers holds the optional
// taking-up in Init: a guard that is delivered is the one consulted, and none
// delivered means no endpoint is covered.
func TestTheScopeGuardTheGatewayReadsIsTheOneTheAssemblyDelivers(t *testing.T) {
	guard := fakeScope{"tenanted": true}
	for name, supplied := range map[string][]supply{
		"with a guard":    {{module: "stand-in.admission", token: (*authn.ScopeGuard)(nil), instance: guard}},
		"without a guard": nil,
	} {
		t.Run(name, func(t *testing.T) {
			var g *gateway
			err := assembly{
				body:     `{}`,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public", "tenanted"),
				supplied: supplied,
			}.drive(t, func(_ context.Context, reg *core.Registry) error {
				gate, err := core.Resolve[authn.Gate](reg)
				if err != nil {
					return err
				}
				g, _ = gate.(*gateway)
				return nil
			})
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
			if g == nil {
				t.Fatal("the delivered gate is not a gateway")
			}
			if supplied == nil && g.scope != nil {
				t.Errorf("the gateway holds the guard %v with none delivered", g.scope)
			}
			if supplied != nil && (g.scope == nil || !g.scope.Covers("tenanted") || g.scope.Covers("public")) {
				t.Errorf("the gateway holds %v, want the guard that was delivered", g.scope)
			}
		})
	}
}
