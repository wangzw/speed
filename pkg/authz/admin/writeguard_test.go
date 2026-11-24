package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// writeRouteCase is one management write and the kind the guard must be told it
// is. Every write route is here, so a route that skips the seam or names the
// wrong kind is a case that fails rather than a route nobody covers.
type writeRouteCase struct {
	name   string
	op     authz.WriteOp
	route  func(*product) gin.HandlerFunc
	method string
	target string
	params gin.Params
	body   any
}

// writeRouteCases builds one write route of each kind. The grant routes need a
// permission the catalog holds and the translation has a row for.
func writeRouteCases() []writeRouteCase {
	const (
		rolePutTarget    = "/api/v1/system/tenants/t1/roles/r1"
		grantPutTarget   = "/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read"
		bindingPutTarget = "/api/v1/system/tenants/t1/bindings/acct-1/editor"
	)
	return []writeRouteCase{
		{
			name: "a role write", op: authz.WriteRole,
			route:  func(p *product) gin.HandlerFunc { return rolePutRoute(p) },
			method: http.MethodPut, target: rolePutTarget,
			params: params("tenant", "t1", "role", "r1"), body: roleBody{Description: "Runs it."},
		},
		{
			name: "a role delete", op: authz.WriteRole,
			route:  func(p *product) gin.HandlerFunc { return writeRoute(p, roleDeleteOperation.Handle(p.handleRoleDelete)) },
			method: http.MethodDelete, target: rolePutTarget,
			params: params("tenant", "t1", "role", "r1"),
		},
		{
			name: "a grant write", op: authz.WriteGrant,
			route:  func(p *product) gin.HandlerFunc { return writeRoute(p, grantPutOperation.Handle(p.handleGrantPut)) },
			method: http.MethodPut, target: grantPutTarget,
			params: params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)),
		},
		{
			name: "a grant delete", op: authz.WriteGrant,
			route: func(p *product) gin.HandlerFunc {
				return writeRoute(p, grantDeleteOperation.Handle(p.handleGrantDelete))
			},
			method: http.MethodDelete, target: grantPutTarget,
			params: params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)),
		},
		{
			name: "a binding write", op: authz.WriteBinding,
			route:  func(p *product) gin.HandlerFunc { return writeRoute(p, bindingPutOperation.Handle(p.handleBindingPut)) },
			method: http.MethodPut, target: bindingPutTarget,
			params: params("tenant", "t1", "account", "acct-1", "role", "editor"),
		},
		{
			name: "a binding delete", op: authz.WriteBinding,
			route: func(p *product) gin.HandlerFunc {
				return writeRoute(p, bindingDeleteOperation.Handle(p.handleBindingDelete))
			},
			method: http.MethodDelete, target: bindingPutTarget,
			params: params("tenant", "t1", "account", "acct-1", "role", "editor"),
		},
	}
}

// TestEveryWriteConsultsTheWriteGuard holds the seam's range and its vocabulary:
// each role, grant and binding write asks the guard once about the tenant the
// path names, with the kind the route writes, and a permit lets the write reach
// the manager.
func TestEveryWriteConsultsTheWriteGuard(t *testing.T) {
	for _, c := range writeRouteCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, authz.PermissionAccountRead)
			w := r.serve(c.route(r.p), c.method, c.target, c.params, c.body)
			if w.Code != http.StatusNoContent {
				t.Fatalf("the write answered %d, want 204", w.Code)
			}
			asked := r.writeGuard.asked()
			if len(asked) != 1 {
				t.Fatalf("the write guard was asked %+v, want one question", asked)
			}
			if asked[0].tenant != "t1" || asked[0].op != c.op {
				t.Fatalf("the write guard was asked %+v, want tenant t1 and op %v", asked[0], c.op)
			}
			if got := r.manager.recorded(); len(got) != 1 {
				t.Fatalf("the manager calls = %+v, want the permitted write", got)
			}
		})
	}
}

// TestADeniedWriteIsRefusedBeforeTheAuditAndTheManager holds the deny direction:
// the route answers 403 with the capability's protected type and neither lands a
// mandatory audit row nor reaches the manager.
func TestADeniedWriteIsRefusedBeforeTheAuditAndTheManager(t *testing.T) {
	for _, c := range writeRouteCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, authz.PermissionAccountRead)
			r.writeGuard.refuse()
			w := r.serve(c.route(r.p), c.method, c.target, c.params, c.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("a denied write answered %d, want 403", w.Code)
			}
			if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
				t.Fatalf("the refusal's type = %q, want urn:speed:authz:protected", p.Type)
			}
			if got := r.audit.entries(); len(got) != 0 {
				t.Fatalf("the denied write landed audit rows %+v, want none", got)
			}
			if got := r.manager.recorded(); len(got) != 0 {
				t.Fatalf("the denied write reached the manager %+v, want no call", got)
			}
		})
	}
}

