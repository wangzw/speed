package tenantadmin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// TestTheDescriptorNamesWhatItRequiresAndDelivers holds the descriptor's shape.
func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authz.tenantadmin" {
		t.Errorf("module name = %q (constant %q), want authz.tenantadmin", m.Name, ModuleName)
	}
	if len(m.Provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", m.Provides)
	}
	wantRequired := []core.Token{
		(*authz.Manager)(nil), (*authz.Catalog)(nil), (*authz.SubjectPermissions)(nil),
		(*authz.MembershipChecker)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil),
		(*authn.SignupVerification)(nil),
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

// TestTheDescriptorDoesNotTakeASeederOrAReconciler holds the wall the design
// states: the tenant management module never holds either seeding surface.
func TestTheDescriptorDoesNotTakeASeederOrAReconciler(t *testing.T) {
	for _, r := range Module().Requires {
		switch r.Token {
		case (*authz.Seeder)(nil), (*authz.Reconciler)(nil):
			t.Errorf("the descriptor requires %v: the management surface must not reach the seeding surfaces", r.Token)
		}
	}
}

// TestPrepareStatesEnabled holds the stance.
func TestPrepareStatesEnabled(t *testing.T) {
	module := Module()
	reader := authntest.ConfigReader(t, module, `{"authz": {"tenantadmin": {"endpoints": ["api"]}}}`)
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

// TestAGuardOutageIsA503 holds that an outage from the guard is answered as the
// guard wrote it and nothing else runs.
func TestAGuardOutageIsA503(t *testing.T) {
	r := newRig(t)
	r.guard.err = errors.New("the engine is unavailable")
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/r1",
		params("tenant", "t1", "role", "r1"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a guard outage answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on a guard outage", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("the audit recorded %v on a guard outage", got)
	}
}

// TestTheMandatoryAuditLandsBeforeTheWrite holds the write path's order.
func TestTheMandatoryAuditLandsBeforeTheWrite(t *testing.T) {
	t.Run("a landing row precedes the write", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/r1",
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
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/r1",
			params("tenant", "t1", "role", "r1"), roleBody{})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a write whose audit did not land answered %d, want 503", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
		}
	})
}

// TestTheFailureToAnswerMapping holds the closed mapping from a manager failure
// to its answer.
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
		"a reserved role is a 403 protected":     {authz.ErrReservedRole, http.StatusForbidden, "urn:speed:authz:protected"},
		"any other failure is a 503":             {errors.New("the store is down"), http.StatusServiceUnavailable, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.manager.fail(tc.err)
			w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/r1",
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

// TestAnAnonymousCallerGetsA401 holds the no-identity answer's shape.
func TestAnAnonymousCallerGetsA401(t *testing.T) {
	r := newRig(t)
	w := r.serveAnonymous(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/r1",
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
	if got := w.Header().Get("WWW-Authenticate"); got != realmChallenge("api") {
		t.Fatalf("WWW-Authenticate = %q, want the realm challenge %q", got, realmChallenge("api"))
	}
}

// TestTheCatalogRouteExposesNoOpaqueID holds that the catalog path carries no
// parameter segment beyond the tenant.
func TestTheCatalogRouteExposesNoOpaqueID(t *testing.T) {
	if pathPermissions != "/api/v1/tenants/:tenant/permissions" {
		t.Fatalf("the catalog path = %q, want the literal /api/v1/tenants/:tenant/permissions", pathPermissions)
	}
}
