package gate

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// assembly is one drive of a real registry through this module's descriptor:
// the gateway, each backend registered as a module of its own, the
// configuration reader supplied to the config module the way a host supplies
// it, the router supplied to a stand-in for the entry point, and whatever else a
// case takes part.
//
// The cases elsewhere in this package build a gateway by hand, which is what
// lets them reach one state at a time. What they cannot reach is the descriptor
// itself — which callback runs which step, and what each one takes up from the
// registry — and that is what a case built on this holds.
type assembly struct {
	// body is the whole configuration, JSON.
	body string
	// backends are registered as authn.<name> and all take part.
	backends []authn.Verifier
	// router is the instance of a stand-in that declares the router
	// capability; nil leaves the command with no HTTP entry point at all.
	router speedhttp.Router
	// modules take part beside the gateway and read their own section of
	// body.
	modules []core.Module
	// supplied are further instances handed to the registry before the
	// drive, each to a stand-in module of its own.
	supplied []supply
	// gate is supplied as the gateway module's instance, in place of the one
	// its New builds; nil leaves the module to build its own.
	gate authn.Gate
}

// supply is one instance a case hands the registry the way a host would, for a
// module that is not among the ones these cases register: the one that delivers
// the capability in a real assembly is a stand-in here.
type supply struct {
	// module is the name the stand-in is registered under, and the name the
	// instance is supplied to.
	module string
	// token is the capability the stand-in declares, which the instance
	// delivers.
	token    core.Token
	instance any
}

const routerModule = "stand-in.router"

