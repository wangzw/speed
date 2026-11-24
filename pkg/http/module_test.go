package http

import (
	"context"
	"errors"
	"net"
	nethttp "net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

func testProduct(t *testing.T, cfg moduleConfig) *router {
	t.Helper()
	logger, _ := newRecordingLogger()
	r, err := newModule(&stubReader{cfg: cfg}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("building the product: %v", err)
	}
	r.logger = logger
	return r
}

func oneEndpoint(name string) moduleConfig {
	return moduleConfig{
		Listeners: map[string]listenerConfig{name: {Address: "127.0.0.1:0"}},
		Endpoints: map[string]endpointConfig{name: {Listener: name}},
	}
}

// TestPrepareFailsWhenNoListenerIsConfigured pins that a host which took this module in but
// configured no listener gets a startup failure, not a stance.
func TestPrepareFailsWhenNoListenerIsConfigured(t *testing.T) {
	_, err := prepare(&stubReader{}, nil)
	if err == nil {
		t.Fatal("Prepare with no listener configured succeeded, so this entry point would " +
			"start and serve nothing")
	}
	mustContain(t, err.Error(), "http.listeners", "the configuration key to write")
}

// TestPrepareRefusesAnEndpointNamedWithTheWildcard pins where the reserved name is caught: in
// Prepare, with the rest of the configuration's checks, not in Serve once the stages have run.
func TestPrepareRefusesAnEndpointNamedWithTheWildcard(t *testing.T) {
	for name, cfg := range map[string]moduleConfig{
		"alone": oneEndpoint("*"),
		"beside another": {
			Listeners: map[string]listenerConfig{"public": {Address: "127.0.0.1:0"}},
			Endpoints: map[string]endpointConfig{
				"public": {Listener: "public"}, "*": {Listener: "public"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			stance, err := prepare(&stubReader{cfg: cfg}, nil)
			if err == nil {
				t.Fatalf("Prepare accepted an endpoint named \"*\" and stated %v", stance.State)
			}
			mustContain(t, err.Error(), `"*"`, "the endpoint whose name is refused")
			mustContain(t, err.Error(), "reserves", "that the name is refused as a reserved one")
			if strings.Contains(err.Error(), "no listening endpoint is configured") {
				t.Errorf("the entry named \"*\" was read as no endpoint at all: %v", err)
			}
		})
	}
}

// TestPrepareValidatesSpecsBeforeTheEndpointPremise pins the order inside Prepare.
func TestPrepareValidatesSpecsBeforeTheEndpointPremise(t *testing.T) {
	specs := []core.Resource[Spec]{{Module: "billing", Value: Spec{Endpoint: "public", Document: []byte(`{}`)}}}
	_, err := prepare(&stubReader{}, specs)
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("with no endpoint configured and a malformed fragment, Prepare returned %v; "+
			"the fragment's shape is checked first", err)
	}
	mustContain(t, err.Error(), "billing", "the module that declared the fragment")
}

// TestPrepareStatesEnabledNotAuto pins the pair that keeps this module from standing down.
func TestPrepareStatesEnabledNotAuto(t *testing.T) {
	stance, err := prepare(&stubReader{cfg: oneEndpoint("public")}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if stance.State != core.StateEnabled {
		t.Errorf("the stance is %v and has to be %v", stance.State, core.StateEnabled)
	}
}

// TestPrepareValidatesDeclaredSpecs pins where a malformed fragment is caught.
func TestPrepareValidatesDeclaredSpecs(t *testing.T) {
	specs := []core.Resource[Spec]{{Module: "billing", Value: Spec{Endpoint: "public", Document: []byte(`{}`)}}}
	_, err := prepare(&stubReader{cfg: oneEndpoint("public")}, specs)
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("a fragment without an openapi field did not fail Prepare: %v", err)
	}
	mustContain(t, err.Error(), "billing", "the module that declared the fragment")
}

// TestPrepareAndNewReadOneSection pins that the stance and the product are built from the same
// configuration path.
func TestPrepareAndNewReadOneSection(t *testing.T) {
	stance := &stubReader{cfg: oneEndpoint("public")}
	if _, err := prepare(stance, nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	product := &stubReader{cfg: oneEndpoint("public")}
	if _, err := newModule(product, nil, nil, nil, nil); err != nil {
		t.Fatalf("New: %v", err)
	}
	if stance.path != configPath || product.path != configPath {
		t.Errorf("Prepare read %q and New read %q; both are this module's section %q",
			stance.path, product.path, configPath)
	}
}

// TestModuleProvidesRouterExclusively pins the delivery declaration.
func TestModuleProvidesRouterExclusively(t *testing.T) {
	provides := Module().Provides
	if len(provides) != 1 {
		t.Fatalf("the module declares %d capabilities: %+v", len(provides), provides)
	}
	if got := reflect.TypeOf(provides[0].Token); got != reflect.TypeFor[*Router]() {
		t.Errorf("the capability delivered is %v, and it has to be (*Router)(nil)", got)
	}
	if !provides[0].Exclusive {
		t.Error("Router is delivered without the exclusive claim")
	}
}

// TestDeclaredDependenciesAreOptional pins all four declared dependencies.
func TestDeclaredDependenciesAreOptional(t *testing.T) {
	requires := Module().Requires
	want := []reflect.Type{
		reflect.TypeFor[*log.Logger](),
		reflect.TypeFor[*ErrorResponder](),
		reflect.TypeFor[*i18n.Localizer](),
		reflect.TypeFor[*i18n.Resolver](),
	}
	if len(requires) != len(want) {
		t.Fatalf("the module declares %d requirements: %+v", len(requires), requires)
	}
	for i, req := range requires {
		if got := reflect.TypeOf(req.Token); got != want[i] {
			t.Errorf("requirement %d is on %v, and it has to be %v", i, got, want[i])
		}
		if !req.Optional {
			t.Errorf("the dependency on %v is declared as mandatory", reflect.TypeOf(req.Token))
		}
	}

	injected, err := requestLogger(core.New(), "http")
	if err != nil {
		t.Fatalf("taking up the logger from a registry that has none: %v", err)
	}
	if injected != nil {
		t.Error("a logger was produced although no module delivers the capability")
	}

	responder, err := errorResponder(core.New())
	if err != nil {
		t.Fatalf("taking up the error responder from a registry that has none: %v", err)
	}
	if responder != nil {
		t.Error("an error responder was produced although no module delivers the capability")
	}

	localizer, err := localizerFor(core.New())
	if err != nil {
		t.Fatalf("taking up the localizer from a registry that has none: %v", err)
	}
	if localizer != nil {
		t.Error("a localizer was produced although no module delivers the capability")
	}

	resolver, err := resolverFor(core.New())
	if err != nil {
		t.Fatalf("taking up the resolver from a registry that has none: %v", err)
	}
	if resolver != nil {
		t.Error("a resolver was produced although no module delivers the capability")
	}
}

// TestTheReaderIsUndeclaredAndStillRequired pins the one thing this module takes up without
// declaring.
func TestTheReaderIsUndeclaredAndStillRequired(t *testing.T) {
	m := Module()
	for i, req := range m.Requires {
		if reflect.TypeOf(req.Token) == reflect.TypeFor[*config.Reader]() {
			t.Errorf("Requires[%d] declares the configuration reader, which this module takes up "+
				"in Prepare and New, where the instance the host supplied already exists, and "+
				"reads nowhere later: the entry would only order this module after the "+
				"config module", i)
		}
	}

	for _, c := range []struct {
		name     string
		registry func() *core.Registry
	}{
		{"no config module is registered", core.New},
		{"the config module is registered and was supplied nothing", func() *core.Registry {
			reg := core.New()
			reg.Register(config.Module())
			return reg
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := m.Prepare(context.Background(), c.registry())
			if !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("Prepare against a registry with no reader to give: %v", err)
			}
			mustContain(t, err.Error(), "configuration", "what the startup is missing")
		})
	}
}

// TestPrepareTakesUpTheSuppliedReader pins that the reader a host supplied is the one Prepare
// reads, in the stage that runs before any module is constructed.
func TestPrepareTakesUpTheSuppliedReader(t *testing.T) {
	m := Module()

	reg := core.New()
	supplyConfig(reg, oneEndpoint("public"))
	stance, err := m.Prepare(context.Background(), reg)
	if err != nil {
		t.Fatalf("Prepare with a supplied reader declaring an endpoint: %v", err)
	}
	if stance.State != core.StateEnabled {
		t.Errorf("the stance is %v and has to be %v", stance.State, core.StateEnabled)
	}

	empty := core.New()
	supplyConfig(empty, moduleConfig{})
	_, err = m.Prepare(context.Background(), empty)
	if err == nil {
		t.Fatal("Prepare with a supplied reader declaring no listener succeeded, so it did not " +
			"read the reader the host supplied")
	}
	if errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Prepare did not find the reader that was supplied: %v", err)
	}
	mustContain(t, err.Error(), "http.listeners", "the configuration key to write")
}

