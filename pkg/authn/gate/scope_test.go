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

func impersonatingBackend() *fakeVerifier {
	b := &fakeVerifier{name: "imp", accepts: authn.Accepts{Schemes: []string{"bearer"}, Impersonation: true}}
	b.verify = func(context.Context, authn.Credential) authn.Verdict {
		p := principalOf(b, "customer-1")
		p.Actor, p.Session, p.Scope = "admin-9", "0198f0aa-0000-7000-8000-000000000002", "tenant-a"
		return authn.Authenticated(p)
	}
	return b
}

// TestAnImpersonatingIdentityIsAdmittedOnlyWhereTheRuleSaysSo holds the impersonation scope rule from every side.
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

// TestAnIdentityThatIsNotImpersonatingIsNotHeldToTheScopeRule holds the rule applying only to an actor that differs from the account.
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

// TestAnImpersonationOutOfScopeIsRefusedAfterTheBackendAnswered holds the scope rule applied to the backend's answer.
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

// TestAnImpersonationReachesAnOptionalRouteOnlyWhenItIsAlsoImpersonable holds Optional and Impersonable not standing in for one another.
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

// TestAnExemptRouteDoesNotApplyTheScopeRuleBecauseItReadsNoCredential holds an exempt route reached with no identity.
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

// TestTheMountEndpointIsHeldToBeingGuardedAndOutsideTheAdmissionLayer holds the Start checks of the mount endpoint, each with its own sentinel.
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

		{"no endpoint configured with several declared", []string{"admin", "public"}, []string{"admin", "public"}, "", false, fakeScope{"admin": true, "public": true}, nil, ""},
		{"no endpoint configured, no module mounts routes, the only endpoint covered", []string{"public"}, []string{"public"}, "", false, fakeScope{"public": true}, nil, ""},
		{"no endpoint configured, no module mounts routes, the only endpoint left unguarded", nil, []string{"public"}, "", false, nil, nil, ""},

		{"no endpoint configured, routes mounted, the only endpoint covered", []string{"public"}, []string{"public"}, "", true, fakeScope{"public": true}, authn.ErrInvalidConfig, "public"},
		{"no endpoint configured, routes mounted, the only endpoint not covered", []string{"public"}, []string{"public"}, "", true, fakeScope{}, nil, ""},
		{"no endpoint configured, routes mounted, no admission layer", []string{"public"}, []string{"public"}, "", true, nil, nil, ""},
		{"no endpoint configured, routes mounted, the only endpoint left unguarded", nil, []string{"public"}, "", true, nil, authn.ErrRoutesUnguarded, "public"},
		{"no endpoint configured, routes mounted, several declared", []string{"admin", "public"}, []string{"admin", "public"}, "", true, fakeScope{"admin": true}, authn.ErrRoutesUnconfigured, "admin, public"},
		{"no endpoint configured, routes mounted, none declared", []string{"public"}, nil, "", true, nil, authn.ErrRoutesUnconfigured, defaultMountItem},
	} {
		t.Run(c.name, func(t *testing.T) {
			set := settings{endpoints: c.guarded, budget: 1, inFlight: 1, mount: mountConfig{Endpoints: endpoints{Default: c.mount}}}
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

// resolutionLayer is a stand-in for the tenant resolution layer's configuration
// section: its endpoint list is required, as in the real layer's schema.
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

// TestTheMountEndpointIsNotOneTheResolutionLayerResolvesATenantOn holds the mount endpoint outside what the resolution layer resolves a tenant on.
func TestTheMountEndpointIsNotOneTheResolutionLayerResolvesATenantOn(t *testing.T) {
	for _, c := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"the mount endpoint is one the layer resolves on", `{"authn":{"gate":{"mount":{"endpoints":{"default":"public"}}}},"tenant":{"resolve":{"endpoints":["public"]}}}`, true},
		{"the mount endpoint is among several it resolves on", `{"authn":{"gate":{"mount":{"endpoints":{"default":"admin"}}}},"tenant":{"resolve":{"endpoints":["public","admin"]}}}`, true},
		{"the layer resolves on another endpoint", `{"authn":{"gate":{"mount":{"endpoints":{"default":"admin"}}}},"tenant":{"resolve":{"endpoints":["public"]}}}`, false},
		{"no resolution layer is configured", `{"authn":{"gate":{"mount":{"endpoints":{"default":"admin"}}}}}`, false},
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

// TestAnImpersonatingRequestThroughTheRealEntryPoint holds the scope rule against the real entry point.
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
	if want := "reached as admin-9 [required]"; orders.body != want {
		t.Errorf("the handler on the covered endpoint saw %q, want %q", orders.body, want)
	}
}

// TestTheScopeGuardTheGatewayReadsIsTheOneTheAssemblyDelivers holds the guard consulted being the delivered one.
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