// standIn is a module that declares the capability token names and nothing
// else. An instance a case supplies has to belong to a registered module, and
// the modules that deliver such a capability in a real assembly are not ones
// these cases bring in.
func standIn(name string, token core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	gateModule := Module()
	reader := authntest.ConfigReaderAll(t, a.body, append([]core.Module{gateModule}, a.modules...)...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(gateModule)
	names := []string{ModuleName}
	for _, v := range a.backends {
		m := backendModule(v)
		reg.Register(m)
		names = append(names, m.Name)
	}
	for _, m := range a.modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)
	if a.router != nil {
		reg.Register(standIn(routerModule, (*speedhttp.Router)(nil)))
		reg.Supply(a.router, routerModule)
	}
	for _, s := range a.supplied {
		reg.Register(standIn(s.module, s.token))
		reg.Supply(s.instance, s.module)
	}
	if a.gate != nil {
		reg.Supply(a.gate, ModuleName)
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

func backendModule(v authn.Verifier) core.Module {
	return core.Module{
		Name:     "authn." + v.Name(),
		Provides: []core.Provision{{Token: (*authn.Verifier)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return v, nil },
	}
}

func guarding(endpoints ...string) string {
	return `{"authn":{"gate":{"endpoints":["` + strings.Join(endpoints, `","`) + `"]}}}`
}

// nothing is an action that only lets the drive arrive.
func nothing(context.Context, *core.Registry) error { return nil }

// TestTheDescriptorRegistersTheLayerOnEveryGuardedEndpointAndNowhereElse holds
// what Init does with the endpoints list: one layer on each endpoint named, and
// none on an endpoint left out of it — which is the one place a missing entry in
// that list could ever show up.
func TestTheDescriptorRegistersTheLayerOnEveryGuardedEndpointAndNowhereElse(t *testing.T) {
	router := routerWith("public", "admin", "ops")
	err := assembly{
		body:     guarding("public", "admin"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   router,
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	for _, name := range []string{"public", "admin"} {
		layers := router.endpoints[name].middleware
		if len(layers) != 1 {
			t.Fatalf("endpoint %q carries %d layer(s), want exactly one authentication layer",
				name, len(layers))
		}
		layer := layers[0]
		if layer.Name != ModuleName || layer.Stage != speedhttp.StageIdentify {
			t.Errorf("endpoint %q carries the layer %q in stage %v, want %q in the identifying stage",
				name, layer.Name, layer.Stage, ModuleName)
		}
		if len(layer.Provides) != 1 || layer.Provides[0] != core.Token((*authn.Gate)(nil)) {
			t.Errorf("endpoint %q carries a layer providing %v, want the gateway's own token "+
				"alone: it is the only anchor another layer can place itself against", name,
				layer.Provides)
		}
	}
	if got := len(router.endpoints["ops"].middleware); got != 0 {
		t.Errorf("endpoint \"ops\" is not in the endpoints list and carries %d layer(s)", got)
	}
}

// TestInitRefusesWhatTheCommandIsMissing holds the two sentinels Init reports
// for a command that left something out, and the one it reports for a name
// that does not exist.
func TestInitRefusesWhatTheCommandIsMissing(t *testing.T) {
	for _, c := range []struct {
		name string
		a    assembly
		want error
	}{
		{
			name: "no HTTP entry point",
			a: assembly{
				body:     guarding("public"),
				backends: []authn.Verifier{basicBackend("static", "static.")},
			},
			want: authn.ErrNoRouter,
		},
		{
			name: "no backend",
			a: assembly{
				body:   guarding("public"),
				router: routerWith("public"),
			},
			want: authn.ErrNoVerifier,
		},
		{
			name: "an endpoint the entry point does not have",
			a: assembly{
				body:     guarding("pubilc"),
				backends: []authn.Verifier{basicBackend("static", "static.")},
				router:   routerWith("public"),
			},
			want: authn.ErrUnknownEndpoint,
		},
		{
			name: "two backends taking Basic",
			a: assembly{
				body:     guarding("public"),
				backends: []authn.Verifier{basicBackend("static", "static."), basicBackend("users", "users.")},
				router:   routerWith("public"),
			},
			want: authn.ErrClaimConflict,
		},
		{
			name: "a backend declaring the static prefix under another name",
			a: assembly{
				body: guarding("public"),
				backends: []authn.Verifier{
					basicBackend("static", "static."),
					&fakeVerifier{name: "impostor", accepts: authn.Accepts{Schemes: []string{"bearer"}, Accounts: "static."}},
				},
				router: routerWith("public"),
			},
			want: authn.ErrInvalidClaim,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.drive(t, nothing)
			if !errors.Is(err, c.want) {
				t.Errorf("the drive returned %v, want it to wrap %v", err, c.want)
			}
		})
	}
}

type brokenRouter struct {
	*fakeRouter
	broken string
	err    error
}

func (r brokenRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	if name == r.broken {
		return nil, r.err
	}
	return r.fakeRouter.Endpoint(name)
}

// TestARouterFailureOtherThanAnUnknownNameIsCarriedOutAsItStands holds the
// other branch of the endpoint lookup.
func TestARouterFailureOtherThanAnUnknownNameIsCarriedOutAsItStands(t *testing.T) {
	failure := errors.New("the entry point cannot hand out endpoints right now")
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   brokenRouter{fakeRouter: routerWith("public"), broken: "public", err: failure},
	}.drive(t, nothing)

	if !errors.Is(err, failure) {
		t.Errorf("the drive returned %v, want it to carry the router's own failure", err)
	}
	if errors.Is(err, authn.ErrUnknownEndpoint) {
		t.Errorf("the drive returned %v, which reads as a misspelt endpoint name: the router "+
			"reported something else", err)
	}
}

// TestAfterStopNoRequestReachesABackend holds the Stop callback through the
// descriptor: the layer the drive registered goes on existing after shutdown,
// because a draining entry point can still hand it a request, and every such
// request ends as an outage charged to the gateway, never as a pass.
func TestAfterStopNoRequestReachesABackend(t *testing.T) {
	router := routerWith("public")
	var asked countingVerifier
	asked.fakeVerifier = passes(basicBackend("static", "static."))

	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{&asked},
		router:   router,
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	engine, seen := engineWith(router.endpoints["public"].middleware[0].Handler)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, withBasic(request("GET", "/orders"), "alice", "secret"))

	if seen.reached {
		t.Error("the handler was reached after the gateway had stopped")
	}
	if w.Code != nethttp.StatusServiceUnavailable {
		t.Errorf("a request after Stop answered %d, want %d", w.Code, nethttp.StatusServiceUnavailable)
	}
	if asked.calls != 0 {
		t.Errorf("a backend was asked %d time(s) after Stop, want none", asked.calls)
	}
}

// countingVerifier counts Verify calls without the atomic the concurrent cases
// need; the drive here is sequential.
type countingVerifier struct {
	*fakeVerifier
	calls int
}

func (c *countingVerifier) Verify(ctx context.Context, cred authn.Credential) authn.Verdict {
	c.calls++
	return c.fakeVerifier.Verify(ctx, cred)
}

type foreignGate struct{}

func (foreignGate) Exempt(string, string, string)       {}
func (foreignGate) Optional(string, string, string)     {}
func (foreignGate) Impersonable(string, string, string) {}

// TestTheCallbacksPassByAnInstanceSuppliedForTheGateway holds what the
// descriptor does with an instance this module did not build: nothing.
func TestTheCallbacksPassByAnInstanceSuppliedForTheGateway(t *testing.T) {
	router := routerWith("public")
	var delivered authn.Gate
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   router,
		gate:     foreignGate{},
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		delivered, err = core.Resolve[authn.Gate](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	if _, ok := delivered.(foreignGate); !ok {
		t.Fatalf("the gateway capability is delivered by %T, want the instance the case supplied, "+
			"so nothing here was checked", delivered)
	}
	if got := len(router.endpoints["public"].middleware); got != 0 {
		t.Errorf("endpoint \"public\" carries %d layer(s), want none: the instance supplied for the "+
			"gateway is not a gateway, and the callbacks register nothing on its behalf", got)
	}
}

// TestTheRedactionRulesReachTheLoggingModulesOwnOutput holds the redaction
// registration end to end: Init takes the logging capability up, registers the
// rules there, and a record written afterwards through that module's chain
// reaches its destination with the credential masked — under a credential key,
// and inside the free text of any other value — while a refusal cause that only
// talks about a scheme arrives as it was written.
func TestTheRedactionRulesReachTheLoggingModulesOwnOutput(t *testing.T) {
	const cause = "the bearer token names no live session"
	path := filepath.Join(t.TempDir(), "log.json")
	body := `{"authn":{"gate":{"endpoints":["public"]}},` +
		`"log":{"outputs":[{"to":"file","format":"json","path":"` + path + `"}]}}`

	err := assembly{
		body:     body,
		backends: []authn.Verifier{basicBackend("static", "static."), schemeBackend("tokens", "bearer")},
		router:   routerWith("public"),
		modules:  []core.Module{log.Module()},
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		logging, err := core.Resolve[log.Logger](reg)
		if err != nil {
			return err
		}
		logging.Named("case").Info("a handler that logs what it was sent",
			"Authorization", "Basic YWxpY2U6c2VjcmV0",
			"Cookie", "sid=cookie-secret-1",
			"set-cookie", "sid=cookie-secret-2; Path=/",
			"credential", "credential-secret-3",
			"note", "the caller sent Bearer tok-very-secret and nothing else",
			"cause", cause)
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	text := string(written)
	if !strings.Contains(text, "a handler that logs what it was sent") {
		t.Fatalf("the record is not in the output at all, so nothing here was checked:\n%s", text)
	}
	for _, secret := range []string{"YWxpY2U6c2VjcmV0", "tok-very-secret", "cookie-secret-1", "cookie-secret-2", "credential-secret-3"} {
		if strings.Contains(text, secret) {
			t.Errorf("the output carries the credential %q in the clear:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, cause) {
		t.Errorf("the output does not carry the refusal cause %q as it was written, so the rule "+
			"took a word of prose for a credential:\n%s", cause, text)
	}
}

// TestTheKeyAndShapeRulesGovernAnAttributeBoundBeforeInit holds the effect of
// registering those rules before Init: a module constructed after the
// gateway, and before Init, binds attributes carrying a credential under a
// key name and as a bare JWT, and the destination gets neither.
func TestTheKeyAndShapeRulesGovernAnAttributeBoundBeforeInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.json")
	body := `{"authn":{"gate":{"endpoints":["public"]}},` +
		`"log":{"outputs":[{"to":"file","format":"json","path":"` + path + `"}]}}`
	const early = "early record written in New"
	earlyWriter := core.Module{
		Name: "stand-in.early",
		// Requiring the gateway's capability and logging puts this module's New
		// after the gateway's and after logging's, which is the moment between
		// the two registrations that the case is about.
		Requires: []core.Requirement{
			{Token: (*authn.Gate)(nil)},
			{Token: (*log.Logger)(nil)},
		},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logging, err := core.Resolve[log.Logger](reg)
			if err != nil {
				return nil, err
			}
			// The attributes are bound with With, which judges them once, when
			// they are bound: a rule registered after this line never sees them.
			// Written on the record itself they would be judged when the record
			// is written, and a destination that is not open yet would hide the
			// difference.
			logging.Named("early").With(
				"token", "token-secret-1",
				"Password", "password-secret-2",
				"note", "carrying "+jwtToken+" in the clear",
				"refresh", refreshToken).Info(early)
			return struct{}{}, nil
		},
	}

	err := assembly{
		body:     body,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		modules:  []core.Module{log.Module(), earlyWriter},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log output: %v", err)
	}
	text := string(written)
	if !strings.Contains(text, early) {
		t.Fatalf("the early record is not in the output, so nothing here was checked:\n%s", text)
	}
	for _, secret := range []string{"token-secret-1", "password-secret-2", jwtToken, refreshToken} {
		if strings.Contains(text, secret) {
			t.Errorf("an attribute bound before Init carries %q in the clear:\n%s", secret, text)
		}
	}
}

// TestWithNoLoggingModuleNoRuleIsRegisteredAndNothingFails holds the absence of
// the optional dependency: the gateway builds and runs, and registering the
// rules is a no-op rather than an error.
func TestWithNoLoggingModuleNoRuleIsRegisteredAndNothingFails(t *testing.T) {
	if err := registerNewRules(core.New()); err != nil {
		t.Errorf("registering the rules with no logging in the registry failed: %v", err)
	}
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
	}.drive(t, nothing)
	if err != nil {
		t.Errorf("an assembly with no logging module failed: %v", err)
	}
}

// rulesRecorder is a logging capability that records the order in which the
// redaction rules were registered with it.
type rulesRecorder struct {
	mu     *sync.Mutex
	events *[]string
	keys   *[]string
}

func (r rulesRecorder) Named(string) *slog.Logger { return quiet() }
func (r rulesRecorder) Redaction() log.Redaction  { return r }
func (r rulesRecorder) AddKeys(keys ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.events = append(*r.events, "keys")
	*r.keys = append(*r.keys, keys...)
}

func (r rulesRecorder) AddPattern(name string, _ log.Matcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.events = append(*r.events, "pattern:"+name)
}

// TestTheRulesAreRegisteredAsKeysThenShapesThenTheSchemeRule holds what
// reaches the logging capability and in which order: the credential key
// names, then the value shapes, then the rule over the value that follows a
// scheme, which needs the backends' declarations.
func TestTheRulesAreRegisteredAsKeysThenShapesThenTheSchemeRule(t *testing.T) {
	var mu sync.Mutex
	var events, keys []string
	logging := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return rulesRecorder{mu: &mu, events: &events, keys: &keys}, nil
		},
	}
	// A module that records the moment the gateway's New has run: it requires
	// nothing of the gateway, so what is held is the gateway's own declared
	// dependency on logging and not an ordering this case arranged.
	err := assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		modules:  []core.Module{logging},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"keys", "pattern:" + ModuleName + ".shapes", "pattern:" + ModuleName + ".credential"}
	if !slices.Equal(events, want) {
		t.Fatalf("the rules were registered in the order %v, want %v: the key names and the "+
			"value shapes in New, the scheme rule once the backends are known", events, want)
	}
	for _, want := range []string{
		"Authorization", "authorization", "Cookie", "cookie", "Set-Cookie", "set-cookie",
		"Credential", "credential", "password", "Password", "SAMLResponse", "samlresponse", "state", "flow_token",
	} {
		if !slices.Contains(keys, want) {
			t.Errorf("the key %q is not registered, and key matching is exact", want)
		}
	}
}

