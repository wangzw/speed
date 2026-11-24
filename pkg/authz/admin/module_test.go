package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
)

// TestTheDescriptorNamesWhatItRequiresAndDelivers holds the descriptor's shape:
// the required capabilities, the logger as the one optional one, and that this
// module delivers no capability of its own.
func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authz.admin" {
		t.Errorf("module name = %q (constant %q), want authz.admin", m.Name, ModuleName)
	}
	if len(m.Provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", m.Provides)
	}
	wantRequired := []core.Token{
		(*authz.Manager)(nil), (*authz.Catalog)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil),
		(*authn.SignupVerification)(nil), (*authz.SubjectPermissions)(nil),
	}
	var required []core.Token
	for _, r := range m.Requires {
		if !r.Optional {
			required = append(required, r.Token)
		}
	}
	if len(required) != len(wantRequired) {
		t.Fatalf("Requires = %+v, want the required capabilities %v", m.Requires, wantRequired)
	}
	for i, tk := range wantRequired {
		if required[i] != tk {
			t.Errorf("Required[%d] = %v, want %v", i, required[i], tk)
		}
	}
	if len(m.Requires) != len(wantRequired)+1 || !m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the logger as the one optional dependency", m.Requires)
	}
}

// TestPrepareStatesEnabled holds the stance.
func TestPrepareStatesEnabled(t *testing.T) {
	module := Module()
	reader := authntest.ConfigReader(t, module, `{"authz": {"admin": {"endpoints": ["public"]}}}`)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	got, err := module.Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds the init.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	if _, ok := core.ProcessRegistry.Lookup(ModuleName); !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
}

// TestEndpointsAreRequiredAndRefuseAsterisk holds the endpoint list's shape: an
// absent or empty list is refused, and the wildcard that stands for every
// endpoint is refused.
func TestEndpointsAreRequiredAndRefuseAsterisk(t *testing.T) {
	t.Run("an empty list is refused", func(t *testing.T) {
		if _, err := (moduleConfig{}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("resolve of an empty list = %v, want authn.ErrInvalidConfig", err)
		}
	})
	t.Run("an absent item is refused by a real reader", func(t *testing.T) {
		// The item is declared required, so the loader refuses a body that gives
		// it no value before this module's own check runs; either way the read
		// is refused.
		reader := authntest.ConfigReader(t, Module(), `{}`)
		if _, err := readConfig(reader); err == nil {
			t.Fatal("reading a body with no endpoints was accepted, want a refusal")
		}
	})
	t.Run("the wildcard is refused", func(t *testing.T) {
		if _, err := (moduleConfig{Endpoints: []string{"*"}}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("resolve of the wildcard = %v, want authn.ErrInvalidConfig", err)
		}
	})
	t.Run("a name written twice is refused", func(t *testing.T) {
		if _, err := (moduleConfig{Endpoints: []string{"public", "public"}}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("resolve of a doubled name = %v, want authn.ErrInvalidConfig", err)
		}
	})
	t.Run("an empty name inside the list is refused", func(t *testing.T) {
		if _, err := (moduleConfig{Endpoints: []string{"public", ""}}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("resolve of a list carrying an empty name = %v, want authn.ErrInvalidConfig", err)
		}
	})
}

