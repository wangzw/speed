package gate

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/configreader"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// assembly is one drive of a real registry through this module's descriptor:
// the gateway, each backend registered as a module of its own, the router and
// the configuration handed in the way a host hands them in, and whatever else a
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
	// router is supplied under the router capability; nil leaves the
	// command with no HTTP entry point at all.
	router speedhttp.Router
	// modules take part beside the gateway and read their own section of
	// body.
	modules []core.Module
	// supplied are further products handed to the registry before the
	// drive, keyed by the capability token they answer for.
	supplied []supply
}

// supply is one product a case hands the registry the way a host would.
type supply struct {
	token   core.Token
	product any
}

// drive advances the assembly to Start, runs action there, and returns what the
// drive returned once every Stop and Close has run.
func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	gateModule := Module()
	reader := configreader.OverAll(t, a.body, append([]core.Module{gateModule}, a.modules...)...)

	reg := core.New()
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
	reg.Supply((*config.Reader)(nil), reader)
	if a.router != nil {
		reg.Supply((*speedhttp.Router)(nil), a.router)
	}
	for _, s := range a.supplied {
		reg.Supply(s.token, s.product)
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// backendModule registers a backend the way a backend subpackage does: its own
// module, delivering the verification capability non-exclusively.
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

// guarding is the configuration body for a gateway guarding the endpoints
// named, with every other item at its default.
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
		backends: []authn.Verifier{enumerating("local", "alice")},
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
		if layer.Name != ModuleName || layer.Order != middlewareOrder {
			t.Errorf("endpoint %q carries the layer %q at Order %d, want %q at %d",
				name, layer.Name, layer.Order, ModuleName, middlewareOrder)
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
// for a command that left something out, and the one it reports for a name that
// does not exist. Each points at a different edit, which is why they are told
// apart at all.
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
				backends: []authn.Verifier{enumerating("local", "alice")},
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
				backends: []authn.Verifier{enumerating("local", "alice")},
				router:   routerWith("public"),
			},
			want: authn.ErrUnknownEndpoint,
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

// brokenRouter reports a failure for one endpoint that is not the unknown-name
// one, which is the case the router's contract leaves open.
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

// TestARouterFailureOtherThanAnUnknownNameIsCarriedOutAsItStands holds the other
// branch of the endpoint lookup. Reading every failure as a misspelt name would
// send the operator to a configuration key that is already right.
func TestARouterFailureOtherThanAnUnknownNameIsCarriedOutAsItStands(t *testing.T) {
	failure := errors.New("the entry point cannot hand out endpoints right now")
	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{enumerating("local", "alice")},
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
	asked.fakeVerifier = passes(enumerating("local", "alice"))

	err := assembly{
		body:     guarding("public"),
		backends: []authn.Verifier{&asked},
		router:   router,
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	handler := router.endpoints["public"].middleware[0].Wrap(nethttp.HandlerFunc(
		func(nethttp.ResponseWriter, *nethttp.Request) {
			t.Error("the handler was reached after the gateway had stopped")
		}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, withBasic(request("GET", "/orders"), "alice", "secret"))

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

func (c *countingVerifier) Verify(ctx context.Context, subject string, cred authn.Credential) authn.Verdict {
	c.calls++
	return c.fakeVerifier.Verify(ctx, subject, cred)
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
		backends: []authn.Verifier{enumerating("local", "alice"), claimingScheme("tokens", "bearer")},
		router:   routerWith("public"),
		modules:  []core.Module{log.Module()},
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		logging, err := core.Resolve[log.Logger](reg)
		if err != nil {
			return err
		}
		logging.Named("case").Info("a handler that logs what it was sent",
			"Authorization", "Basic YWxpY2U6c2VjcmV0",
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
	for _, secret := range []string{"YWxpY2U6c2VjcmV0", "tok-very-secret"} {
		if strings.Contains(text, secret) {
			t.Errorf("the output carries the credential %q in the clear:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, cause) {
		t.Errorf("the output does not carry the refusal cause %q as it was written, so the rule "+
			"took a word of prose for a credential:\n%s", cause, text)
	}
}
