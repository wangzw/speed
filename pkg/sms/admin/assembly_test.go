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
	"github.com/vislake/speed/pkg/sms"
)

const managementBody = `{"sms": {"admin": {"endpoints": ["public"]}}}`

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
	body                                     string
	noRegistry, noProvider, noGuard, noAudit bool
	noRouter                                 bool
	registry                                 sms.Registry
	provider                                 sms.Provider
	router                                   speedhttp.Router
	gate                                     authz.Gate
	logger                                   *slog.Logger
	record                                   *any
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

	if !a.noRegistry {
		r := a.registry
		if r == nil {
			r = &stubRegistry{}
		}
		reg.Register(standIn("stand-in.registry", r, (*sms.Registry)(nil)))
		names = append(names, "stand-in.registry")
	}
	if !a.noProvider {
		p := a.provider
		if p == nil {
			p = &stubProvider{name: "stub-carrier"}
		}
		reg.Register(standIn("stand-in.provider", p, (*sms.Provider)(nil)))
		names = append(names, "stand-in.provider")
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

// TestAMissingRequiredCapabilityFailsResolution checks that each of the four
// required capabilities missing fails resolution with ErrMissingProvider.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]managementAssembly{
		"no registry": {noRegistry: true},
		"no provider": {noProvider: true},
		"no guard":    {noGuard: true},
		"no audit":    {noAudit: true},
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

// TestAnUnknownEndpointIsErrUnknownEndpoint checks that an endpoint the entry point does not declare fails Init naming it.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"sms": {"admin": {"endpoints": ["public", "ghost"]}}}`
	err := managementAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem checks that New takes the
// registry, guard and audit, and Init the entry point and the declaration.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	registry := &stubRegistry{}
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the required capabilities up", func(t *testing.T) {
		provider := &stubProvider{name: "stub-carrier"}
		var instance any
		err := managementAssembly{registry: registry, provider: provider, record: &instance}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if any(p.registry) != any(registry) {
			t.Errorf("New took up registry %v, want the delivered %v", p.registry, registry)
		}
		if any(p.provider) != any(provider) {
			t.Errorf("New took up provider %v, want the delivered %v", p.provider, provider)
		}
		if p.audit == nil || p.guard == nil {
			t.Error("New did not take up the guard or the audit capability")
		}
	})

	t.Run("Init takes the entry point and the optional surface up", func(t *testing.T) {
		router := newStubRouter("public")
		var instance any
		err := managementAssembly{registry: registry, router: router, gate: gate, logger: logger, record: &instance}.
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

// TestARealRegistryMountsEveryRouteAndDeclaresThemOpen checks that every route
// is mounted on the configured endpoint and declared open.
func TestARealRegistryMountsEveryRouteAndDeclaresThemOpen(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	if err := (managementAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/system/sms/templates"}:                true,
		{http.MethodPost, "/api/v1/system/sms/templates"}:               true,
		{http.MethodPost, "/api/v1/system/sms/templates/submissions"}:   true,
		{http.MethodPost, "/api/v1/system/sms/templates/:name/enabled"}: true,
		{http.MethodDelete, "/api/v1/system/sms/templates/:name"}:       true,
		{http.MethodGet, "/api/v1/system/sms/carrier"}:                  true,
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

// TestAnAbsentEndpointListStandsTheModuleDown checks that Prepare stands the
// module down with a reason instead of failing the startup.
func TestAnAbsentEndpointListStandsTheModuleDown(t *testing.T) {
	reader := authntest.ConfigReader(t, Module(), "{}")

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got.State != core.StateDisabled || !strings.Contains(got.Reason, endpointsKey) {
		t.Errorf("Prepare = %+v, want disabled and naming %q", got, endpointsKey)
	}
}

// TestTheEndpointListRefusesShapesItCannotRunOn checks that an empty list and the every-endpoint name are refused naming the key.
func TestTheEndpointListRefusesShapesItCannotRunOn(t *testing.T) {
	cases := map[string]string{
		"empty":      `[]`,
		"star":       `["*"]`,
		"empty name": `[""]`,
		"duplicate":  `["public", "public"]`,
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"sms": {"admin": {"endpoints": ` + endpoints + `}}}`
			err := managementAssembly{body: body}.drive(t, core.StagePrepare, nil)
			if !errors.Is(err, sms.ErrInvalidConfig) {
				t.Fatalf("the drive ended with %v, want sms.ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), endpointsKey) {
				t.Errorf("the failure does not name the key %q: %v", endpointsKey, err)
			}
		})
	}
}

// TestTheModuleDeclaresItsPermissionVocabulary checks that the descriptor declares the two permissions and their translation.
func TestTheModuleDeclaresItsPermissionVocabulary(t *testing.T) {
	var spec authz.PermissionSpec
	var translation authz.TranslationSpec
	var seenSpec, seenTranslation bool
	for _, res := range Module().Resources {
		switch v := res.(type) {
		case authz.PermissionSpec:
			spec, seenSpec = v, true
		case authz.TranslationSpec:
			translation, seenTranslation = v, true
		}
	}
	if !seenSpec || !seenTranslation {
		t.Fatalf("resources carry spec=%v translation=%v, want both", seenSpec, seenTranslation)
	}

	declared := map[authz.Permission]bool{}
	for _, p := range spec.Permissions {
		declared[p] = true
	}
	for _, p := range []authz.Permission{PermissionTemplateRead, PermissionTemplateManage} {
		if !declared[p] {
			t.Errorf("the permission %q is not declared", p)
		}
		if _, ok := translation.Rows[p]; !ok {
			t.Errorf("the permission %q has no translation row", p)
		}
	}
	if row := translation.Rows[PermissionTemplateRead]; row.Object != templateObject || row.Action != "read" {
		t.Errorf("template.read translates to %+v, want %q/read", row, templateObject)
	}
	if row := translation.Rows[PermissionTemplateManage]; row.Object != templateObject || row.Action != "manage" {
		t.Errorf("template.manage translates to %+v, want %q/manage", row, templateObject)
	}
}

// TestTheDeclaredRowsReachTheProcessTranslation checks that the rows reach the process
// translation without colliding with the authorization capability's own.
func TestTheDeclaredRowsReachTheProcessTranslation(t *testing.T) {
	body := `{"sms": {"admin": {"endpoints": ["public"]}}, "authz": {"guard": {"endpoints": ["public"]}}}`
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	catalog := authztest.NewCatalog(PermissionTemplateRead, PermissionTemplateManage)

	guardModule := guard.Module()
	adminModule := Module()
	reader := authntest.ConfigReaderAll(t, body, guardModule, adminModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(guardModule)
	reg.Register(adminModule)
	names := []string{guard.ModuleName, ModuleName}
	for _, s := range []struct {
		name     string
		instance any
		token    core.Token
	}{
		{"stand-in.enforcer", enforcer, (*authz.Enforcer)(nil)},
		{"stand-in.catalog", catalog, (*authz.Catalog)(nil)},
		{"stand-in.registry", &stubRegistry{}, (*sms.Registry)(nil)},
		{"stand-in.provider", &stubProvider{name: "stub-carrier"}, (*sms.Provider)(nil)},
		{"stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)},
		{"stand-in.router", newStubRouter("public"), (*speedhttp.Router)(nil)},
		{"stand-in.logger", loggerStub{logger: (&logBuffer{}).logger()}, (*log.Logger)(nil)},
	} {
		reg.Register(standIn(s.name, s.instance, s.token))
		names = append(names, s.name)
	}
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart, Participants: names}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	if object, action, ok := catalog.ObjectAction(PermissionTemplateRead); !ok || object != templateObject || action != "read" {
		t.Errorf("template.read resolves to %q/%q ok=%v after the drive, want %q/read", object, action, ok, templateObject)
	}
	if object, action, ok := catalog.ObjectAction(PermissionTemplateManage); !ok || object != templateObject || action != "manage" {
		t.Errorf("template.manage resolves to %q/%q ok=%v after the drive, want %q/manage", object, action, ok, templateObject)
	}
}