// TestImportingThisPackageCarriesALoggingModule pins the half of the optional dependency that makes
// it a statement of contract rather than a branch hosts reach: this package imports pkg/log.
func TestImportingThisPackageCarriesALoggingModule(t *testing.T) {
	for _, m := range core.ProcessRegistry.Modules() {
		for _, p := range m.Provides {
			if reflect.TypeOf(p.Token) == reflect.TypeFor[*log.Logger]() {
				return
			}
		}
	}
	t.Error("no module in the process registry delivers Logger, although importing this " +
		"package pulls pkg/log in and that registers itself")
}

// TestTheModuleIsRegisteredWithTheProcessRegistryUnderItsName pins the registration this package
// performs when it is initialised: a module named "http", delivering Router exclusively.
func TestTheModuleIsRegisteredWithTheProcessRegistryUnderItsName(t *testing.T) {
	if ModuleName != "http" {
		t.Errorf("ModuleName is %q, and the module is named %q", ModuleName, "http")
	}
	if got := Module().Name; got != ModuleName {
		t.Errorf("the descriptor is named %q, and ModuleName is %q", got, ModuleName)
	}

	var registered []core.Module
	for _, m := range core.ProcessRegistry.Modules() {
		if m.Name == ModuleName {
			registered = append(registered, m)
		}
	}
	if len(registered) != 1 {
		t.Fatalf("the process registry holds %d modules named %q, want exactly the one this "+
			"package registers", len(registered), ModuleName)
	}
	provides := registered[0].Provides
	if len(provides) != 1 || reflect.TypeOf(provides[0].Token) != reflect.TypeFor[*Router]() ||
		!provides[0].Exclusive {
		t.Errorf("the registered module provides %+v, want Router, exclusively", provides)
	}
}

