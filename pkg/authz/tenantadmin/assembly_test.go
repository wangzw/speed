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
)

const tenantBody = `{"authz": {"tenantadmin": {"endpoints": ["api"]}}}`

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

type tenantAssembly struct {
	body                                                                            string
	noManager, noCatalog, noSubject, noMembership, noGuard, noAudit, noVerification bool
	manager                                                                         authz.Manager
	catalog                                                                         authz.Catalog
	subject                                                                         authz.SubjectPermissions
	membership                                                                      authz.MembershipChecker
	verification                                                                    authn.SignupVerification
	noRouter                                                                        bool
	router                                                                          speedhttp.Router
	routeGuard                                                                      speedhttp.TenantAdminRouteGuard
	noRouteGuard                                                                    bool
	gate                                                                            authz.Gate
	writeGuard                                                                      authz.WriteGuard
	logger                                                                          *slog.Logger
	record                                                                          *any
}

func (a tenantAssembly) drive(t *testing.T, depth core.Stage, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = tenantBody
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

	var manager authz.Manager
	if !a.noManager {
		manager = a.manager
		if manager == nil {
			manager = &stubManager{}
		}
		reg.Register(standIn("stand-in.manager", manager, (*authz.Manager)(nil)))
		names = append(names, "stand-in.manager")
	}
	var catalog authz.Catalog
	if !a.noCatalog {
		catalog = a.catalog
		if catalog == nil {
			catalog = newStubCatalog(authz.PermissionTenantRoleRead)
		}
		reg.Register(standIn("stand-in.catalog", catalog, (*authz.Catalog)(nil)))
		names = append(names, "stand-in.catalog")
	}
	if stub, ok := manager.(*stubManager); ok {
		stub.catalog = catalog
	}
	if !a.noSubject {
		s := a.subject
		if s == nil {
			s = &stubSubjectPermissions{}
		}
		reg.Register(standIn("stand-in.subject", s, (*authz.SubjectPermissions)(nil)))
		names = append(names, "stand-in.subject")
	}
	if !a.noMembership {
		m := a.membership
		if m == nil {
			m = &stubMembership{member: true}
		}
		reg.Register(standIn("stand-in.membership", m, (*authz.MembershipChecker)(nil)))
		names = append(names, "stand-in.membership")
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
	logger := a.logger
	if logger == nil {
		logger = (&logBuffer{}).logger()
	}
	reg.Register(standIn("stand-in.logger", loggerStub{logger: logger}, (*log.Logger)(nil)))
	names = append(names, "stand-in.logger")
	if !a.noRouter {
		router := a.router
		if router == nil {
			router = newStubRouter("api")
		}
		reg.Register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if !a.noRouteGuard {
		routeGuard := a.routeGuard
		if routeGuard == nil {
			routeGuard = &stubRouteGuard{}
		}
		reg.Register(standIn("stand-in.route-guard", routeGuard, (*speedhttp.TenantAdminRouteGuard)(nil)))
		names = append(names, "stand-in.route-guard")
	}
	if a.gate != nil {
		reg.Register(standIn("stand-in.gate", a.gate, (*authz.Gate)(nil)))
		names = append(names, "stand-in.gate")
	}
	if a.writeGuard != nil {
		reg.Register(standIn("stand-in.write-guard", a.writeGuard, (*authz.WriteGuard)(nil)))
		names = append(names, "stand-in.write-guard")
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

// TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem checks that the assembly takes up its dependencies at the stage that reads them.
func TestTheAssemblyTakesUpItsDependenciesAtTheStageThatReadsThem(t *testing.T) {
	manager := &stubManager{}
	catalog := newStubCatalog(authz.PermissionTenantRoleRead)
	subject := &stubSubjectPermissions{}
	membership := &stubMembership{member: true}
	gate := authztest.NewGate()
	logger := (&logBuffer{}).logger()

	t.Run("New takes the policy capabilities up", func(t *testing.T) {
		var instance any
		err := tenantAssembly{
			manager: manager, catalog: catalog, subject: subject, membership: membership, record: &instance,
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
		if any(p.subject) != any(subject) {
			t.Errorf("New took up the permission reader %v, want the delivered %v", p.subject, subject)
		}
		if any(p.membership) != any(membership) {
			t.Errorf("New took up the membership checker %v, want the delivered %v", p.membership, membership)
		}
	})

	t.Run("Init takes the entry point, the path guard and the optional surfaces up", func(t *testing.T) {
		router := newStubRouter("api")
		routeGuard := &stubRouteGuard{}
		var instance any
		err := tenantAssembly{
			manager: manager, catalog: catalog, subject: subject, membership: membership,
			router: router, routeGuard: routeGuard, gate: gate, logger: logger, record: &instance,
		}.drive(t, core.StageInit, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.gate == nil || any(p.gate) != any(gate) {
			t.Errorf("Init took up the declaration surface %v, want the delivered %v", p.gate, gate)
		}
		if p.routeGuard == nil || any(p.routeGuard) != any(routeGuard) {
			t.Errorf("Init took up the path-tenant guard %v, want the delivered %v", p.routeGuard, routeGuard)
		}
		if p.logger != logger {
			t.Errorf("Init took up logger %v, want the delivered %v", p.logger, logger)
		}
		if got := router.endpoint("api").registered(); len(got) == 0 {
			t.Error("Init mounted no route on the delivered entry point")
		}
		if got := routeGuard.wrapCount(); got == 0 {
			t.Error("the path-tenant guard wrapped no handler, want every route")
		}
	})

	t.Run("the optional surfaces are not taken up before Init", func(t *testing.T) {
		var instance any
		err := tenantAssembly{
			manager: manager, catalog: catalog, subject: subject, membership: membership,
			gate: gate, logger: logger, record: &instance,
		}.drive(t, core.StageNew, nil)
		if err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.gate != nil {
			t.Error("New took up the declaration surface: it is taken up at Init")
		}
		if p.logger == logger {
			t.Error("New took up the delivered logger: the logging capability is taken up at Init")
		}
	})
}

// TestAMissingRequiredCapabilityFailsResolution checks that a missing required capability fails resolution.
func TestAMissingRequiredCapabilityFailsResolution(t *testing.T) {
	for name, missing := range map[string]tenantAssembly{
		"no manager":      {noManager: true},
		"no catalog":      {noCatalog: true},
		"no subject":      {noSubject: true},
		"no membership":   {noMembership: true},
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

// TestNoEntryPointIsErrNoRouter checks that no entry point fails with ErrNoRouter.
func TestNoEntryPointIsErrNoRouter(t *testing.T) {
	err := tenantAssembly{noRouter: true}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrNoRouter) {
		t.Fatalf("the drive ended with %v, want authz.ErrNoRouter", err)
	}
	if err == nil || !strings.Contains(err.Error(), ModuleName) {
		t.Fatalf("the failure does not name the module: %v", err)
	}
}

// TestAnUnknownEndpointIsErrUnknownEndpoint checks that an unknown endpoint fails with ErrUnknownEndpoint.
func TestAnUnknownEndpointIsErrUnknownEndpoint(t *testing.T) {
	body := `{"authz": {"tenantadmin": {"endpoints": ["api", "ghost"]}}}`
	err := tenantAssembly{body: body}.drive(t, core.StageInit, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive ended with %v, want authz.ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// TestAMissingPathTenantGuardRefusesTheMount checks that a missing path tenant guard refuses the mount.
func TestAMissingPathTenantGuardRefusesTheMount(t *testing.T) {
	err := tenantAssembly{noRouteGuard: true}.drive(t, core.StageInit, nil)
	if err == nil {
		t.Fatal("the drive mounted the tenant-scoped routes without a path-tenant guard")
	}
	if !strings.Contains(err.Error(), ModuleName) || !strings.Contains(err.Error(), "tenant administration guard") {
		t.Fatalf("the failure does not name the module and the missing guard: %v", err)
	}
}

// TestARealRegistryMountsEveryRouteAndDeliversNothing checks that a real registry mounts every route and delivers nothing.
func TestARealRegistryMountsEveryRouteAndDeliversNothing(t *testing.T) {
	router := newStubRouter("api")
	gate := authztest.NewGate()
	if err := (tenantAssembly{router: router, gate: gate}).drive(t, core.StageStart, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if provides := Module().Provides; len(provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", provides)
	}

	want := map[mountedRoute]bool{
		{http.MethodGet, "/api/v1/tenants/:tenant/permissions"}:                       true,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles"}:                             true,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles/:role/bindings"}:              true,
		{http.MethodPut, "/api/v1/tenants/:tenant/roles/:role"}:                       true,
		{http.MethodDelete, "/api/v1/tenants/:tenant/roles/:role"}:                    true,
		{http.MethodGet, "/api/v1/tenants/:tenant/roles/:role/grants"}:                true,
		{http.MethodPut, "/api/v1/tenants/:tenant/roles/:role/grants/:permission"}:    true,
		{http.MethodDelete, "/api/v1/tenants/:tenant/roles/:role/grants/:permission"}: true,
		{http.MethodGet, "/api/v1/tenants/:tenant/bindings"}:                          true,
		{http.MethodGet, "/api/v1/tenants/:tenant/bindings/:account"}:                 true,
		{http.MethodPut, "/api/v1/tenants/:tenant/bindings/:account/:role"}:           true,
		{http.MethodDelete, "/api/v1/tenants/:tenant/bindings/:account/:role"}:        true,
	}
	got := router.endpoint("api").registered()
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
