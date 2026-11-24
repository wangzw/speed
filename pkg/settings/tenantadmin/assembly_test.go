package tenantadmin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
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

const managementBody = `{"settings": {"tenantadmin": {"endpoints": ["public"]}}}`

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

type managementAssembly struct {
	body                                                       string
	noStore, noGuard, noAudit, noVerification, noWriteSettings bool
	noRouter                                                   bool
	noRouteGuard                                               bool
	routeGuard                                                 *stubRouteGuard
	store                                                      settings.Store
	verification                                               authn.SignupVerification
	writeSeam                                                  authz.WriteSettings
	router                                                     speedhttp.Router
	gate                                                       authz.Gate
	logger                                                     *slog.Logger
	record                                                     *any
}

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
	if !a.noVerification {
		v := a.verification
		if v == nil {
			v = &fakeSignupVerification{}
		}
		reg.Register(standIn("stand-in.verification", v, (*authn.SignupVerification)(nil)))
		names = append(names, "stand-in.verification")
	}
	if !a.noWriteSettings {
		w := a.writeSeam
		if w == nil {
			w = &stubWriteSettings{}
		}
		reg.Register(standIn("stand-in.write-settings", w, (*authz.WriteSettings)(nil)))
		names = append(names, "stand-in.write-settings")
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
	if !a.noRouteGuard {
		guard := a.routeGuard
		if guard == nil {
			guard = &stubRouteGuard{}
		}
		reg.Register(standIn("stand-in.route-guard", guard, (*speedhttp.TenantAdminRouteGuard)(nil)))
		names = append(names, "stand-in.route-guard")
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

func productOfAssembly(t *testing.T, instance any) *product {
	t.Helper()
	p, ok := instance.(*product)
	if !ok || p == nil {
		t.Fatalf("the built instance is %T, not this module's product", instance)
	}
	return p
}

// TestThisModuleDeliversNothing checks that the module provides no capability.
func TestThisModuleDeliversNothing(t *testing.T) {
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}
}

// TestAMissingRequiredCapabilityFailsResolution checks that each required
// capability missing fails resolution with ErrMissingProvider.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]managementAssembly{
		"no settings surface":    {noStore: true},
		"no guard":               {noGuard: true},
		"no audit":               {noAudit: true},
		"no verification":        {noVerification: true},
		"no settings write seam": {noWriteSettings: true},
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

// TestNoEntryPointIsErrNoRouter checks that a drive with no entry point fails with ErrNoRouter.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	err := managementAssembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if err == nil || !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// TestMountingWithoutThePathTenantGuardFailsInit checks that mounting the tenant
// routes without the tenant administration guard fails Init.
func TestMountingWithoutThePathTenantGuardFailsInit(t *testing.T) {
	err := managementAssembly{noRouteGuard: true}.drive(t, core.StageInit, nil)
	if err == nil {
		t.Fatal("the drive succeeded, want the mount to refuse without the guard")
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Errorf("the failure does not name the module: %v", err)
	}
	if !strings.Contains(err.Error(), "tenant administration guard") {
		t.Errorf("the failure does not name the missing guard: %v", err)
	}
}

// TestAnUnknownEndpointIsErrUnknownEndpoint checks that an endpoint the entry point does not declare fails Init naming it.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"settings": {"tenantadmin": {"endpoints": ["public", "ghost"]}}}`
	err := managementAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem checks which stage takes up each capability.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	store := &stubStore{values: map[string]any{}}
	seam := &stubWriteSettings{}
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the required capabilities up", func(t *testing.T) {
		var instance any
		err := managementAssembly{store: store, writeSeam: seam, record: &instance}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.store != store {
			t.Errorf("New took up store %v, want the delivered %v", p.store, store)
		}
		if p.writeSettings != seam {
			t.Errorf("New took up the write seam %v, want the delivered %v", p.writeSettings, seam)
		}
		if p.audit == nil || p.guard == nil || p.verification == nil {
			t.Error("New did not take up the guard, the audit capability or the verification read")
		}
	})

	t.Run("Init takes the entry point and the optional surface up", func(t *testing.T) {
		router := newStubRouter("public")
		var instance any
		err := managementAssembly{store: store, writeSeam: seam, router: router, gate: gate, logger: logger, record: &instance}.
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
		if p.routeGuard == nil {
			t.Error("Init did not take up the path-tenant guard")
		}
	})
}

// TestARealRegistryMountsEveryRouteAndDeclaresThemOpen checks that every route is mounted and declared open.
func TestARealRegistryMountsEveryRouteAndDeclaresThemOpen(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	if err := (managementAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/tenants/:tenant/settings"}:                    true,
		{http.MethodPut, "/api/v1/tenants/:tenant/settings/:namespace/:key"}:    true,
		{http.MethodDelete, "/api/v1/tenants/:tenant/settings/:namespace/:key"}: true,
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

// TestTheMountAppliesTheTenantAdminGuardToEveryRoute checks that every route
// gets the tenant administration guard, never the shared identity guard.
func TestTheMountAppliesTheTenantAdminGuardToEveryRoute(t *testing.T) {
	router := newStubRouter("public")
	guard := &stubRouteGuard{}
	if err := (managementAssembly{router: router, routeGuard: guard}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	got := len(router.endpoint("public").registered())
	if want := guard.adminCount(); want != got {
		t.Fatalf("the tenant administration guard wrapped %d handlers, want %d (one per route)", want, got)
	}
	if shared := guard.sharedCount(); shared != 0 {
		t.Fatalf("the shared path-tenant guard was applied %d times, want the tenant administration guard alone", shared)
	}
}

// TestTheModuleDeclaresNoPermissionVocabulary checks that the module registers no permission catalog entry or translation row.
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

// TestTheDescriptorRequiresItsCapabilities checks the required capabilities and the optional logger.
func TestTheDescriptorRequiresItsCapabilities(t *testing.T) {
	optional := map[any]bool{}
	for _, req := range Module().Requires {
		optional[req.Token] = req.Optional
	}
	for _, tk := range []any{
		(*settings.Store)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil),
		(*authn.SignupVerification)(nil), (*authz.WriteSettings)(nil),
	} {
		opt, present := optional[tk]
		if !present {
			t.Errorf("the descriptor does not require %T", tk)
		}
		if opt {
			t.Errorf("%T is optional, want required", tk)
		}
	}
	if opt, present := optional[(*log.Logger)(nil)]; !present || !opt {
		t.Error("the logger is not taken as an optional requirement")
	}
}