// TestTheKeyAndShapeRulesAreInPlaceWhenTheNextModuleIsBuilt holds the stage
// the key names and the value shapes are registered at.
func TestTheKeyAndShapeRulesAreInPlaceWhenTheNextModuleIsBuilt(t *testing.T) {
	var mu sync.Mutex
	var events, keys, atNextNew []string
	logging := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return rulesRecorder{mu: &mu, events: &events, keys: &keys}, nil
		},
	}
	next := core.Module{
		Name: "stand-in.next",
		Requires: []core.Requirement{
			{Token: (*authn.Gate)(nil)},
			{Token: (*log.Logger)(nil)},
		},
		New: func(context.Context, *core.Registry) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			atNextNew = slices.Clone(events)
			return struct{}{}, nil
		},
	}
	err := assembly{
		body:     `{}`,
		backends: []authn.Verifier{basicBackend("static", "static.")},
		router:   routerWith("public"),
		modules:  []core.Module{logging, next},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := []string{"keys", "pattern:" + ModuleName + ".shapes"}
	if !slices.Equal(atNextNew, want) {
		t.Errorf("by the time the module after the gateway was built the rules registered were %v, "+
			"want %v: the key names and the value shapes belong to New, before any logger binds an attribute",
			atNextNew, want)
	}
}

// TestTheGatewayRequiresLoggingAsOptionalAndNothingElse holds the descriptor's
// one dependency: logging, and optional, so that the key rules are in place
// before any logger binds an attribute without the gateway forcing a logging
// module into a command that has none.
func TestTheGatewayRequiresLoggingAsOptionalAndNothingElse(t *testing.T) {
	m := Module()
	if len(m.Requires) != 1 || m.Requires[0].Token != core.Token((*log.Logger)(nil)) || !m.Requires[0].Optional {
		t.Errorf("the gateway requires %+v, want an optional dependency on logging alone", m.Requires)
	}
	if len(m.Provides) != 1 || !m.Provides[0].Exclusive || m.Provides[0].Token != core.Token((*authn.Gate)(nil)) {
		t.Errorf("the gateway provides %+v, want authn.Gate exclusively", m.Provides)
	}
}
