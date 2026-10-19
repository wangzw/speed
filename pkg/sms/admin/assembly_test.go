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

// The cases built on this file drive this module's descriptor through a real
// registry: the configuration module with a reader the real loader produced,
// this module itself, and stand-ins for the required capabilities and for the
// entry point it takes up in Init. What a product built by hand cannot show is
// which callback takes up which dependency, what a single missing capability
// does at resolution, and what the descriptor says of its own delivery, which is
// none.

// managementBody is the configuration a default drive runs on: one endpoint of
// the entry point, "public", that the management routes are mounted on.
const managementBody = `{"sms": {"admin": {"endpoints": ["public"]}}}`

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
	// noRegistry, noGuard and noAudit leave out the stand-in for the required
	// capability named, so the drive reaches resolution without it.
	noRegistry, noGuard, noAudit bool
	// noRouter leaves the entry point out, which fails Init.
	noRouter bool
	// registry is the registry stand-in delivered as that capability.
	registry sms.Registry
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

	if !a.noRegistry {
		r := a.registry
		if r == nil {
			r = &stubRegistry{}
		}
		reg.Register(standIn("stand-in.registry", r, (*sms.Registry)(nil)))
		names = append(names, "stand-in.registry")
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
// command that brings this one in without the registry, the guard or the audit
// capability fails resolution with ErrMissingProvider, before New builds
// anything.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]managementAssembly{
		"no registry": {noRegistry: true},
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
	body := `{"sms": {"admin": {"endpoints": ["public", "ghost"]}}}`
	err := managementAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// New takes the registry, the guard and the audit capability up, and Init takes
// the entry point and the optional route-declaration surface.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	registry := &stubRegistry{}
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the required capabilities up", func(t *testing.T) {
		var instance any
		err := managementAssembly{registry: registry, record: &instance}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if any(p.registry) != any(registry) {
			t.Errorf("New took up registry %v, want the delivered %v", p.registry, registry)
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
		{http.MethodGet, "/api/v1/system/sms/templates"}:                true,
		{http.MethodPost, "/api/v1/system/sms/templates"}:               true,
		{http.MethodPost, "/api/v1/system/sms/templates/submissions"}:   true,
		{http.MethodPost, "/api/v1/system/sms/templates/:name/enabled"}: true,
		{http.MethodDelete, "/api/v1/system/sms/templates/:name"}:       true,
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

// The endpoint list refuses a shape the module cannot run on, naming the key:
// an empty list mounts the routes nowhere, and the name that stands for every
// endpoint is refused because it would put them on the public endpoints.
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

// The descriptor declares the two permissions and their translation as
// resources, so the process's catalog carries them and the process's translation
// expresses a decision on either. Without the translation the guard could not
// express the permission and every route would refuse its caller.
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

// Driving the real enforcement module alongside this one resolves the process
// translation with this module's rows and leaves the guard able to express both
// permissions: the rows reach the one set the whole process decides by, and they
// do not collide with the rows the authorization capability carries, which would
// fail the resolution outright.
func TestTheDeclaredRowsReachTheProcessTranslation(t *testing.T) {
	// The enforcement module's Init resolves the process translation from every
	// registered module's rows; driving it here is what proves this module's rows
	// reach that set and do not conflict with the rows the capability carries.
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
