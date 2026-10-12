package guard

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases built on this file drive a real registry through this module's
// descriptor: the configuration module with a reader the real loader produced,
// the entry point module itself, this module, and stand-ins for the capabilities
// it takes. What a product assembled by hand cannot show is which callback takes
// up which dependency — the engine and the catalog at New, the entry point at
// Init — and what the registry itself says of the descriptor's three deliveries,
// each exclusive.

// assemblyBody is the configuration a default drive runs on: one endpoint of the
// entry point, "public", which this module guards.
const assemblyBody = `{
	"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}}},
	"authz": {"guard": {"endpoints": ["public"]}}
}`

// anchorStandIn delivers the admission anchor this module names in the
// middleware's After. The ordering anchor is named and never called, so a type
// carrying the one marker method stands where the real admission layer would.
type anchorStandIn struct{}

func (anchorStandIn) AdmissionLayer() {}

// assembly is one drive. The zero value guards "public" with everything this
// module requires and a real entry point; each field takes one thing away or
// adds one, so a case holds what a single missing or doubled dependency does.
type assembly struct {
	// body is the configuration. The zero value uses assemblyBody.
	body string
	// noRouter leaves the entry point module out of the command.
	noRouter bool
	// noEnforcer and noCatalog leave out the stand-in for the capability named,
	// so the drive reaches resolution without it.
	noEnforcer, noCatalog bool
	// enforcer and catalog, when set, are the stand-ins the assembly delivers. A
	// case that holds the product to what New took up sets them to the very
	// instances it reads back.
	enforcer authz.Enforcer
	catalog  authz.Catalog
	// extra are further modules to register, each a participant under its own
	// name: a registrant that declares a route from its own Init.
	extra []core.Module
	// supplied are further capabilities, each a stand-in module: the optional
	// logger and meter provider, or a second provider of a face.
	supplied []authztest.Provider
}

// drive advances the assembly to depth and runs action there. The entry point's
// declarations are served to the reader whether or not the entry point takes
// part, the way a command tree written by hand leaves them, so the noRouter case
// still loads the http section and instead leaves the module out of the
// registered set.
func (a assembly) drive(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = assemblyBody
	}
	module := Module()
	entry := speedhttp.Module()
	reader := authztest.ConfigReaderAll(t, body, module, entry)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(module)
	names := []string{ModuleName}
	if !a.noRouter {
		reg.Register(entry)
		names = append(names, speedhttp.ModuleName)
	}
	for _, m := range a.extra {
		reg.Register(m)
		names = append(names, m.Name)
	}
	if !a.noEnforcer {
		e := a.enforcer
		if e == nil {
			e = authztest.NewEnforcer()
		}
		authztest.AddProvider(reg, authztest.EnforcerProvider(e))
	}
	if !a.noCatalog {
		c := a.catalog
		if c == nil {
			c = authztest.NewCatalog(authz.PermissionAccountRead)
		}
		authztest.AddProvider(reg, authztest.CatalogProvider(c))
	}
	authztest.AddProvider(reg, authztest.Provider{
		Name: "stand-in.admission", Token: (*tenant.Admission)(nil), Instance: anchorStandIn{},
	})
	for _, p := range a.supplied {
		authztest.AddProvider(reg, p)
	}
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{Depth: depth, Participants: names, Action: action})
}

// productOfAssembly resolves the module's product under one of the faces it
// delivers, for a case that reads back what a callback took up.
func productOfAssembly(t *testing.T, reg *core.Registry) *product {
	t.Helper()
	guardAPI, err := core.Resolve[Guard](reg)
	if err != nil {
		t.Fatalf("resolving the handler API: %v", err)
	}
	p, ok := guardAPI.(*product)
	if !ok {
		t.Fatalf("the delivered handler API is a %T, not this module's product", guardAPI)
	}
	return p
}