// TestAnUnknownAdminEndpointFailsInit holds the start-time check: an endpoint
// name the entry point does not declare stops the startup and the failure names
// it.
func TestAnUnknownAdminEndpointFailsInit(t *testing.T) {
	router := newStubRouter("public")
	err := assembly{
		body:   `{"authz": {"admin": {"endpoints": ["ghost"]}}}`,
		router: router,
	}.drive(t, nil)
	if !errors.Is(err, authz.ErrUnknownEndpoint) {
		t.Fatalf("the drive = %v, want ErrUnknownEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("the failure does not name the endpoint: %v", err)
	}
}

// TestARealRouterMountsEveryManagementRoute holds the descriptor end to end: the
// module mounts its closed set of routes on the configured endpoint and declares
// each of them open on the route-declaration surface.
func TestARealRouterMountsEveryManagementRoute(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	err := assembly{router: router, gate: gate}.drive(t, nil)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
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

// assembly drives the module's descriptor through a real registry with a stub
// router and stand-ins for the required capabilities. It is how the descriptor's
// wiring — what Init takes up, which endpoints it mounts on, what a name the
// entry point does not declare fails with — is observed.
type assembly struct {
	// body is the configuration, JSON; the default declares one endpoint of the
	// entry point, "public".
	body string
	// router is supplied as the entry point; a nil one leaves the capability
	// out, which must fail Init.
	router speedhttp.Router
	// gate is supplied as the route-declaration surface when set.
	gate authz.Gate
}

func (a assembly) drive(t *testing.T, action func(*core.Registry)) error {
	t.Helper()
	body := a.body
	if body == "" {
		body = `{"authz": {"admin": {"endpoints": ["public"]}}}`
	}
	module := Module()
	reader := authntest.ConfigReader(t, module, body)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{ModuleName}
	reg.Register(module)
	reg.Register(standIn("stand-in.manager", &stubManager{}, (*authz.Manager)(nil)))
	reg.Register(standIn("stand-in.catalog", newStubCatalog(), (*authz.Catalog)(nil)))
	reg.Register(standIn("stand-in.guard", &stubGuard{allow: true}, (*guard.Guard)(nil)))
	reg.Register(standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)))
	reg.Register(standIn("stand-in.verification", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)))
	reg.Register(standIn("stand-in.subject", &stubSubjectPermissions{}, (*authz.SubjectPermissions)(nil)))
	logs := &logBuffer{}
	reg.Register(standIn("stand-in.logger", loggerStub{logger: logs.logger()}, (*log.Logger)(nil)))
	names = append(names, "stand-in.manager", "stand-in.catalog", "stand-in.guard", "stand-in.audit",
		"stand-in.verification", "stand-in.subject", "stand-in.logger")
	if a.router != nil {
		reg.Register(standIn("stand-in.router", a.router, (*speedhttp.Router)(nil)))
		names = append(names, "stand-in.router")
	}
	if a.gate != nil {
		reg.Register(standIn("stand-in.gate", a.gate, (*authz.Gate)(nil)))
		names = append(names, "stand-in.gate")
	}
	if action == nil {
		action = func(*core.Registry) {}
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       func(_ context.Context, reg *core.Registry) error { action(reg); return nil },
	})
}

// TestThePermissionTheRouteAsksFor holds that every route asks the handler for
// the permission its method implies: a read asks for role.read and a write for
// role.manage.
func TestThePermissionTheRouteAsksFor(t *testing.T) {
	t.Run("a read asks for role.read", func(t *testing.T) {
		r := newRig(t)
		r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet, "/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
		if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
			t.Fatalf("the read asked for %v, want [authz.role.read]", got)
		}
	})
	t.Run("a write asks for role.manage", func(t *testing.T) {
		r := newRig(t)
		r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
			params("tenant", "t1", "role", "r1"), roleBody{})
		if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
			t.Fatalf("the write asked for %v, want [authz.role.manage]", got)
		}
	})
}

// TestTheDecisionIsAskedInThePlatformDomain holds the domain the handler
// asks the guard to decide in: the platform domain, whatever tenant the path
// names.
func TestTheDecisionIsAskedInThePlatformDomain(t *testing.T) {
	r := newRig(t)
	r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet, "/api/v1/system/tenants/other/roles", params("tenant", "other"), nil)
	if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != authz.PlatformDomain {
		t.Fatalf("the read decided in %v, want [system]: the platform domain and not the path tenant", got)
	}
}

// TestADeniedCallerIsRefusedBeforeTheManager holds that a decision shortfall
// ends the handler before anything else: the manager is never called.
func TestADeniedCallerIsRefusedBeforeTheManager(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet, "/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a denied read answered %d, want 403", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on a denied request", got)
	}
}

// TestAnAnonymousCallerGetsA401 holds that a request with no identity is refused
// as a 401 before the guard is asked and before the manager is called, and that
// the answer is the shape the admission and authorization layers write for the
// same condition: no problem type and the realm challenge.
func TestAnAnonymousCallerGetsA401(t *testing.T) {
	r := newRig(t)
	w := r.serveAnonymous(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
		params("tenant", "t1", "role", "r1"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous write answered %d, want 401", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 0 {
		t.Fatalf("the guard was asked %v for an anonymous request", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for an anonymous request", got)
	}
	if p := decodeProblem(t, w.Body.String()); strings.HasPrefix(p.Type, "urn:speed:authz:") {
		t.Fatalf("the 401's type = %q, want none of this capability's codes", p.Type)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != realmChallenge("public") {
		t.Fatalf("WWW-Authenticate = %q, want the realm challenge %q", got, realmChallenge("public"))
	}
}

// TestAGuardOutageIsA503 holds that an outage from the guard, which is not a
// denial, is answered as the guard wrote it and nothing else runs.
func TestAGuardOutageIsA503(t *testing.T) {
	r := newRig(t)
	r.guard.err = errors.New("the engine is unavailable")
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
		params("tenant", "t1", "role", "r1"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a guard outage answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on a guard outage", got)
	}
}

// TestTheMandatoryAuditLandsBeforeTheWrite holds the write path's order: the
// mandatory row lands before the manager is called, and a row that does not land
// refuses the write with a 503.
func TestTheMandatoryAuditLandsBeforeTheWrite(t *testing.T) {
	t.Run("a landing row precedes the write", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
			params("tenant", "t1", "role", "r1"), roleBody{})
		if w.Code != http.StatusNoContent {
			t.Fatalf("a permitted write answered %d, want 204", w.Code)
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionRolePut || rows[0].Outcome != outcomeSuccess {
			t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionRolePut)
		}
		if got := r.manager.recorded(); len(got) != 1 || got[0].op != "PutRole" {
			t.Fatalf("the manager calls = %+v, want one PutRole", got)
		}
	})
	t.Run("a row that does not land refuses the write", func(t *testing.T) {
		r := newRig(t)
		r.audit.fail(errors.New("the audit store is down"))
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
			params("tenant", "t1", "role", "r1"), roleBody{})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a write whose audit did not land answered %d, want 503", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
		}
	})
}

