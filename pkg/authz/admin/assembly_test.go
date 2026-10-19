package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// The cases built on this file drive this module's descriptor through a real
// registry: the configuration module with a reader the real loader produced,
// this module itself, and stand-ins for the four required capabilities and for
// the entry point it takes up in Init. What a product built by hand cannot show
// is which callback takes up which dependency — the manager and the catalog at
// New, the entry point and the optional route-declaration surface at Init —
// what a single missing capability does at resolution, and what the descriptor
// says of its own delivery, which is none.
//
// This module delivers no capability, so no lookup returns its product the way
// resolving the enforcement module's guard returns that one. A case that holds
// what New took up registers this module's descriptor with its New wrapped to
// record the product, which observes the instance without changing what New does.

// managementBody is the configuration a default drive runs on: one endpoint of
// the entry point, "public", that the management routes are mounted on.
const managementBody = `{"authz": {"admin": {"endpoints": ["public"]}}}`

// recordingModule is this module's descriptor with its New wrapped to record the
// product it built. The wrapper records the instance and returns it unchanged,
// so the descriptor behaves as it does when registered as it stands.
func recordingModule(record *any) core.Module {
	m := Module()
	inner := m.New
	m.New = func(ctx context.Context, reg *core.Registry) (any, error) {
		instance, err := inner(ctx, reg)
		if err != nil {
			return nil, err
		}
		*record = instance
		return instance, nil
	}
	return m
}

// managementAssembly is one drive. The zero value mounts the management routes
// on "public" with everything this module requires and a stand-in entry point;
// each field takes one capability away, replaces one stand-in, or records the
// product New built, so a case holds what a single missing capability or a
// delivered instance does.
type managementAssembly struct {
	// body is the configuration. The zero value uses managementBody.
	body string
	// noManager, noCatalog, noGuard and noAudit leave out the stand-in for the
	// required capability named, so the drive reaches resolution without it.
	noManager, noCatalog, noGuard, noAudit bool
	// noRouter leaves the entry point out, which fails Init.
	noRouter bool
	// manager and catalog, when set, are the stand-ins delivered as those
	// capabilities; a case that holds the product to what New took up sets them
	// to the very instances it reads back.
	manager authz.Manager
	catalog authz.Catalog
	// router is the entry point stand-in; nil uses one that declares "public".
	router speedhttp.Router
	// gate is the optional route-declaration surface, taken up in Init when set.
	gate authz.Gate
	// logger is the logging capability, taken up in Init; nil uses a recorder.
	logger *slog.Logger
	// record, when set, receives the product New built.
	record *any
}

// drive advances the assembly to depth and runs action there.
func (a managementAssembly) drive(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = managementBody
	}
	module := Module()
	if a.record != nil {
		module = recordingModule(a.record)
	}
	reader := authntest.ConfigReader(t, module, body)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{ModuleName}
	reg.Register(module)

	if !a.noManager {
		m := a.manager
		if m == nil {
			m = &stubManager{}
		}
		reg.Register(standIn("stand-in.manager", m, (*authz.Manager)(nil)))
		names = append(names, "stand-in.manager")
	}
	if !a.noCatalog {
		c := a.catalog
		if c == nil {
			c = newStubCatalog()
		}
		reg.Register(standIn("stand-in.catalog", c, (*authz.Catalog)(nil)))
		names = append(names, "stand-in.catalog")
	}
	if !a.noGuard {
		reg.Register(standIn("stand-in.guard", &stubGuard{allow: true}, (*guard.Guard)(nil)))
		names = append(names, "stand-in.guard")
	}
	if !a.noAudit {
		reg.Register(standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)))
		names = append(names, "stand-in.audit")
	}
	logger := a.logger
	if logger == nil {
		logger = (&logBuffer{}).logger()
	}
	reg.Register(standIn("stand-in.logger", loggerStub{logger: logger}, (*log.Logger)(nil)))
	names = append(names, "stand-in.logger")
	if !a.noRouter {
		router := a.router
		if router == nil {
			router = newStubRouter("public")
		}
		reg.Register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if a.gate != nil {
		reg.Register(standIn("stand-in.gate", a.gate, (*authz.Gate)(nil)))
		names = append(names, "stand-in.gate")
	}
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{Depth: depth, Participants: names, Action: action})
}

// productOfAssembly narrows a recorded instance to this module's product, for a
// case that reads back what a callback took up.
func productOfAssembly(t *testing.T, instance any) *product {
	t.Helper()
	p, ok := instance.(*product)
	if !ok || p == nil {
		t.Fatalf("the built instance is %T, not this module's product", instance)
	}
	return p
}