// TestAWriteGuardThatCannotAnswerIsA503 holds the failure direction: an answer
// that cannot be read is never a permit, and it is a 503 rather than a denial.
func TestAWriteGuardThatCannotAnswerIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.writeGuard.fail(errors.New("the guard's store is down"))
	w := r.serve(writeRouteCases()[0].route(r.p), http.MethodPut,
		"/api/v1/system/tenants/t1/roles/r1", params("tenant", "t1", "role", "r1"), roleBody{Description: "Runs it."})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a write guard that could not answer answered %d, want 503", w.Code)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("an unanswerable guard still landed audit rows %+v, want none", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("an unanswerable guard still reached the manager %+v, want no call", got)
	}
}

// TestReadsDoNotConsultTheWriteGuard holds the seam's direction: it protects
// writes, so a read is never asked about.
func TestReadsDoNotConsultTheWriteGuard(t *testing.T) {
	r := newRig(t)
	reads := []struct {
		name   string
		route  gin.HandlerFunc
		target string
		params gin.Params
	}{
		{"the catalog", readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), "/api/v1/system/permissions", nil},
		{"the roles", readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), "/api/v1/system/tenants/t1/roles", params("tenant", "t1")},
		{"the grants", readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), "/api/v1/system/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1")},
		{"the bindings", readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), "/api/v1/system/tenants/t1/bindings", params("tenant", "t1")},
		{"the account's bindings", readRoute(r.p, bindingOfOperation.Handle(r.p.handleBindingOf)), "/api/v1/system/tenants/t1/bindings/acct-1", params("tenant", "t1", "account", "acct-1")},
	}
	for _, c := range reads {
		t.Run(c.name, func(t *testing.T) {
			if w := r.serve(c.route, http.MethodGet, c.target, c.params, nil); w.Code != http.StatusOK {
				t.Fatalf("the read answered %d, want 200", w.Code)
			}
		})
	}
	if got := r.writeGuard.asked(); len(got) != 0 {
		t.Fatalf("a read asked the write guard %+v, want no question", got)
	}
}

// TestTheWriteGuardIsOptional holds the seam's absence: an assembly that
// delivers none writes as the module did before the seam existed.
func TestTheWriteGuardIsOptional(t *testing.T) {
	r := newRig(t)
	r.p.writeGuard = nil
	w := r.serve(writeRouteCases()[0].route(r.p), http.MethodPut,
		"/api/v1/system/tenants/t1/roles/r1", params("tenant", "t1", "role", "r1"), roleBody{Description: "Runs it."})
	if w.Code != http.StatusNoContent {
		t.Fatalf("a write with no guard assembled answered %d, want 204", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 1 {
		t.Fatalf("the manager calls = %+v, want the write to land", got)
	}
}

// TestTheWriteGuardIsTakenUpFromTheAssembly holds the wiring: a guard the
// assembly delivers is what Init puts on the product, and an assembly without
// one leaves the seam absent rather than failing the startup.
func TestTheWriteGuardIsTakenUpFromTheAssembly(t *testing.T) {
	t.Run("a delivered guard is taken up", func(t *testing.T) {
		guard := &stubWriteGuard{}
		var instance any
		if err := (managementAssembly{writeGuard: guard, record: &instance}).drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.writeGuard == nil || any(p.writeGuard) != any(guard) {
			t.Fatalf("Init took up write guard %v, want the delivered %v", p.writeGuard, guard)
		}
	})

	t.Run("an absent guard is legal", func(t *testing.T) {
		var instance any
		if err := (managementAssembly{record: &instance}).drive(t, core.StageInit, nil); err != nil {
			t.Fatalf("the drive failed without a write guard: %v", err)
		}
		p := productOfAssembly(t, instance)
		if p.writeGuard != nil {
			t.Fatalf("Init took up write guard %v from an assembly that delivered none", p.writeGuard)
		}
	})
}

// TestTheWriteGuardIsAskedAfterTheShapeChecks holds the ordering the design
// states: a request the route refuses on shape never reaches the seam.
func TestTheWriteGuardIsAskedAfterTheShapeChecks(t *testing.T) {
	t.Run("an undeclared grant permission", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a grant on an undeclared permission answered %d, want 400", w.Code)
		}
		if got := r.writeGuard.asked(); len(got) != 0 {
			t.Fatalf("the shape refusal asked the write guard %+v, want no question", got)
		}
	})

	t.Run("a binding with no account", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings//editor",
			params("tenant", "t1", "account", "", "role", "editor"), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a binding with no account answered %d, want 400", w.Code)
		}
		if got := r.writeGuard.asked(); len(got) != 0 {
			t.Fatalf("the shape refusal asked the write guard %+v, want no question", got)
		}
	})
}