// TestGateOpensInMigrateAndSealsInStart pins the two borrowed stages, which are what frames the
// window registrations are accepted in.
func TestGateOpensInMigrateAndSealsInStart(t *testing.T) {
	m := Module()
	product := testProduct(t, oneEndpoint("public"))
	e := endpointOf(t, product, "public")
	ctx := context.Background()

	wantPanic(t, "a registration before Migrate", func() {
		e.Route("GET", "/before", filler)
	})

	if err := m.Migrate(ctx, nil, product); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	e.Route("GET", "/during", filler)

	if err := m.Start(ctx, nil, product); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantPanic(t, "a registration during the Start stage", func() {
		e.Route("GET", "/after", filler)
	})
}

// TestServeBindsAndTheEndpointServes drives the stages in order and pins that what comes out the
// other end really answers requests, with the chain and the routes that were registered.
func TestServeBindsAndTheEndpointServes(t *testing.T) {
	m := Module()
	product := testProduct(t, oneEndpoint("public"))
	ctx := context.Background()
	t.Cleanup(func() { _ = m.Close(ctx, nil, product) })

	if err := m.Migrate(ctx, nil, product); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	e := endpointOf(t, product, "public")
	e.Route("GET", "/things", func(c *gin.Context) { c.Status(nethttp.StatusTeapot) })
	if err := m.Start(ctx, nil, product); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Serve(ctx, nil, product); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if !e.Accepting() {
		t.Fatal("the endpoint reports that it is not accepting requests after Serve returned")
	}
	addr := product.endpoints["public"].listener.addr().String()
	status, err := getStatus(ctx, addr, "/things")
	if err != nil {
		t.Fatalf("the bound endpoint refused a request: %v", err)
	}
	if status != nethttp.StatusTeapot {
		t.Errorf("the registered handler answered %d, not the %d it writes",
			status, nethttp.StatusTeapot)
	}

	if err := m.Stop(ctx, nil, product); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if e.Accepting() {
		t.Error("the endpoint still reports that it is accepting requests after Stop")
	}
	if err := m.Close(ctx, nil, product); err != nil {
		t.Errorf("Close after a clean stop: %v", err)
	}
}

