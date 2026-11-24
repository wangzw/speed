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
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/tenant"
)

const assemblyBody = `{
	"http": {"listeners": {"public": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}}},
	"authz": {"guard": {"endpoints": ["public"]}}
}`

type anchorStandIn struct{}

func (anchorStandIn) AdmissionLayer() {}

type assembly struct {
	body                  string
	noRouter              bool
	noEnforcer, noCatalog bool
	enforcer              authz.Enforcer
	catalog               authz.Catalog
	extra                 []core.Module
	supplied              []authztest.Provider
}

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

// TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem checks that the assembly takes up its dependencies at the stage that reads them.
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
		if product.logger == logger {
			t.Error("New took up the delivered logger: the optional capabilities are taken up at Init")
		}
		if product.meter != nil {
			t.Error("New took up the delivered meter provider: the optional capabilities are taken up at Init")
		}
	})
}

// TestTheAssemblyAnswersAllThreeFacesExclusively checks that the assembly answers all three faces exclusively.
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

// TestAMissingEngineOrCatalogFailsResolution checks that a missing engine or catalog fails resolution.
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

// TestASecondProviderOfAnyFaceFailsAssembly checks that a second provider of any face fails assembly.
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

// TestAGuardWithNoEntryPointFailsAssembly checks that a guard with no entry point fails assembly.
func TestAGuardWithNoEntryPointFailsAssembly(t *testing.T) {
	err := assembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// TestAGuardedEndpointTheEntryPointDoesNotServeFailsAssembly checks that a guarded endpoint the entry point does not serve fails assembly.
func TestAGuardedEndpointTheEntryPointDoesNotServeFailsAssembly(t *testing.T) {
	body := `{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}}},
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

// TestADeclaredPermissionOutsideTheCatalogFailsAssembly checks that a declared permission outside the catalog fails assembly.
func TestADeclaredPermissionOutsideTheCatalogFailsAssembly(t *testing.T) {
	err := assembly{
		extra: []core.Module{registrantModule},
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

// TestAModuleDeclaredTranslationRowLetsAssemblyStart checks that a module declared translation row lets assembly start.
func TestAModuleDeclaredTranslationRowLetsAssemblyStart(t *testing.T) {
	permission := authz.Permission("crm.deal.read")
	t.Cleanup(translation.Reset)

	err := assembly{
		extra:   []core.Module{translationRegistrant(permission)},
		catalog: authztest.NewCatalog(permission),
		supplied: []authztest.Provider{
			{Name: "stand-in.logger", Token: (*log.Logger)(nil), Instance: logStub{logger: slog.New(newRecorder())}},
		},
	}.drive(t, core.StageStart, nil)
	if err != nil {
		t.Fatalf("the drive ended with %v, want nil: the module's declared row should reach the process translation", err)
	}
}

func translationRegistrant(permission authz.Permission) core.Module {
	return core.Module{
		Name:     "stand-in.business",
		Requires: []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Resources: []any{
			authz.PermissionSpec{Permissions: []authz.Permission{permission}},
			authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				permission: {Object: "deal", Action: "read"},
			}},
		},
		New: func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				return err
			}
			gate.Require(nethttp.MethodGet, "/deals", permission)
			return nil
		},
	}
}

// TestStartAbortsBeforeRegisteringTheCounter checks that start aborts before registering the counter.
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

// TestTheLoggerIsNamedForTheModule checks that the logger is named for the module.
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

type namedLog struct {
	logger *slog.Logger
	name   *string
}

func (l namedLog) Named(name string) *slog.Logger { *l.name = name; return l.logger }
func (namedLog) Redaction() log.Redaction         { return noRules{} }
