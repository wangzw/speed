package gate

import (
	"errors"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestRegisteringOutsideTheWindowPanics holds both ends of the three-state
// gate.
//
// Before the window a registrant is still constructing, and the instance it
// would register with may not exist. After it the table is frozen and the
// matchers are built from it, so a late registration would change what is
// guarded while requests are already being served. Neither is something a
// caller can retry, which is why it is a panic rather than a value handed back.
func TestRegisteringOutsideTheWindowPanics(t *testing.T) {
	t.Run("before the window opens", func(t *testing.T) {
		g := newGateway(settings{endpoints: []string{"public"}, inFlight: 1})
		assertPanics(t, "registering before Migrate", func() {
			g.Exempt("public", "GET /health")
		})
	})

	t.Run("after the window shuts", func(t *testing.T) {
		g := testGateway(t, []string{"public"}, enumerating("local", "alice"))
		sealed(t, g, routerWith("public"))
		assertPanics(t, "registering after Start", func() {
			g.Exempt("public", "GET /health")
		})
	})
}

// TestAnExemptionThatCannotLandFailsTheStartup holds the three validations
// Start makes, each with its own sentinel.
//
// The unguarded case is the one worth spelling out: the direction is already
// fail-closed, since an exemption on an endpoint nobody guards changes nothing.
// Swallowing it silently is what the sentinel prevents — the registrant
// believes a route is public, and nothing anywhere says it is not.
func TestAnExemptionThatCannotLandFailsTheStartup(t *testing.T) {
	for _, c := range []struct {
		name      string
		guarded   []string
		endpoints []string
		exempt    exemption
		want      error
	}{
		{
			name:      "the endpoint does not exist",
			guarded:   []string{"public"},
			endpoints: []string{"public"},
			exempt:    exemption{endpoint: "typo", pattern: "GET /health"},
			want:      authn.ErrUnknownEndpoint,
		},
		{
			name:      "the endpoint exists but this gateway does not guard it",
			guarded:   []string{"public"},
			endpoints: []string{"public", "admin"},
			exempt:    exemption{endpoint: "admin", pattern: "GET /health"},
			want:      authn.ErrExemptionUnguarded,
		},
		{
			name:      "the matcher will not take the pattern",
			guarded:   []string{"public"},
			endpoints: []string{"public"},
			exempt:    exemption{endpoint: "public", pattern: "/{"},
			want:      authn.ErrUnmatchablePattern,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := testGateway(t, c.guarded, enumerating("local", "alice"))
			g.Exempt(c.exempt.endpoint, c.exempt.pattern)

			err := g.seal(routerWith(c.endpoints...))
			if !errors.Is(err, c.want) {
				t.Fatalf("sealing gave %v, want one wrapping %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.exempt.endpoint) {
				t.Errorf("the failure reads %q and does not name the endpoint %q",
					err, c.exempt.endpoint)
			}
			if !strings.Contains(err.Error(), c.exempt.pattern) {
				t.Errorf("the failure reads %q and does not name the pattern %q",
					err, c.exempt.pattern)
			}
		})
	}
}

// TestOnePatternRegisteredTwiceIsOneExemption holds that two modules asking for
// the same route to be public are asking for the same thing.
//
// Handing the duplicate straight to the matcher would panic, and a panic in
// Start skips the registry's rollback — the endpoints bound before it would be
// left listening with nothing to close them.
func TestOnePatternRegisteredTwiceIsOneExemption(t *testing.T) {
	g := testGateway(t, []string{"public"}, enumerating("local", "alice"))
	g.Exempt("public", "GET /health")
	g.Exempt("public", "GET /health")

	sealed(t, g, routerWith("public"))

	if got := g.matchers["public"].patterns; len(got) != 1 {
		t.Fatalf("the frozen table holds %v, want the one pattern it was given twice", got)
	}
	if !g.matchers["public"].exempts(request("GET", "/health")) {
		t.Error("the pattern registered twice exempts nothing, so the deduplication dropped it")
	}
}

// TestWhatTheMatcherLetsThroughIsWhatWasRegistered holds the matching itself,
// including the part that is easy to lose: the method is part of the pattern,
// so exempting one verb leaves the others guarded.
func TestWhatTheMatcherLetsThroughIsWhatWasRegistered(t *testing.T) {
	g := testGateway(t, []string{"public"}, enumerating("local", "alice"))
	g.Exempt("public", "GET /health")
	g.Exempt("public", "/assets/")
	g.Exempt("public", "GET /orders/{id}")
	sealed(t, g, routerWith("public"))

	m := g.matchers["public"]
	for _, c := range []struct {
		method, target string
		want           bool
		why            string
	}{
		{"GET", "/health", true, "the pattern it was registered under"},
		{"POST", "/health", false, "only GET was exempted, so the other verbs stay guarded"},
		{"GET", "/assets/logo.png", true, "a subtree mount covers what is under it"},
		{"GET", "/orders/17", true, "a pattern carrying a path parameter can be exempted"},
		{"GET", "/orders", false, "the parameter is not optional"},
		{"GET", "/secret", false, "a route nobody exempted"},
	} {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			if got := m.exempts(request(c.method, c.target)); got != c.want {
				t.Errorf("the matcher exempts=%v, want %v: %s", got, c.want, c.why)
			}
		})
	}
}

