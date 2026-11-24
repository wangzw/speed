package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// TestAnUnauthorisedBindingDeleteReachesNoManagerAndWritesNoAudit checks that an unauthorised binding delete reaches no manager and writes no audit.
func TestAnUnauthorisedBindingDeleteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
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

// TestDeletingABindingAsksForRoleManageAndLandsOneSuccessRow checks that deleting a binding asks for role manage and lands one success row.
func TestDeletingABindingAsksForRoleManageAndLandsOneSuccessRow(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
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
	if rows[0].Object != "account:acct-1" {
		t.Fatalf("the mandatory row's object = %q, want the account acct-1", rows[0].Object)
	}
}

// TestDeletingABindingTheManagerRefusesIsMapped checks that deleting a binding the manager refuses is mapped.
func TestDeletingABindingTheManagerRefusesIsMapped(t *testing.T) {
	t.Run("a tenant mismatch is a 400", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrTenantMismatch)
		w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a delete whose manager refused the tenant answered %d, want 400", w.Code)
		}
	})
	t.Run("a store failure is a 503 with a linked failed row", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
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

// TestABindingWriteWhoseAuditDoesNotLandIsA503 checks that a binding write whose audit does not land is a 503.
func TestABindingWriteWhoseAuditDoesNotLandIsA503(t *testing.T) {
	r := newRig(t)
	r.audit.fail(errors.New("the audit store is down"))
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a binding write whose mandatory audit did not land answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
	}
}

// TestAnonymousRequestsAreRefusedOnEveryBindingRoute checks that anonymous requests are refused on every binding route.
func TestAnonymousRequestsAreRefusedOnEveryBindingRoute(t *testing.T) {
	cases := map[string]struct {
		handler func(*product) gin.HandlerFunc
		method  string
		path    string
		params  gin.Params
	}{
		"the list": {func(p *product) gin.HandlerFunc {
			return readRoute(p, bindingListOperation.Handle(p.handleBindingList))
		}, http.MethodGet, "/api/v1/system/tenants/t1/bindings", params("tenant", "t1")},
		"the account read": {func(p *product) gin.HandlerFunc { return readRoute(p, bindingOfOperation.Handle(p.handleBindingOf)) }, http.MethodGet, "/api/v1/system/tenants/t1/bindings/acct-1", params("tenant", "t1", "account", "acct-1")},
		"the write":        {func(p *product) gin.HandlerFunc { return writeRoute(p, bindingPutOperation.Handle(p.handleBindingPut)) }, http.MethodPut, "/api/v1/system/tenants/t1/bindings/acct-1/editor", params("tenant", "t1", "account", "acct-1", "role", "editor")},
		"the delete": {func(p *product) gin.HandlerFunc {
			return writeRoute(p, bindingDeleteOperation.Handle(p.handleBindingDelete))
		}, http.MethodDelete, "/api/v1/system/tenants/t1/bindings/acct-1/editor", params("tenant", "t1", "account", "acct-1", "role", "editor")},
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

// TestABindingReadWhoseManagerFailsIsMapped checks that a binding read whose manager fails is mapped.
func TestABindingReadWhoseManagerFailsIsMapped(t *testing.T) {
	t.Run("the list", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), http.MethodGet, "/api/v1/system/tenants/t1/bindings",
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
		w := r.serve(readRoute(r.p, bindingOfOperation.Handle(r.p.handleBindingOf)), http.MethodGet, "/api/v1/system/tenants/t1/bindings/acct-1",
			params("tenant", "t1", "account", "acct-1"), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("an account read whose manager failed answered %d, want 503", w.Code)
		}
	})
}

// TestTheBindingListViewCarriesEveryField checks that the binding list view carries every field.
func TestTheBindingListViewCarriesEveryField(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-1", Role: "editor"},
		{Tenant: "t1", Account: "acct-2", Role: "viewer"},
	}
	w := r.serve(readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), http.MethodGet, "/api/v1/system/tenants/t1/bindings",
		params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the bindings answered %d, want 200", w.Code)
	}
	var got struct {
		Bindings []AuthzAdminBindingView `json:"bindings"`
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

// TestABindingWriteCarriesTheRoleAndTheAuditObject checks that a binding write carries the role and the audit object.
func TestABindingWriteCarriesTheRoleAndTheAuditObject(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("binding a role answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutBinding" || got[0].binding.Role != authz.Role("editor") {
		t.Fatalf("the manager calls = %+v, want one PutBinding carrying role editor", got)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Object != "account:acct-1" {
		t.Fatalf("the mandatory row = %+v, want its object to be the account acct-1", rows)
	}
}

// TestTheBindingDecisionIsAskedInThePlatformDomain checks that the binding decision is asked in the platform domain.
func TestTheBindingDecisionIsAskedInThePlatformDomain(t *testing.T) {
	for name, drive := range map[string]func(*rig){
		"the list": func(r *rig) {
			r.serve(readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), http.MethodGet, "/api/v1/system/tenants/other/bindings",
				params("tenant", "other"), nil)
		},
		"the write": func(r *rig) {
			r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut, "/api/v1/system/tenants/other/bindings/acct-1/editor",
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

// TestRemovingTheCallersOwnTenantAdministratorBindingIsRefused checks that removing the caller's own tenant administrator binding is refused.
func TestRemovingTheCallersOwnTenantAdministratorBindingIsRefused(t *testing.T) {
	const caller = "test-account"
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings/"+caller+"/"+string(authz.TenantAdminRole),
		params("tenant", "t1", "account", caller, "role", string(authz.TenantAdminRole)), nil,
		authntest.WithAccount(caller))

	if w.Code != http.StatusForbidden {
		t.Fatalf("removing the caller's own tenant-admin binding answered %d, want 403", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
		t.Fatalf("the 403's type = %q, want urn:speed:authz:protected", p.Type)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %+v for a refused self-removal", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("the audit recorded %+v for a refused self-removal", got)
	}
}

// TestRemovingAnotherAccountsTenantAdministratorBindingIsAllowed checks that removing another account's tenant administrator binding is allowed.
func TestRemovingAnotherAccountsTenantAdministratorBindingIsAllowed(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings/acct-2/"+string(authz.TenantAdminRole),
		params("tenant", "t1", "account", "acct-2", "role", string(authz.TenantAdminRole)), nil,
		authntest.WithAccount("test-account"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("removing another account's tenant-admin binding answered %d, want 204", w.Code)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "DeleteBinding" || calls[0].account != "acct-2" ||
		calls[0].role != authz.TenantAdminRole {
		t.Fatalf("the manager calls = %+v, want one DeleteBinding for acct-2", calls)
	}
}

// TestRemovingTheCallersOwnOtherBindingIsAllowed checks that removing the caller's own other binding is allowed.
func TestRemovingTheCallersOwnOtherBindingIsAllowed(t *testing.T) {
	const caller = "test-account"
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings/"+caller+"/editor",
		params("tenant", "t1", "account", caller, "role", "editor"), nil,
		authntest.WithAccount(caller))

	if w.Code != http.StatusNoContent {
		t.Fatalf("removing the caller's own non-administrator binding answered %d, want 204", w.Code)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "DeleteBinding" || calls[0].account != caller {
		t.Fatalf("the manager calls = %+v, want one DeleteBinding for the caller", calls)
	}
}
