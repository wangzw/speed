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
	"github.com/vislake/speed/pkg/settings"
)

// The cases built on this file drive this module's descriptor through a real
// registry: the configuration module with a reader the real loader produced,
// this module itself, and stand-ins for the required capabilities and for the
// entry point it takes up in Init. What a product built by hand cannot show is
// which callback takes up which dependency, what a single missing capability
// does at resolution, and what the descriptor says of its own delivery, which is
// none.

// managementBody is the configuration a default drive runs on: one endpoint of
// the entry point, "public", that the management routes are mounted on.
const managementBody = `{"settings": {"admin": {"endpoints": ["public"]}}}`

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
// product New built.
type managementAssembly struct {
	// body is the configuration. The zero value uses managementBody.
	body string
	// noStore, noGuard and noAudit leave out the stand-in for the required
	// capability named, so the drive reaches resolution without it.
	noStore, noGuard, noAudit bool
	// noRouter leaves the entry point out, which fails Init.
	noRouter bool
	// store is the settings surface stand-in delivered as that capability.
	store settings.Store
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

	if !a.noStore {
		s := a.store
		if s == nil {
			s = &stubStore{values: map[string]any{}}
		}
		reg.Register(standIn("stand-in.store", s, (*settings.Store)(nil)))
		names = append(names, "stand-in.store")
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

// productOfAssembly narrows a recorded instance to this module's product.
func productOfAssembly(t *testing.T, instance any) *product {
	t.Helper()
	p, ok := instance.(*product)
	if !ok || p == nil {
		t.Fatalf("the built instance is %T, not this module's product", instance)
	}
	return p
}

// This module delivers no capability: it carries no product another module
// resolves, and the routes are its only product.
func TestThisModuleDeliversNothing(t *testing.T) {
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}
}

// The three required capabilities are each another module's to deliver: a
// command that brings this one in without the settings surface, the guard or the
// audit capability fails resolution with ErrMissingProvider, before New builds
// anything.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]managementAssembly{
		"no settings surface": {noStore: true},
		"no guard":            {noGuard: true},
		"no audit":            {noAudit: true},
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
// wiring with ErrNoRouter, naming this module.
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
// Init with ErrUnknownEndpoint, naming the endpoint.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"settings": {"admin": {"endpoints": ["public", "ghost"]}}}`
	err := managementAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// New takes the settings surface, the guard and the audit capability up, and
// Init takes the entry point and the optional route-declaration surface.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	store := &stubStore{values: map[string]any{}}
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the required capabilities up", func(t *testing.T) {
		var instance any
		err := managementAssembly{store: store, record: &instance}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.store != store {
			t.Errorf("New took up store %v, want the delivered %v", p.store, store)
		}
		if p.audit == nil || p.guard == nil {
			t.Error("New did not take up the guard or the audit capability")
		}
	})

	t.Run("Init takes the entry point and the optional surface up", func(t *testing.T) {
		router := newStubRouter("public")
		var instance any
		err := managementAssembly{store: store, router: router, gate: gate, logger: logger, record: &instance}.
			drive(t, core.StageInit, nil)
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
	})
}

// A real registry mounts the module's whole route surface on the configured
// endpoint and declares every route open on the surface, so the middleware lets
// a request reach the handler, which makes the decision.
func TestARealRegistryMountsEveryRouteAndDeclaresThemOpen(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	if err := (managementAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/system/settings"}:                    true,
		{http.MethodGet, "/api/v1/system/settings/:namespace"}:         true,
		{http.MethodPut, "/api/v1/system/settings/:namespace/:key"}:    true,
		{http.MethodDelete, "/api/v1/system/settings/:namespace/:key"}: true,
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

// This module declares no permission vocabulary of its own: settings.read and
// settings.write are carried for the settings capability by the authorization
// directory, so this module registers no catalog entry and no translation row.
func TestTheModuleDeclaresNoPermissionVocabulary(t *testing.T) {
	var sawSchema bool
	for _, res := range Module().Resources {
		switch res.(type) {
		case authz.PermissionSpec:
			t.Error("the module declares a permission spec; its permissions belong to the authorization directory")
		case authz.TranslationSpec:
			t.Error("the module declares a translation spec; it decides on permissions it does not own")
		case config.Schema:
			sawSchema = true
		}
	}
	if !sawSchema {
		t.Error("the module declares no configuration schema")
	}
}
