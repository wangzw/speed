package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

// TestAnUnauthorisedBindingDeleteReachesNoManagerAndWritesNoAudit holds the
// binding delete route's decision: the guard is asked for role.manage and a
// shortfall ends the handler before the manager and before any audit row. A
// delete whose permission check was weakened or dropped was invisible before.
func TestAnUnauthorisedBindingDeleteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(r.p.handleBindingDelete, http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised binding delete answered %d, want 403", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the delete asked the guard for %v, want [authz.role.manage]", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on an unauthorised delete", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("an audit row was written %v on an unauthorised delete", got)
	}
}

// TestDeletingABindingAsksForRoleManageAndLandsOneSuccessRow holds the delete
// route's happy path: it asks for role.manage, calls DeleteBinding with the
// tenant, the account and the role from the path, and writes exactly one success
// row whose object is the account. Without this every decision past the empty
// account was uncovered.
func TestDeletingABindingAsksForRoleManageAndLandsOneSuccessRow(t *testing.T) {
	r := newRig(t)
	w := r.serve(r.p.handleBindingDelete, http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting a binding answered %d, want 204", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the delete asked the guard for %v, want [authz.role.manage]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteBinding" || got[0].tenant != "t1" ||
		got[0].account != "acct-1" || got[0].role != authz.Role("editor") {
		t.Fatalf("the manager calls = %+v, want one DeleteBinding(t1, acct-1, editor)", got)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionBindingDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionBindingDelete)
	}
	if rows[0].Object != "acct-1" {
		t.Fatalf("the mandatory row's object = %q, want the account acct-1", rows[0].Object)
	}
}

// TestDeletingABindingTheManagerRefusesIsMapped holds the delete route's failure
// paths: a tenant mismatch is a 400, a store failure is a 503, and a refused
// delete leaves a failed row linked to the mandatory one by the action id. A
// delete that swallowed the manager's error would report success.
func TestDeletingABindingTheManagerRefusesIsMapped(t *testing.T) {
	t.Run("a tenant mismatch is a 400", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrTenantMismatch)
		w := r.serve(r.p.handleBindingDelete, http.MethodDelete,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a delete whose manager refused the tenant answered %d, want 400", w.Code)
		}
	})
	t.Run("a store failure is a 503 with a linked failed row", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(r.p.handleBindingDelete, http.MethodDelete,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a binding delete whose store failed answered %d, want 503", w.Code)
		}
		rows := r.audit.entries()
		if len(rows) != 2 || rows[0].Action != actionBindingDelete || rows[0].Outcome != outcomeSuccess {
			t.Fatalf("the audit rows = %+v, want the mandatory success row and the failed one", rows)
		}
		if rows[1].Action != actionBindingDelete || rows[1].Outcome != outcomeFailed {
			t.Fatalf("the failed row = %+v, want a failed row for %s", rows[1], actionBindingDelete)
		}
		id := rows[0].Detail[detailActionID]
		if id == "" || rows[1].Detail[detailActionID] != id {
			t.Fatalf("the rows are not linked by the action id: %q vs %q", id, rows[1].Detail[detailActionID])
		}
	})
}

// TestABindingWriteWhoseAuditDoesNotLandIsA503 holds the mandatory audit's grip
// on the binding write: a row that does not land refuses the write with a 503
// before the manager is called, so a change that could not be recorded is not
// made. An implementation that ignored auditRequired's result would call the
// manager here.
func TestABindingWriteWhoseAuditDoesNotLandIsA503(t *testing.T) {
	r := newRig(t)
	r.audit.fail(errors.New("the audit store is down"))
	w := r.serve(r.p.handleBindingPut, http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a binding write whose mandatory audit did not land answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
	}
}

// TestAnonymousRequestsAreRefusedOnEveryBindingRoute holds the caller check on
// the binding routes: each answers 401 before the guard is asked and before the
// manager is called, so the check cannot be dropped on the list, the account
// read or the delete while the write's own case still passes.
func TestAnonymousRequestsAreRefusedOnEveryBindingRoute(t *testing.T) {
	cases := map[string]struct {
		handler func(*product) gin.HandlerFunc
		method  string
		path    string
		params  gin.Params
	}{
		"the list":         {func(p *product) gin.HandlerFunc { return p.handleBindingList }, http.MethodGet, "/api/v1/authz/admin/tenants/t1/bindings", params("tenant", "t1")},
		"the account read": {func(p *product) gin.HandlerFunc { return p.handleBindingOf }, http.MethodGet, "/api/v1/authz/admin/tenants/t1/bindings/acct-1", params("tenant", "t1", "account", "acct-1")},
		"the write":        {func(p *product) gin.HandlerFunc { return p.handleBindingPut }, http.MethodPut, "/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor", params("tenant", "t1", "account", "acct-1", "role", "editor")},
		"the delete":       {func(p *product) gin.HandlerFunc { return p.handleBindingDelete }, http.MethodDelete, "/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor", params("tenant", "t1", "account", "acct-1", "role", "editor")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			w := r.serveAnonymous(tc.handler(r.p), tc.method, tc.path, tc.params)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("an anonymous request answered %d, want 401", w.Code)
			}
			if got := r.guard.askedFor(); len(got) != 0 {
				t.Fatalf("the guard was asked %v for an anonymous request", got)
			}
			if got := r.manager.recorded(); len(got) != 0 {
				t.Fatalf("the manager was called %v for an anonymous request", got)
			}
		})
	}
}