// Each callback takes up the dependency the design places on it: New takes the
// engine and the catalog, building the declaration table over the catalog; Init
// takes the entry point and the optional capabilities. The engine and the
// catalog are held to the very stand-ins the assembly delivered, so a New that
// resolved something else fails. Init's read of the entry point is held from the
// other side by the no-entry-point and unknown-endpoint cases below.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	catalog := authztest.NewCatalog(authz.PermissionAccountRead)
	logger := slog.New(newRecorder())
	meter := sdkmetric.NewMeterProvider()
	t.Cleanup(func() {
		if err := meter.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	t.Run("New takes the engine and the catalog up", func(t *testing.T) {
		var product *product
		err := assembly{enforcer: enforcer, catalog: catalog}.drive(t, core.StageNew,
			func(_ context.Context, reg *core.Registry) error {
				product = productOfAssembly(t, reg)
				return nil
			})
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		if any(product.enforcer) != any(enforcer) {
			t.Errorf("New took up engine %v, want the delivered %v", product.enforcer, enforcer)
		}
		if any(product.catalog) != any(catalog) {
			t.Errorf("New took up catalog %v, want the delivered %v", product.catalog, catalog)
		}
		if product.gate == nil || any(product.gate.catalog) != any(catalog) {
			t.Error("New left the declaration table unbuilt over the delivered catalog")
		}
	})

	t.Run("Init takes the entry point and the optional capabilities up", func(t *testing.T) {
		var product *product
		err := assembly{enforcer: enforcer, catalog: catalog, supplied: []authztest.Provider{
			{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: logger}},
			{Name: "stand-in.meter", Token: (*observability.MeterProvider)(nil), Instance: meter},
		}}.drive(t, core.StageInit, func(_ context.Context, reg *core.Registry) error {
			product = productOfAssembly(t, reg)
			return nil
		})
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		if product.logger != logger {
			t.Errorf("Init took up logger %v, want the delivered %v", product.logger, logger)
		}
		if any(product.meter) != any(meter) {
			t.Errorf("Init took up meter %v, want the delivered %v", product.meter, meter)
		}
	})

	t.Run("the optional capabilities are not taken up before Init", func(t *testing.T) {
		var product *product
		err := assembly{enforcer: enforcer, catalog: catalog, supplied: []authztest.Provider{
			{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: logger}},
			{Name: "stand-in.meter", Token: (*observability.MeterProvider)(nil), Instance: meter},
		}}.drive(t, core.StageNew, func(_ context.Context, reg *core.Registry) error {
			product = productOfAssembly(t, reg)
			return nil
		})
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		// New leaves the module's default logger and a nil meter; the optional
		// capabilities are taken up in Init. A module that took them up earlier
		// would already hold the delivered instances here.
		if product.logger == logger {
			t.Error("New took up the delivered logger: the optional capabilities are taken up at Init")
		}
		if product.meter != nil {
			t.Error("New took up the delivered meter provider: the optional capabilities are taken up at Init")
		}
	})
}

// The registry answers the three faces this module delivers, and all three are
// one instance of its product: a module that asked for the declaration anchor
// and one that asked for the seam decide from the same table. The descriptor
// declares each delivery exclusive, which is what makes a second provider a
// startup failure rather than a resolution.
func TestTheAssemblyAnswersAllThreeFacesExclusively(t *testing.T) {
	t.Run("the registry answers each face from one instance", func(t *testing.T) {
		var gate authz.Gate
		var handler Guard
		var decider authn.Decider
		err := assembly{}.drive(t, core.StageNew, func(_ context.Context, reg *core.Registry) error {
			var err error
			if gate, err = core.Resolve[authz.Gate](reg); err != nil {
				return err
			}
			if handler, err = core.Resolve[Guard](reg); err != nil {
				return err
			}
			if decider, err = core.Resolve[authn.Decider](reg); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		if gate == nil || handler == nil || decider == nil {
			t.Fatalf("a face was not delivered: gate %v handler %v decider %v", gate, handler, decider)
		}
		if any(handler) != any(gate) || any(decider) != any(gate) {
			t.Error("the three faces are not one instance of this module's product")
		}
	})

	t.Run("the descriptor delivers each exclusively", func(t *testing.T) {
		exclusive := map[core.Token]bool{}
		for _, p := range Module().Provides {
			exclusive[p.Token] = p.Exclusive
		}
		for _, tok := range []core.Token{(*authz.Gate)(nil), (*Guard)(nil), (*authn.Decider)(nil)} {
			if !exclusive[tok] {
				t.Errorf("the %T delivery is not declared exclusive by the descriptor", tok)
			}
		}
	})
}

// The engine and the catalog are required as each other: a command that brings
// this module in without either fails resolution with ErrMissingProvider, before
// New builds anything. Without an engine the module has nothing to decide with;
// without a catalog the declaration surface it delivers can check nothing.
func TestAMissingEngineOrCatalogFailsResolution(t *testing.T) {
	for name, a := range map[string]assembly{
		"no engine":  {noEnforcer: true},
		"no catalog": {noCatalog: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := a.drive(t, core.StageNew, nil)
			if !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
			}
		})
	}
}

