package profile

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
	"github.com/vislake/speed/pkg/feature"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
)

const moduleBody = `{"tenant": {"profile": {"endpoints": ["api"]}}}`

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

type moduleAssembly struct {
	body                                        string
	noCatalog, noGuard, noAudit, noVerification bool
	noRouteGuard                                bool
	noRouter                                    bool
	router                                      speedhttp.Router
	gate                                        authz.Gate
	accounts                                    authn.Accounts
	logger                                      *slog.Logger
	record                                      *any
}

func (a moduleAssembly) drive(t *testing.T, depth core.Stage, action func(context.Context, *core.Registry) error) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = moduleBody
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

	if !a.noCatalog {
		reg.Register(standIn("stand-in.catalog", &stubCatalog{}, (*tenant.Catalog)(nil)))
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
	if !a.noVerification {
		reg.Register(standIn("stand-in.verification", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)))
		names = append(names, "stand-in.verification")
	}
	logger := a.logger
	if logger == nil {
		logger = (&logBuffer{}).logger()
	}
	reg.Register(standIn("stand-in.logger", loggerStub{logger: logger}, (*log.Logger)(nil)))
	names = append(names, "stand-in.logger")
	if !a.noRouteGuard {
		reg.Register(standIn("stand-in.route-guard", permissiveRouteGuard{}, (*speedhttp.TenantAdminRouteGuard)(nil)))
		names = append(names, "stand-in.route-guard")
	}
	if !a.noRouter {
		router := a.router
		if router == nil {
			router = newStubRouter("api")
		}
		reg.Register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if a.gate != nil {
		reg.Register(standIn("stand-in.gate", a.gate, (*authz.Gate)(nil)))
		names = append(names, "stand-in.gate")
	}
	if a.accounts != nil {
		reg.Register(standIn("stand-in.accounts", a.accounts, (*authn.Accounts)(nil)))
		names = append(names, "stand-in.accounts")
	}
	if action == nil {
		action = func(context.Context, *core.Registry) error { return nil }
	}
	return reg.Drive(t.Context(), core.Drive{Depth: depth, Participants: names, Action: action})
}

// TestThisModuleDeliversNothing checks that the module provides no capability.
func TestThisModuleDeliversNothing(t *testing.T) {
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}
}

// TestAMissingRequiredCapabilityFailsResolution checks that a missing roster, guard, audit or verification read fails with ErrMissingProvider.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]moduleAssembly{
		"no catalog":      {noCatalog: true},
		"no guard":        {noGuard: true},
		"no audit":        {noAudit: true},
		"no verification": {noVerification: true},
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

// A command that mounts the routes and names no entry point fails wiring with
// tenant.ErrNoRouter, naming this module.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	err := moduleAssembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, tenant.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want tenant.ErrNoRouter", err)
	}
	if err == nil || !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// An endpoint the configuration lists but the entry point does not declare
// fails Init with tenant.ErrUnknownEndpoint, naming the endpoint.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"tenant": {"profile": {"endpoints": ["api", "ghost"]}}}`
	err := moduleAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, tenant.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want tenant.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// TestMountRefusesWithoutTheTenantAdministrationGuard checks that a mount with no tenant administration guard is refused.
func TestMountRefusesWithoutTheTenantAdministrationGuard(t *testing.T) {
	err := moduleAssembly{noRouteGuard: true}.drive(t, core.StageInit, nil)
	if err == nil {
		t.Fatal("the drive succeeded with no tenant administration guard")
	}
	if !strings.Contains(err.Error(), ModuleName) {
		t.Errorf("the failure does not name the module: %v", err)
	}
}

// The endpoint list refuses a shape the module cannot run on, naming the key.
func TestTheEndpointListRefusesShapesItCannotRunOn(t *testing.T) {
	cases := map[string]string{
		"empty":      `[]`,
		"star":       `["*"]`,
		"empty name": `[""]`,
		"duplicate":  `["api", "api"]`,
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"tenant": {"profile": {"endpoints": ` + endpoints + `}}}`
			err := moduleAssembly{body: body}.drive(t, core.StagePrepare, nil)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}

// TestARealRegistryMountsEveryRouteAndDeclaresThemOpen checks that a real registry mounts every route on the configured endpoint and declares them open.
func TestARealRegistryMountsEveryRouteAndDeclaresThemOpen(t *testing.T) {
	router := newStubRouter("api")
	gate := authztest.NewGate()
	if err := (moduleAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := map[string]bool{
		http.MethodGet + " /api/v1/tenants/:tenant/profile":   true,
		http.MethodPatch + " /api/v1/tenants/:tenant/profile": true,
	}
	got := router.endpoint("api").registered()
	if len(got) != len(want) {
		t.Fatalf("mounted %d routes (%v), want %d", len(got), got, len(want))
	}
	for _, r := range got {
		key := r.method + " " + r.path
		if !want[key] {
			t.Errorf("unexpected route %s %s", r.method, r.path)
		}
		if !gate.IsOpen(r.method, r.path) {
			t.Errorf("the route %s %s was not declared open", r.method, r.path)
		}
	}
}

// The default endpoint is the API endpoint: a configuration with no
// tenant.profile section mounts the routes there.
func TestTheDefaultEndpointIsTheAPIEndpoint(t *testing.T) {
	router := newStubRouter("api")
	if err := (moduleAssembly{body: `{}`, router: router}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got := len(router.endpoint("api").registered()); got != 2 {
		t.Errorf("mounted %d routes on api, want 2", got)
	}
}

// The descriptor declares the static feature the frontend gates the page and
// its navigation entry by.
func TestTheModuleDeclaresTheProfileFeature(t *testing.T) {
	found := false
	for _, resource := range Module().Resources {
		if spec, ok := resource.(feature.Spec); ok && spec.Name == "tenant.profile" {
			found = true
			if spec.Conditional {
				t.Error("the profile feature is conditional, want static: the surface is mounted whenever the module is in force")
			}
		}
	}
	if !found {
		t.Error("the module declares no tenant.profile feature")
	}
}