// TestAnEndpointWithNoExemptionExemptsNothing holds the nil matcher, which is
// what an endpoint carrying no registration is left with. Reading it as
// "nothing was registered, so let everything through" would open every guarded
// endpoint that nobody wrote an exemption for.
func TestAnEndpointWithNoExemptionExemptsNothing(t *testing.T) {
	g := testGateway(t, []string{"public"}, enumerating("local", "alice"))
	sealed(t, g, routerWith("public"))

	if g.matchers["public"].exempts(request("GET", "/anything")) {
		t.Error("an endpoint with no registered exemption exempts a request, so every route on " +
			"it would be served without a credential")
	}
}

// TestAnExemptionOnlyExemptsTheEndpointItWasRegisteredOn holds the keying of
// the matchers.
//
// An exemption is registered against one endpoint by name. A gateway that
// consulted the wrong endpoint's matcher — or one matcher for all of them —
// would open a public route's path on every endpoint it guards. That is an
// authentication bypass produced by a one-word mistake and visible nowhere
// else: both endpoints go on answering, one of them without a credential.
func TestAnExemptionOnlyExemptsTheEndpointItWasRegisteredOn(t *testing.T) {
	g := testGateway(t, []string{"public", "admin"}, passes(enumerating("local", "alice")))
	g.Exempt("public", "GET /health")
	sealed(t, g, routerWith("public", "admin"))

	// Driven through the request path, not through the matcher map: picking
	// the wrong endpoint's matcher and building one matcher for all of them
	// are two different mistakes with one consequence, and only the request
	// path sees both.
	opened, _ := run(g, "public", request("GET", "/health"))
	if opened.Code != nethttp.StatusOK {
		t.Fatalf("the endpoint the exemption was registered on answered %d, want %d",
			opened.Code, nethttp.StatusOK)
	}

	guarded, reached := run(g, "admin", request("GET", "/health"))
	if guarded.Code != nethttp.StatusUnauthorized {
		t.Errorf("an endpoint nobody registered the exemption on answered %d, want %d: a path "+
			"made public on one endpoint would be public on every endpoint this gateway "+
			"guards", guarded.Code, nethttp.StatusUnauthorized)
	}
	if reached.reached {
		t.Error("the layer behind the gateway was reached without a credential on an endpoint " +
			"carrying no exemption")
	}
}

// assertPanics runs fn and fails unless it panicked.
func assertPanics(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s returned without panicking", what)
		}
	}()
	fn()
}