// TestAFailedWriteLeavesAFailedRow holds that a write the manager refuses is
// closed with a failed audit row linked to the permitted one by the action
// identifier.
func TestAFailedWriteLeavesAFailedRow(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrBuiltinRole)
	w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/system/tenants/t1/roles/r1",
		params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("deleting a builtin role answered %d, want 409", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want a permitted row and a failed one", rows)
	}
	if rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the audit outcomes = %q, %q, want success then failed", rows[0].Outcome, rows[1].Outcome)
	}
	if rows[0].Detail[detailActionID] == "" || rows[0].Detail[detailActionID] != rows[1].Detail[detailActionID] {
		t.Fatalf("the rows are not linked: %q vs %q", rows[0].Detail[detailActionID], rows[1].Detail[detailActionID])
	}
}

// TestTheFailureToAnswerMapping holds the closed mapping from a manager failure
// to its answer, including the problem type each refusal carries.
func TestTheFailureToAnswerMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		want     int
		wantType string
	}{
		"an unknown role is a 400":               {authz.ErrUnknownRole, http.StatusBadRequest, "urn:speed:authz:bad-request"},
		"a tenant mismatch is a 400":             {authz.ErrTenantMismatch, http.StatusBadRequest, "urn:speed:authz:bad-request"},
		"a refused platform membership is a 400": {authz.ErrPlatformMembershipRequired, http.StatusBadRequest, "urn:speed:authz:bad-request"},
		"a missing membership checker is a 503":  {authz.ErrMissingMembershipChecker, http.StatusServiceUnavailable, ""},
		"a builtin role is a 409":                {authz.ErrBuiltinRole, http.StatusConflict, "urn:speed:authz:conflict"},
		"the last tenant administrator is a 409": {authz.ErrLastTenantAdmin, http.StatusConflict, "urn:speed:authz:conflict"},
		"any other failure is a 503":             {errors.New("the store is down"), http.StatusServiceUnavailable, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.manager.fail(tc.err)
			w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/r1",
				params("tenant", "t1", "role", "r1"), roleBody{})
			if w.Code != tc.want {
				t.Fatalf("the answer = %d, want %d", w.Code, tc.want)
			}
			if tc.wantType == "" {
				return
			}
			if p := decodeProblem(t, w.Body.String()); p.Type != tc.wantType {
				t.Fatalf("the answer's type = %q, want %q", p.Type, tc.wantType)
			}
		})
	}
}

// TestAGrantOnAnUnknownPermissionIsRefusedBeforeTheManager holds the grant
// route's own check: a permission the catalog does not hold is a 400 and the
// manager is not called.
func TestAGrantOnAnUnknownPermissionIsRefusedBeforeTheManager(t *testing.T) {
	t.Run("an undeclared permission", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", "authn.account.read"), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("granting an undeclared permission answered %d, want 400", w.Code)
		}
		if got := r.catalog.asked(); len(got) != 1 || got[0] != authz.PermissionAccountRead {
			t.Fatalf("the catalog was asked %v, want [authn.account.read]", got)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v for an undeclared permission", got)
		}
	})
	t.Run("a declared permission is translated and written", func(t *testing.T) {
		r := newRig(t, authz.PermissionAccountRead)
		w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", "authn.account.read"), nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("granting a declared permission answered %d, want 204", w.Code)
		}
		got := r.manager.recorded()
		if len(got) != 1 || got[0].op != "PutGrant" {
			t.Fatalf("the manager calls = %+v, want one PutGrant", got)
		}
		if got[0].grant.Resource != "account" || got[0].grant.Action != "read" {
			t.Fatalf("the grant = %+v, want the pair (account, read)", got[0].grant)
		}
	})
}

