package mount

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// handler is a handler no case calls; the registrations here are about what is
// written to the two registration surfaces and not about what a request does.
func handler(*gin.Context) {}

// newReader is the configuration reader a case hands the module, with the
// gateway's keys declared the way they are in an assembly that holds it.
func newReader(t *testing.T, body string) config.Reader {
	t.Helper()
	return authntest.ConfigReaderAll(t, body, Module(), gate.Module())
}

// TestTheModuleIsDeliveredAndTheCapabilityResolvesToIt drives the real registry
// through the descriptor: construction asserts the instance against the
// capability the descriptor declares, so a product that did not implement it
// would fail the drive with ErrUndeliveredCapability before this case's action
// ran. The resolved capability is the one the descriptor's New built, reading
// what the configuration says.
func TestTheModuleIsDeliveredAndTheCapabilityResolvesToIt(t *testing.T) {
	var got mount.Mounter
	err := stack{
		body:           `{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}}}`,
		router:         &standInRouter{names: []string{"main"}, ledger: &ledger{}},
		realGateModule: true,
		gate:           standInGate{ledger: &ledger{}},
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		got, err = core.Resolve[mount.Mounter](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got.Base() != "/auth" || got.Endpoint() != "main" || got.PublicURL() != "https://app.example.com" || got.CSRF() == nil {
		t.Errorf("the capability reads base %q, endpoint %q, public URL %q, check %v; want /auth, main, "+
			"https://app.example.com and a check", got.Base(), got.Endpoint(), got.PublicURL(), got.CSRF())
	}
}

// TestTheDescriptorStatesWhatTheDesignStates holds the declarations of the
// descriptor: the gateway is required and nothing else is, the capability is
// delivered exclusively and by nothing else, no input item is declared, and the
// stance is enabled whatever the configuration says.
func TestTheDescriptorStatesWhatTheDesignStates(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.mount" {
		t.Errorf("the module is named %q (constant %q), want authn.mount", m.Name, ModuleName)
	}
	if len(m.Requires) != 2 || m.Requires[0].Token != core.Token((*authn.Gate)(nil)) || m.Requires[0].Optional {
		t.Errorf("Requires is %+v, want the gateway required and the logging capability optional", m.Requires)
	}
	if last := m.Requires[len(m.Requires)-1]; last.Token != core.Token((*log.Logger)(nil)) || !last.Optional {
		t.Errorf("Requires = %+v, want the logging capability as the optional dependency", m.Requires)
	}
	if len(m.Provides) != 1 || m.Provides[0].Token != core.Token((*mount.Mounter)(nil)) || !m.Provides[0].Exclusive {
		t.Errorf("Provides is %+v, want the route-mounting capability delivered exclusively", m.Provides)
	}
	if len(m.Resources) != 0 {
		t.Errorf("Resources is %v, want none: the keys are the gateway's", m.Resources)
	}
	for _, body := range []string{`{}`, `{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}}}`} {
		reader := newReader(t, body)
		reg := core.New()
		reg.Register(config.Module())
		reg.Supply(reader, config.ModuleName)
		got, err := m.Prepare(t.Context(), reg)
		if err != nil || got.State != core.StateEnabled {
			t.Errorf("Prepare over %s gave (%v, %v), want the enabled stance", body, got.State, err)
		}
	}
}

// TestTheProcessRegistryCarriesTheModule holds that importing the package is
// what makes the module available to a command.
func TestTheProcessRegistryCarriesTheModule(t *testing.T) {
	if _, found := core.ProcessRegistry.Lookup(ModuleName); !found {
		t.Errorf("the process registry does not hold %s", ModuleName)
	}
}

// TestAGatewayTheHostSuppliedTakesTheModuleIn holds that a gateway that is not
// the gateway module's product, delivered either by a module of its own or
// supplied over the gateway module, passes the delivery check and receives each
// registration through its three public methods and nothing else, each paired
// with the route written to the entry point at the same endpoint, method and path.
func TestAGatewayTheHostSuppliedTakesTheModuleIn(t *testing.T) {
	register := func(m mount.Mounter) {
		m.Public("POST", "/login", handler)
		m.Optional("GET", "/session", handler)
		m.Guarded("DELETE", "/session", handler)
		m.Guarded("POST", "/impersonation/end", handler, mount.Impersonable())
		m.Optional("GET", "/me", handler, mount.Impersonable())
		m.Callback([]string{"GET", "POST"}, "/sso/:provider/callback", handler)
	}
	wantRoutes := []routeCall{
		{"main", "POST", "/auth/login"},
		{"main", "GET", "/auth/session"},
		{"main", "DELETE", "/auth/session"},
		{"main", "POST", "/auth/impersonation/end"},
		{"main", "GET", "/auth/me"},
		{"main", "GET", "/auth/sso/:provider/callback"},
		{"main", "POST", "/auth/sso/:provider/callback"},
	}
	wantAccess := []accessCall{
		{"Exempt", "main", "POST", "/auth/login"},
		{"Optional", "main", "GET", "/auth/session"},
		{"Impersonable", "main", "POST", "/auth/impersonation/end"},
		{"Optional", "main", "GET", "/auth/me"},
		{"Impersonable", "main", "GET", "/auth/me"},
		{"Exempt", "main", "GET", "/auth/sso/:provider/callback"},
		{"Exempt", "main", "POST", "/auth/sso/:provider/callback"},
	}
	for _, c := range []struct {
		name string
		real bool
	}{
		{"delivered by a module of its own, with no gateway module in the command", false},
		{"supplied over the gateway module", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &ledger{}
			err := stack{
				body:           `{}`,
				router:         &standInRouter{names: []string{"main"}, ledger: l},
				realGateModule: c.real,
				gate:           standInGate{ledger: l},
				register:       register,
			}.drive(t, nothing)
			if err != nil {
				t.Fatalf("the drive failed: %v", err)
			}
			routes, access := l.snapshot()
			if !slices.Equal(routes, wantRoutes) {
				t.Errorf("routes written to the entry point:\n got %v\nwant %v", routes, wantRoutes)
			}
			if !slices.Equal(access, wantAccess) {
				t.Errorf("registrations written to the gateway:\n got %v\nwant %v", access, wantAccess)
			}
			if problems := pairingProblems(routes, access, []routeCall{
				{"main", "DELETE", "/auth/session"},
				{"main", "POST", "/auth/impersonation/end"},
			}); len(problems) != 0 {
				t.Errorf("the registrations are not paired: %v", problems)
			}
		})
	}
}

