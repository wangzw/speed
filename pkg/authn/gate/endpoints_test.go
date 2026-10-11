package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
)

// layered reports, for each endpoint of the router, how many authentication
// layers the drive registered on it.
func layered(r *fakeRouter) map[string]int {
	out := make(map[string]int, len(r.endpoints))
	for name, e := range r.endpoints {
		out[name] = len(e.middleware)
	}
	return out
}

// TestWithNoEndpointsKeyTheGatewayGuardsEveryEndpointTheEntryPointDeclares holds
// the default the design settled on: guard everything, and narrow by writing
// names. The side a mistake falls on is refusal, so an endpoint nobody thought of
// is guarded and not served open, and the way to give one a way past is to
// narrow the list or to register an exemption.
func TestWithNoEndpointsKeyTheGatewayGuardsEveryEndpointTheEntryPointDeclares(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want map[string]int
	}{
		{"the key is absent", `{}`, map[string]int{"admin": 1, "ops": 1, "public": 1}},
		{"the key is absent, the section is not", `{"authn":{"gate":{"call-budget":"2s"}}}`, map[string]int{"admin": 1, "ops": 1, "public": 1}},
		{"the wildcard is written", `{"authn":{"gate":{"endpoints":["*"]}}}`, map[string]int{"admin": 1, "ops": 1, "public": 1}},
		{"one endpoint is named", `{"authn":{"gate":{"endpoints":["public"]}}}`, map[string]int{"admin": 0, "ops": 0, "public": 1}},
		{"two endpoints are named", `{"authn":{"gate":{"endpoints":["ops","public"]}}}`, map[string]int{"admin": 0, "ops": 1, "public": 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			router := routerWith("public", "admin", "ops")
			err := assembly{
				body:     c.body,
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   router,
			}.drive(t, nothing)
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
			got := layered(router)
			for name, n := range c.want {
				if got[name] != n {
					t.Errorf("endpoint %q carries %d layer(s), want %d: %v", name, got[name], n, got)
				}
			}
		})
	}
}

// TestTheWildcardIsExpandedAtInitAgainstTheEndpointsDeclaredThen holds where the
// expansion happens: in Init, against the set the entry point has by then, so
// that the default does not depend on which names were written anywhere. An
// exemption on an endpoint of the full set lands, and one on a name that is not
// in it does not.
func TestTheWildcardIsExpandedAtInitAgainstTheEndpointsDeclaredThen(t *testing.T) {
	var guarded []string
	router := routerWith("a", "b", "c")
	err := assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   router,
		modules: []core.Module{{
			Name: "stand-in.registrant",
			New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
			Init: func(_ context.Context, reg *core.Registry, _ any) error {
				gate, resolveErr := core.Resolve[authn.Gate](reg)
				if resolveErr != nil {
					return resolveErr
				}
				gate.Exempt("c", "GET", "/health")
				return nil
			},
		}},
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		gate, resolveErr := core.Resolve[authn.Gate](reg)
		if resolveErr != nil {
			return resolveErr
		}
		if g, ok := gate.(*gateway); ok {
			guarded = g.guarded
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got := strings.Join(guarded, ","); got != "a,b,c" {
		t.Errorf("the gateway guards %q, want every declared endpoint in order", got)
	}

	err = assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("a", "b"),
		modules: []core.Module{{
			Name: "stand-in.registrant",
			New:  func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
			Init: func(_ context.Context, reg *core.Registry, _ any) error {
				gate, resolveErr := core.Resolve[authn.Gate](reg)
				if resolveErr != nil {
					return resolveErr
				}
				gate.Exempt("typo", "GET", "/health")
				return nil
			},
		}},
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrUnknownEndpoint) {
		t.Errorf("an exemption on an endpoint nobody declared gave %v, want one wrapping %v", err, authn.ErrUnknownEndpoint)
	}
}

// TestAnEntryPointThatDeclaresNoEndpointCannotBeGuarded holds the one failure of
// the default: the wildcard of an entry point with nothing declared guards
// nothing, and a gateway that guards nothing is not a gateway that started.
func TestAnEntryPointThatDeclaresNoEndpointCannotBeGuarded(t *testing.T) {
	err := assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith(),
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("the drive returned %v, want one wrapping %v", err, authn.ErrInvalidConfig)
	}
}

// TestAnExplicitEmptyListFailsThePrepareOfTheDescriptor holds the empty list
// through the descriptor and not only through resolve: the drive fails with the
// sentinel and the key, so a host that wrote [] is told, and nothing is
// registered.
func TestAnExplicitEmptyListFailsThePrepareOfTheDescriptor(t *testing.T) {
	router := routerWith("public")
	err := assembly{
		body:     `{"authn":{"gate":{"endpoints":[]}}}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   router,
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive returned %v, want one wrapping %v", err, authn.ErrInvalidConfig)
	}
	if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Errorf("the failure reads %q and does not name the key", err)
	}
	if got := layered(router)["public"]; got != 0 {
		t.Errorf("the drive registered %d layer(s) before refusing", got)
	}
}

// TestTheGuardedEndpointsAreWrittenToTheStartupDiagnostics holds the diagnostic
// that makes a missing guard a missing exemption: the expanded list of endpoints
// that are guarded, and what each backend takes.
func TestTheGuardedEndpointsAreWrittenToTheStartupDiagnostics(t *testing.T) {
	logs := liveAssembly{
		endpoints: []string{"public", "admin"},
		backends: []authn.Verifier{
			passes(basicBackend("static", "static.")),
			cookieBackend("session", "sid"),
		},
		routes: []liveRoute{{endpoint: "public", method: "GET", path: "/orders"}},
	}.serve(t, func(*live) {})

	guarded, ok := logs.recordNamed("authn: the endpoints this gateway guards")
	if !ok {
		t.Fatal("the startup diagnostics do not name the guarded endpoints")
	}
	if got := attrText(guarded, "endpoints"); got != "admin, public" {
		t.Errorf("the guarded endpoints read %q, want the expanded list", got)
	}
	if got := attrText(guarded, "configured"); got != "*" {
		t.Errorf("the configured list reads %q, want the wildcard it was written as", got)
	}

	var sawBasic, sawCookie bool
	for _, r := range logs.recordsOf() {
		if r.Message != "authn: a credential-verifying backend taking part" {
			continue
		}
		switch attrText(r, "backend") {
		case "static":
			sawBasic = attrText(r, "schemes") == "basic" && attrText(r, "accounts") == "static."
		case "session":
			sawCookie = attrText(r, "cookies") == "sid" && attrText(r, "accounts") == "the library namespace"
		}
	}
	if !sawBasic || !sawCookie {
		t.Errorf("the diagnostics do not state what each backend takes (basic %v, cookie %v)", sawBasic, sawCookie)
	}
}