// A second provider of any of the three faces fails the startup: each is claimed
// exclusively, and the stand-in claims nothing, so the guard module's own claim
// is what catches it. The failure is at resolution, before any request.
func TestASecondProviderOfAnyFaceFailsAssembly(t *testing.T) {
	for name, p := range map[string]authztest.Provider{
		"the declaration anchor": authztest.GateProvider(authztest.NewGate()),
		"the handler API":        {Name: "stand-in.guard", Token: (*Guard)(nil), Instance: guardStub{}},
		"the decision seam":      {Name: "stand-in.decider", Token: (*authn.Decider)(nil), Instance: refusingDecider{}},
	} {
		t.Run(name, func(t *testing.T) {
			err := assembly{supplied: []authztest.Provider{p}}.drive(t, core.StageInit, nil)
			if !errors.Is(err, core.ErrExclusiveViolated) {
				t.Fatalf("a second provider of %s ended the drive with %v, want ErrExclusiveViolated", name, err)
			}
		})
	}
}

// A command that guards routes and names no entry point fails wiring with
// ErrNoRouter, naming this module: guarding endpoints nothing serves guards
// nothing, and the fix is to write the entry point module's name into the
// command.
func TestAGuardWithNoEntryPointFailsAssembly(t *testing.T) {
	err := assembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// An endpoint the configuration guards but the entry point does not declare
// fails Init with ErrUnknownEndpoint, naming the endpoint: it is the endpoint
// lookup that reads the entry point, and a name it does not serve stops the
// startup rather than leaving a guard registered nowhere.
func TestAGuardedEndpointTheEntryPointDoesNotServeFailsAssembly(t *testing.T) {
	body := `{
		"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}}},
		"authz": {"guard": {"endpoints": ["public", "typo"]}}
	}`
	err := assembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if !strings.Contains(err.Error(), `"typo"`) {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// A route that declares a permission the catalog does not hold fails Start with
// ErrUnknownPermission, naming the route and the permission. A registrant writes
// its route from its own Init, so the declaration lands after New and before
// Start, where the gate holds every declaration to the catalog once and for all:
// a route that decides on a permission nothing declares is a startup failure and
// not a route that decides nothing.
func TestADeclaredPermissionOutsideTheCatalogFailsAssembly(t *testing.T) {
	err := assembly{
		extra: []core.Module{registrantModule},
		// A logger the case captures keeps the module's startup diagnostics off
		// the test output; the drive reaches wire, which writes them.
		supplied: []authztest.Provider{
			{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: slog.New(newRecorder())}},
		},
	}.drive(t, core.StageStart, nil)
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownPermission", err)
	}
	for _, want := range []string{nethttp.MethodGet, "/orders", string(authz.PermissionAccountDelete)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %q does not name %q", err, want)
		}
	}
}

// Start holds the declarations to the catalog before it registers the metric
// callback: a route on a permission outside the catalog aborts the startup, and
// the meter is never asked for the counter, so a layer that did not start
// registers nothing on a provider that outlives it. Registering the callback
// first would ask the meter for the counter before the abort.
func TestStartAbortsBeforeRegisteringTheCounter(t *testing.T) {
	provider := sdkmetric.NewMeterProvider()
	probe := &meterProbe{MeterProvider: provider}
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})

	err := assembly{
		extra: []core.Module{registrantModule},
		supplied: []authztest.Provider{
			{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: slog.New(newRecorder())}},
			{Name: "stand-in.meter", Token: (*observability.MeterProvider)(nil), Instance: probe},
		},
	}.drive(t, core.StageStart, nil)
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownPermission before the counter is registered", err)
	}
	if probe.askedForMeter() {
		t.Error("Start asked the meter for a counter before verify aborted the start: the counter was registered first")
	}
}

// meterProbe records whether the module asked it for a meter, which is how a
// case reads that registerMetrics ran. It stands over a real provider, so a
// registration that does happen is valid.
type meterProbe struct {
	metric.MeterProvider
	mu    sync.Mutex
	asked bool
}

func (p *meterProbe) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	p.mu.Lock()
	p.asked = true
	p.mu.Unlock()
	return p.MeterProvider.Meter(name, opts...)
}

func (p *meterProbe) askedForMeter() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked
}

// The module names its logger for itself, so its lines are recognisable in a
// host's log. The stand-in records the name it was handed.
func TestTheLoggerIsNamedForTheModule(t *testing.T) {
	var name string
	err := assembly{supplied: []authztest.Provider{
		{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: namedLog{logger: slog.New(newRecorder()), name: &name}},
	}}.drive(t, core.StageInit, nil)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if name != ModuleName {
		t.Errorf("the module named its logger %q, want %q", name, ModuleName)
	}
}

// namedLog delivers the logging capability over one logger and records the name
// it is handed, so a case can hold what the module passes to Named.
type namedLog struct {
	logger *slog.Logger
	name   *string
}

func (l namedLog) Named(name string) *slog.Logger { *l.name = name; return l.logger }
func (namedLog) Redaction() log.Redaction         { return noRules{} }