// pairingProblems lists what is wrong with the halves of the registrations: a
// route whose access level was not written beside it, and an access level written
// for a route nobody registered. The two halves of a registration name one
// (endpoint, method, path), or the registration is half made. The routes in
// guarded are the ones registered with no access level of their own, which the
// gateway guards by default.
func pairingProblems(routes []routeCall, access []accessCall, guarded []routeCall) []string {
	var problems []string
	for _, a := range access {
		if !slices.Contains(routes, routeCall{a.endpoint, a.method, a.path}) {
			problems = append(problems, fmt.Sprintf("%s was registered for %s %s on %q, a route nobody "+
				"registered: a typo in the path leaves the route guarded and the exemption dead",
				a.kind, a.method, a.path, a.endpoint))
		}
	}
	for _, r := range routes {
		covered := slices.ContainsFunc(access, func(a accessCall) bool {
			return a.endpoint == r.endpoint && a.method == r.method && a.path == r.path
		})
		if !covered && !slices.Contains(guarded, r) {
			problems = append(problems, fmt.Sprintf("the route %s %s on %q was mounted with no access "+
				"level written beside it", r.method, r.path, r.endpoint))
		}
	}
	return problems
}

// TestThePairingCheckSeesAMissingAndAMistypedRegistration is the control for
// pairingProblems: it has to report a hand-written pair that went wrong, or the
// case above that relies on it would pass for any registration.
func TestThePairingCheckSeesAMissingAndAMistypedRegistration(t *testing.T) {
	routes := []routeCall{{"main", "POST", "/auth/login"}, {"main", "GET", "/auth/me"}}
	guarded := []routeCall{routes[1]}
	if got := pairingProblems(routes, []accessCall{{"Exempt", "main", "POST", "/auth/login"}}, guarded); len(got) != 0 {
		t.Fatalf("a correct pair was reported: %v", got)
	}
	for _, c := range []struct {
		name   string
		access []accessCall
	}{
		{"the exemption was left out", nil},
		{"the exemption's path has a typo", []accessCall{{"Exempt", "main", "POST", "/auth/logn"}}},
		{"the exemption's method is another", []accessCall{{"Exempt", "main", "GET", "/auth/login"}}},
		{"the exemption names another endpoint", []accessCall{{"Exempt", "admin", "POST", "/auth/login"}}},
	} {
		if got := pairingProblems(routes, c.access, guarded); len(got) == 0 {
			t.Errorf("a registration where %s was not reported", c.name)
		}
	}
}

// TestAnInstanceTheHostSuppliedForTheModuleIsLeftAlone holds the tolerance of
// every callback for an instance that is not the module's own product: the
// registry hands this module the instance that was supplied, Prepare does not
// run for it, and Init neither takes up the entry point nor registers anything,
// so a registrant gets the supplied one and the entry point and the gateway hear
// nothing from this module.
func TestAnInstanceTheHostSuppliedForTheModuleIsLeftAlone(t *testing.T) {
	l := &ledger{}
	supplied := mounttest.New(t)
	reader := authntest.ConfigReaderAll(t, `{}`, Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(Module())
	reg.Register(standIn("stand-in.gate", (*authn.Gate)(nil)))
	reg.Supply(standInGate{ledger: l}, "stand-in.gate")
	reg.Supply(supplied, ModuleName)
	reg.Supply(reader, config.ModuleName)
	user := registrant(func(m mount.Mounter) { m.Public("GET", "/login", handler) })
	reg.Register(user)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{ModuleName, "stand-in.gate", user.Name},
		Action: func(_ context.Context, reg *core.Registry) error {
			got, err := core.Resolve[mount.Mounter](reg)
			if err != nil || got != mount.Mounter(supplied) {
				t.Errorf("the capability resolves to %v (%v), want the supplied instance", got, err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if _, found := supplied.Registered("GET", "/auth/login"); !found {
		t.Error("the registrant's route did not reach the supplied instance")
	}
	if routes, access := l.snapshot(); len(routes) != 0 || len(access) != 0 {
		t.Errorf("this module wrote %v and %v for an instance that is not its own", routes, access)
	}
}