// TestABindingReadWhoseManagerFailsIsMapped holds the binding reads' failure
// path: a manager outage on the list or the account read is a 503 and carries no
// bindings view, so a read that swallowed the error cannot answer 200 with an
// empty list.
func TestABindingReadWhoseManagerFailsIsMapped(t *testing.T) {
	t.Run("the list", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(r.p.handleBindingList, http.MethodGet, "/api/v1/authz/admin/tenants/t1/bindings",
			params("tenant", "t1"), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a binding list whose manager failed answered %d, want 503", w.Code)
		}
		if hasKey(t, w.Body.String(), "bindings") {
			t.Fatalf("the refusal %q carries a bindings view, want none", w.Body.String())
		}
	})
	t.Run("the account read", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(r.p.handleBindingOf, http.MethodGet, "/api/v1/authz/admin/tenants/t1/bindings/acct-1",
			params("tenant", "t1", "account", "acct-1"), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("an account read whose manager failed answered %d, want 503", w.Code)
		}
	})
}

// TestTheBindingListViewCarriesEveryField holds the binding list's response
// shape: the outer key is bindings, it carries one view per row, and each view's
// fields are the tenant, the account and the role. A body that only
// substring-carried the account and the role would not notice the two swapped
// into each other's field, a renamed key, or an extra element.
func TestTheBindingListViewCarriesEveryField(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-1", Role: "editor"},
		{Tenant: "t1", Account: "acct-2", Role: "viewer"},
	}
	w := r.serve(r.p.handleBindingList, http.MethodGet, "/api/v1/authz/admin/tenants/t1/bindings",
		params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the bindings answered %d, want 200", w.Code)
	}
	var got struct {
		Bindings []bindingView `json:"bindings"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Bindings) != 2 {
		t.Fatalf("the answer carries %d views, want two", len(got.Bindings))
	}
	first := got.Bindings[0]
	if first.Tenant != "t1" || first.Account != "acct-1" || first.Role != "editor" {
		t.Fatalf("the first view = %+v, want tenant t1, account acct-1, role editor", first)
	}
}

// TestABindingWriteCarriesTheRoleAndTheAuditObject holds the binding write's
// landing: the role from the path reaches the manager, and the mandatory row's
// object is the account. A write that blanked the role before the manager, or
// set the audit object to the tenant, fails here.
func TestABindingWriteCarriesTheRoleAndTheAuditObject(t *testing.T) {
	r := newRig(t)
	w := r.serve(r.p.handleBindingPut, http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("binding a role answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutBinding" || got[0].binding.Role != authz.Role("editor") {
		t.Fatalf("the manager calls = %+v, want one PutBinding carrying role editor", got)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Object != "acct-1" {
		t.Fatalf("the mandatory row = %+v, want its object to be the account acct-1", rows)
	}
}

// TestTheBindingDecisionIsAskedInThePlatformDomain holds the domain a binding
// route decides in: the platform domain, whatever tenant the path names. A route
// that decided in the request's own tenant or in a fixed unknown domain fails
// here.
func TestTheBindingDecisionIsAskedInThePlatformDomain(t *testing.T) {
	for name, drive := range map[string]func(*rig){
		"the list": func(r *rig) {
			r.serve(r.p.handleBindingList, http.MethodGet, "/api/v1/authz/admin/tenants/other/bindings",
				params("tenant", "other"), nil)
		},
		"the write": func(r *rig) {
			r.serve(r.p.handleBindingPut, http.MethodPut, "/api/v1/authz/admin/tenants/other/bindings/acct-1/editor",
				params("tenant", "other", "account", "acct-1", "role", "editor"), nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			drive(r)
			if got := r.guard.domainsAsked(); len(got) != 1 || got[0] != authz.PlatformDomain {
				t.Fatalf("the route decided in %v, want [system]: the platform domain and not the path tenant", got)
			}
		})
	}
}