// TestABindingCarriesTheNaturalKeys holds that the binding route passes the
// tenant, the account and the role from the path to the manager and lands the
// mandatory row first.
func TestABindingCarriesTheNaturalKeys(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("binding a role answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutBinding" {
		t.Fatalf("the manager calls = %+v, want one PutBinding", got)
	}
	if got[0].binding.Tenant != "t1" || got[0].binding.Account != "acct-1" || string(got[0].binding.Role) != "editor" {
		t.Fatalf("the binding = %+v, want tenant t1, account acct-1, role editor", got[0].binding)
	}
}

// TestTheCatalogRouteShowsTheDeclaredPermissions holds that the permission route
// answers the catalog's declared permissions as views.
func TestTheCatalogRouteShowsTheDeclaredPermissions(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead, authz.PermissionRoleManage)
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet, "/api/v1/system/permissions", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the catalog route answered %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, perm := range []string{string(authz.PermissionRoleRead), string(authz.PermissionRoleManage)} {
		if !strings.Contains(body, perm) {
			t.Errorf("the answer %q does not carry the permission %q", body, perm)
		}
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the catalog route asked for %v, want [authz.role.read]", got)
	}
}

// TestTheManagementRoutesServeOnTheRealEntryPoint checks that the management
// routes answer through the real entry point.
func TestTheManagementRoutesServeOnTheRealEntryPoint(t *testing.T) {
	logs := newCapturedLogs()
	body := `{
		"http": {"listeners": {"public": {"address": "127.0.0.1:0"}}, "endpoints": {"public": {"listener": "public"}}},
		"authz": {"admin": {"endpoints": ["public"]}}
	}`
	module := Module()
	entry := speedhttp.Module()
	reader := authntest.ConfigReaderAll(t, body, module, entry)

	served := make(chan struct{})
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(entry)
	reg.Register(module)
	reg.Register(standIn("stand-in.manager", &stubManager{}, (*authz.Manager)(nil)))
	reg.Register(standIn("stand-in.catalog", newStubCatalog(authz.PermissionRoleRead), (*authz.Catalog)(nil)))
	reg.Register(standIn("stand-in.guard", &stubGuard{allow: true}, (*guard.Guard)(nil)))
	reg.Register(standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)))
	reg.Register(standIn("stand-in.verification", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)))
	reg.Register(standIn("stand-in.subject", &stubSubjectPermissions{}, (*authz.SubjectPermissions)(nil)))
	reg.Register(standIn("stand-in.logger", loggerStub{logger: slog.New(logs)}, (*log.Logger)(nil)))
	// The entry point's own Serve binds the address and writes the listening
	// record; this module sorts after it by name, so its Serve runs once the
	// address is bound and the channel is a deterministic signal.
	reg.Register(core.Module{
		Name:  "stand-in.serve",
		New:   func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Serve: func(context.Context, *core.Registry, any) error { close(served); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = reg.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Errorf("the assembly did not stop within %s", waitLimit)
		}
	})

	select {
	case <-served:
	case <-done:
		t.Fatalf("the assembly stopped before it served: %v", runErr)
	case <-time.After(waitLimit):
		t.Fatalf("the assembly did not reach Serve within %s", waitLimit)
	}

	address := logs.address("public")
	if address == "" {
		t.Fatal("the entry point reported no listening address")
	}
	mounted := statusOf(t, "http://"+address+"/api/v1/system/permissions")
	if mounted == http.StatusNotFound {
		t.Fatal("a mounted route answered 404: the entry point did not assemble it")
	}
	if mounted != http.StatusUnauthorized {
		t.Fatalf("a mounted route answered %d, want 401: the request carries no identity", mounted)
	}
	// The anonymous request reaches the handler through the real router, which
	// mounted it with the endpoint's name as the realm: the 401 is the shape the
	// admission and authorization layers write — no problem type, and the realm
	// challenge naming the endpoint.
	code, header, body := requestOf(t, "http://"+address+"/api/v1/system/permissions")
	if code != http.StatusUnauthorized {
		t.Fatalf("the anonymous request answered %d, want 401", code)
	}
	if got, want := header.Get("WWW-Authenticate"), realmChallenge("public"); got != want {
		t.Fatalf("the management 401's challenge = %q, want %q", got, want)
	}
	if p := decodeProblem(t, body); strings.HasPrefix(p.Type, "urn:speed:authz:") {
		t.Fatalf("the management 401's type = %q, want none of this capability's codes", p.Type)
	}
	if got := statusOf(t, "http://"+address+"/api/v1/system/nowhere"); got != http.StatusNotFound {
		t.Fatalf("an unmounted path answered %d, want the entry point's 404", got)
	}
}

// waitLimit bounds every wait, so a startup that never reaches Serve fails the
// case with a message and not the binary at the global timeout.
var waitLimit = coretest.Scale(60 * time.Second)

// statusOf sends a GET and returns the status code.
func statusOf(t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Timeout: waitLimit, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// requestOf sends a GET and returns the status, the headers and the body, so a
// case can hold the shape of an answer besides its status.
func requestOf(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	client := &http.Client{Timeout: waitLimit, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return resp.StatusCode, resp.Header, string(body)
}