// TestBindFailureReleasesTheEndpointsAlreadyBound pins the rollback across endpoints.
func TestBindFailureReleasesTheEndpointsAlreadyBound(t *testing.T) {
	taken, takeErr := net.Listen("tcp", "127.0.0.1:0")
	if takeErr != nil {
		t.Fatalf("taking a port to collide with: %v", takeErr)
	}
	defer taken.Close()

	m := Module()
	product := testProduct(t, moduleConfig{
		Listeners: map[string]listenerConfig{
			"a": {Address: "127.0.0.1:0"},
			"b": {Address: taken.Addr().String()},
		},
		Endpoints: map[string]endpointConfig{
			"a": {Listener: "a"},
			"b": {Listener: "b"},
		},
	})
	ctx := context.Background()

	if err := m.Migrate(ctx, nil, product); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := m.Start(ctx, nil, product); err != nil {
		t.Fatalf("Start: %v", err)
	}

	serveErr := m.Serve(ctx, nil, product)
	if !errors.Is(serveErr, ErrListen) {
		t.Fatalf("the second listener's bind failure did not end the startup: %v", serveErr)
	}
	first := product.endpoints["a"].listener.addr().String()
	if !product.endpoints["a"].Accepting() {
		t.Error("the endpoint that did bind is not accepting, so the failure was reported on the wrong one")
	}

	// This is what core does after a startup failure, over every constructed
	// instance whatever stage it reached.
	if err := m.Stop(ctx, nil, product); err != nil {
		t.Errorf("Stop during rollback: %v", err)
	}
	if err := m.Close(ctx, nil, product); err != nil {
		t.Errorf("Close during rollback: %v", err)
	}

	reopened, err := net.Listen("tcp", first)
	if err != nil {
		t.Fatalf("the port of the endpoint that did bind was not released: %v", err)
	}
	reopened.Close()
}

// TestStopAndCloseToleranceOnAnInstanceThatNeverServed pins the rollback obligation from the other
// side: a startup that fails before Serve still runs both callbacks over this module's product.
func TestStopAndCloseToleranceOnAnInstanceThatNeverServed(t *testing.T) {
	m := Module()
	product := testProduct(t, oneEndpoint("public"))
	ctx := context.Background()

	if err := m.Stop(ctx, nil, product); err != nil {
		t.Errorf("Stop on a product that never served: %v", err)
	}
	if err := m.Close(ctx, nil, product); err != nil {
		t.Errorf("Close on a product that never served: %v", err)
	}
}

// TestStageCallbacksTolerateAForeignInstance pins the same obligation for the instance itself:
// rollback runs the callbacks on modules whose New never returned a product.
func TestStageCallbacksTolerateAForeignInstance(t *testing.T) {
	m := Module()
	ctx := context.Background()
	for name, call := range map[string]func(any) error{
		"Migrate": func(i any) error { return m.Migrate(ctx, nil, i) },
		"Start":   func(i any) error { return m.Start(ctx, nil, i) },
		"Serve":   func(i any) error { return m.Serve(ctx, nil, i) },
		"Stop":    func(i any) error { return m.Stop(ctx, nil, i) },
		"Close":   func(i any) error { return m.Close(ctx, nil, i) },
	} {
		for _, instance := range []any{nil, (*router)(nil), "not a router", &foreignRouter{}} {
			if err := call(instance); err != nil {
				t.Errorf("%s on the instance %#v: %v", name, instance, err)
			}
		}
	}
}

// TestAnErrorResponderDeliveredByAModuleDecidesTheShape pins the optional dependency over a whole
// assembly, on a real connection: with a module delivering ErrorResponder in the registry.
func TestAnErrorResponderDeliveredByAModuleDecidesTheShape(t *testing.T) {
	responder := &recordingResponder{}
	addr, _ := serveRoutes(t, responder, func(e Endpoint) {
		e.Route("GET", "/refuse", failWith(Problem{Status: nethttp.StatusConflict}))
		e.Route("GET", "/boom", panicking("boom"))
	})

	for _, c := range []struct {
		name   string
		path   string
		status int
	}{
		{"a refusal through Fail", "/refuse", nethttp.StatusConflict},
		{"a path that matches no route", "/nowhere", nethttp.StatusNotFound},
		{"a panic", "/boom", nethttp.StatusInternalServerError},
	} {
		got, err := getResponse(context.Background(), addr, c.path)
		if err != nil {
			t.Fatalf("%s: the request failed: %v", c.name, err)
		}
		if got.status != c.status {
			t.Errorf("%s: the status was %d, want %d", c.name, got.status, c.status)
		}
		if got.header.Get("Content-Type") != customMediaType {
			t.Errorf("%s: the media type was %q, want the responder's %q", c.name,
				got.header.Get("Content-Type"), customMediaType)
		}
		if want := customBody + strconv.Itoa(c.status); got.body != want {
			t.Errorf("%s: the body was %q, want the responder's %q", c.name, got.body, want)
		}
	}
	if responder.calls() != 3 {
		t.Errorf("the responder was asked %d times for three failures, want once for each", responder.calls())
	}
}