// Each callback takes up the dependency the design places on it: New takes the
// policy manager and the permission catalog, and Init takes the entry point and
// the optional route-declaration surface. The manager and the catalog are held
// to the very stand-ins the drive delivered, so a New that took up something
// else fails. Init's read of the entry point is held from the other side by the
// no-router and unknown-endpoint cases below; the surface and the logger are
// read back directly.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	manager := &stubManager{}
	catalog := newStubCatalog(authz.PermissionRoleRead)
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the manager and the catalog up", func(t *testing.T) {
		var instance any
		err := managementAssembly{
			manager: manager, catalog: catalog, record: &instance,
		}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if any(p.manager) != any(manager) {
			t.Errorf("New took up manager %v, want the delivered %v", p.manager, manager)
		}
		if any(p.catalog) != any(catalog) {
			t.Errorf("New took up catalog %v, want the delivered %v", p.catalog, catalog)
		}
	})

	t.Run("Init takes the entry point and the optional surface up", func(t *testing.T) {
		router := newStubRouter("public")
		var instance any
		err := managementAssembly{
			manager: manager, catalog: catalog, router: router,
			gate: gate, logger: logger, record: &instance,
		}.drive(t, core.StageInit, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.gate == nil || any(p.gate) != any(gate) {
			t.Errorf("Init took up the declaration surface %v, want the delivered %v", p.gate, gate)
		}
		if p.logger != logger {
			t.Errorf("Init took up logger %v, want the delivered %v", p.logger, logger)
		}
		if got := router.endpoint("public").registered(); len(got) == 0 {
			t.Error("Init mounted no route on the delivered entry point")
		}
	})

	t.Run("the optional surface is not taken up before Init", func(t *testing.T) {
		var instance any
		err := managementAssembly{
			manager: manager, catalog: catalog, gate: gate, logger: logger, record: &instance,
		}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		// New leaves the module's default logger and a nil surface; the optional
		// capabilities are taken up in Init. A module that took them up earlier
		// would already hold the delivered instances here.
		if p.gate != nil {
			t.Error("New took up the declaration surface: it is taken up at Init")
		}
		if p.logger == logger {
			t.Error("New took up the delivered logger: the logging capability is taken up at Init")
		}
	})
}

// The four required capabilities are each another module's to deliver: a command
// that brings this one in without the manager, the catalog, the handler-internal
// guard or the audit capability fails resolution with ErrMissingProvider, before
// New builds anything. The manager and the catalog are what New takes up to read
// and write the policy tables through; the guard is what makes the per-request
// decision available; the audit capability is what makes a write's mandatory row
// land.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]managementAssembly{
		"no manager": {noManager: true},
		"no catalog": {noCatalog: true},
		"no guard":   {noGuard: true},
		"no audit":   {noAudit: true},
	} {
		t.Run(name, func(t *testing.T) {
			var built any
			a := missing
			a.record = &built
			err := a.drive(t, core.StageNew, nil)
			if !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
			}
			if built != nil {
				t.Error("an instance was constructed before resolution failed")
			}
		})
	}
}

// A command that mounts the management routes and names no entry point fails
// wiring with ErrNoRouter, naming this module: routes mounted nowhere are
// mounted on nothing, and the fix is to write the entry point module's name into
// the command.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	err := managementAssembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if err == nil || !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// An endpoint the configuration lists but the entry point does not declare fails
// Init with ErrUnknownEndpoint, naming the endpoint: it is the endpoint lookup
// that reads the entry point, and a name it does not serve stops the startup
// rather than mounting the management routes on an endpoint nobody serves.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"authz": {"admin": {"endpoints": ["public", "ghost"]}}}`
	err := managementAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// A real registry takes the module in and mounts its whole route surface on the
// configured endpoint, declaring every route open on the surface, and the
// descriptor delivers nothing: this module carries no capability another module
// resolves, and the routes are its only product.
func TestARealRegistryMountsEveryManagementRouteAndDeliversNothing(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	if err := (managementAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}

	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/system/permissions"}:                                       true,
		{http.MethodGet, "/api/v1/system/tenants/:tenant/roles"}:                             true,
		{http.MethodPut, "/api/v1/system/tenants/:tenant/roles/:role"}:                       true,
		{http.MethodDelete, "/api/v1/system/tenants/:tenant/roles/:role"}:                    true,
		{http.MethodGet, "/api/v1/system/tenants/:tenant/roles/:role/grants"}:                true,
		{http.MethodPut, "/api/v1/system/tenants/:tenant/roles/:role/grants/:permission"}:    true,
		{http.MethodDelete, "/api/v1/system/tenants/:tenant/roles/:role/grants/:permission"}: true,
		{http.MethodGet, "/api/v1/system/tenants/:tenant/bindings"}:                          true,
		{http.MethodGet, "/api/v1/system/tenants/:tenant/bindings/:account"}:                 true,
		{http.MethodPut, "/api/v1/system/tenants/:tenant/bindings/:account/:role"}:           true,
		{http.MethodDelete, "/api/v1/system/tenants/:tenant/bindings/:account/:role"}:        true,
	}
	got := router.endpoint("public").registered()
	if len(got) != len(want) {
		t.Fatalf("mounted %d routes (%v), want %d", len(got), got, len(want))
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected route %s %s", r.method, r.path)
		}
		if !gate.IsOpen(r.method, r.path) {
			t.Errorf("the route %s %s was not declared open", r.method, r.path)
		}
	}
}