// TestWithoutAnErrorResponderTheDefaultShapeIsWritten is the same three failures in an assembly
// where no module delivers the capability, which is the ordinary case.
func TestWithoutAnErrorResponderTheDefaultShapeIsWritten(t *testing.T) {
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/refuse", failWith(Problem{Status: nethttp.StatusConflict}))
		e.Route("GET", "/boom", panicking("boom"))
	})

	for _, path := range []string{"/refuse", "/nowhere", "/boom"} {
		got, err := getResponse(context.Background(), addr, path)
		if err != nil {
			t.Fatalf("%s: the request failed: %v", path, err)
		}
		if got.header.Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: the media type was %q, want %q", path, got.header.Get("Content-Type"),
				"application/problem+json")
		}
	}
}

// TestTwoErrorRespondersFailTheStartup pins what delivering the capability from two modules does:
// the lookup is ambiguous, which this module reports as a startup failure rather than picking one.
func TestTwoErrorRespondersFailTheStartup(t *testing.T) {
	deliver := func(name string) core.Module {
		return (&stageProbe{
			name:     name,
			provides: []core.Provision{{Token: (*ErrorResponder)(nil)}},
			product:  &recordingResponder{},
		}).module()
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), deliver("probe.shape-one"), deliver("probe.shape-two"))

	runErr := a.stop(t)
	if !errors.Is(runErr, core.ErrAmbiguousProvider) {
		t.Fatalf("two modules delivering ErrorResponder came back with %v, want a startup failure "+
			"wrapping core.ErrAmbiguousProvider", runErr)
	}
	mustContain(t, runErr.Error(), "probe.shape-one", "one of the two deliverers")
	mustContain(t, runErr.Error(), "probe.shape-two", "the other deliverer")
}

// TestTwoLoggersFailTheStartup is the same statement for the Logger capability: the dependency is
// optional, and the only lookup result this module reads as "no logger" is that nobody delivers it.
func TestTwoLoggersFailTheStartup(t *testing.T) {
	logger, _ := newRecordingLogger()
	deliver := func(name string) core.Module {
		return (&stageProbe{
			name:     name,
			provides: []core.Provision{{Token: (*log.Logger)(nil)}},
			product:  stubLogger{logger: logger},
		}).module()
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), deliver("probe.log-one"), deliver("probe.log-two"))

	runErr := a.stop(t)
	if !errors.Is(runErr, core.ErrAmbiguousProvider) {
		t.Fatalf("two modules delivering Logger came back with %v, want a startup failure wrapping "+
			"core.ErrAmbiguousProvider", runErr)
	}
	mustContain(t, runErr.Error(), "probe.log-one", "one of the two deliverers")
	mustContain(t, runErr.Error(), "probe.log-two", "the other deliverer")
}

// TestTheLoggerInjectedIntoRequestsIsNamedAfterTheModule pins the name this module asks the
// delivered Logger for.
func TestTheLoggerInjectedIntoRequestsIsNamedAfterTheModule(t *testing.T) {
	logger, _ := newRecordingLogger()
	delivering := &namingLogger{stubLogger: stubLogger{logger: logger}}
	deliverer := &stageProbe{
		name:     "probe.delivers-the-logger",
		provides: []core.Provision{{Token: (*log.Logger)(nil)}},
		product:  delivering,
	}

	a := startAssembly(t, oneEndpoint("public"), Module(), deliverer.module())
	if err := a.stop(t); err != nil {
		t.Fatalf("a run that served and was then cancelled came back with %v", err)
	}

	if got, want := delivering.asked(), []string{ModuleName}; !slices.Equal(got, want) {
		t.Errorf("the module asked the Logger capability for the names %v, want %v", got, want)
	}
}
