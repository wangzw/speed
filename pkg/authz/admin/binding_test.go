package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestListingBindingsRequiresRoleReadAndPassesThePathTenant checks that listing bindings requires role read and passes the path tenant.
func TestListingBindingsRequiresRoleReadAndPassesThePathTenant(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}

	w := r.serve(readRoute(r.p, bindingListOperation.Handle(r.p.handleBindingList)), http.MethodGet,
		"/api/v1/system/tenants/t1/bindings", params("tenant", "t1"), nil)

	if w.Code != http.StatusOK {
		t.Fatalf("listing the bindings answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the listing asked for %v, want [authz.role.read]", got)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "Bindings" || calls[0].tenant != "t1" {
		t.Fatalf("the manager calls = %+v, want one Bindings for tenant t1", calls)
	}
	if body := w.Body.String(); !strings.Contains(body, "acct-1") || !strings.Contains(body, "editor") {
		t.Fatalf("the answer %q does not carry the binding's account and role", body)
	}
}

// TestListingAnAccountsBindingsCallsBindingsOf checks that listing an accounts bindings calls bindings of.
func TestListingAnAccountsBindingsCallsBindingsOf(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-9", Role: "editor"}}

	w := r.serve(readRoute(r.p, bindingOfOperation.Handle(r.p.handleBindingOf)), http.MethodGet,
		"/api/v1/system/tenants/t1/bindings/acct-9", params("tenant", "t1", "account", "acct-9"), nil)

	if w.Code != http.StatusOK {
		t.Fatalf("listing an account's bindings answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the account listing asked for %v, want [authz.role.read]", got)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "BindingsOf" || calls[0].tenant != "t1" || calls[0].account != "acct-9" {
		t.Fatalf("the manager calls = %+v, want one BindingsOf for tenant t1 account acct-9", calls)
	}
}

// TestBindingAnUnknownRoleIsRefused checks that binding an unknown role is refused.
func TestBindingAnUnknownRoleIsRefused(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrUnknownRole)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/ghost",
		params("tenant", "t1", "account", "acct-1", "role", "ghost"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("binding an unknown role answered %d, want 400", w.Code)
	}
}

// TestBindingTheReservedRoleOutsideThePlatformDomainIsProtected checks that binding the reserved role outside the platform domain is protected.
func TestBindingTheReservedRoleOutsideThePlatformDomainIsProtected(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrReservedRole)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings/acct-1/platform-admin",
		params("tenant", "t1", "account", "acct-1", "role", "platform-admin"), nil)

	if w.Code != http.StatusForbidden {
		t.Fatalf("binding the reserved role outside the platform domain answered %d, want 403", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
		t.Fatalf("the 403's type = %q, want urn:speed:authz:protected", p.Type)
	}
}

// TestBindingWithAnEmptyAccountIsRefused checks that binding with an empty account is refused.
func TestBindingWithAnEmptyAccountIsRefused(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants/t1/bindings//editor",
		params("tenant", "t1", "account", "", "role", "editor"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("binding an empty account answered %d, want 400", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the write asked the guard for %v, want [authz.role.manage] before the shape check", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %+v for an empty account, want it refused on shape", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("the audit recorded %+v for an empty account, want no row", got)
	}
}

// TestRemovingABindingWithAnEmptyAccountIsRefused checks that removing a binding with an empty account is refused.
func TestRemovingABindingWithAnEmptyAccountIsRefused(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
		"/api/v1/system/tenants/t1/bindings//editor",
		params("tenant", "t1", "account", "", "role", "editor"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("removing a binding with an empty account answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %+v for an empty account, want it refused on shape", got)
	}
}

// TestAnEmptyTenantOnABindingWriteIsForwardedAndRefusedAsABadRequest checks that an empty tenant on a binding write is forwarded and refused as a bad request.
func TestAnEmptyTenantOnABindingWriteIsForwardedAndRefusedAsABadRequest(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrTenantMismatch)

	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/system/tenants//bindings/acct-1/editor",
		params("tenant", "", "account", "acct-1", "role", "editor"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("binding with an empty tenant answered %d, want 400", w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "editor") {
		t.Fatalf("the refusal %q carries the binding, want no row written", body)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "PutBinding" || calls[0].tenant != "" {
		t.Fatalf("the manager calls = %+v, want one PutBinding carrying the empty tenant untouched", calls)
	}
}

// TestABindingWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID checks that a binding write writes the mandatory row before the action and a failed row links by action ID.
func TestABindingWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID(t *testing.T) {
	t.Run("a landed write leaves the mandatory success row", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)

		if w.Code != http.StatusNoContent {
			t.Fatalf("a permitted binding write answered %d, want 204", w.Code)
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionBindingPut || rows[0].Outcome != outcomeSuccess {
			t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionBindingPut)
		}
		if rows[0].Detail[detailTenant] != "t1" || rows[0].Detail[detailAccount] != "acct-1" ||
			rows[0].Detail[detailRole] != "editor" {
			t.Fatalf("the mandatory row's detail = %+v, want the tenant, account and role", rows[0].Detail)
		}
		if got := r.manager.recorded(); len(got) != 1 || got[0].op != "PutBinding" {
			t.Fatalf("the manager calls = %+v, want one PutBinding", got)
		}
	})

	t.Run("a refused write links a failed row by action id", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrUnknownRole)
		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/ghost",
			params("tenant", "t1", "account", "acct-1", "role", "ghost"), nil)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("binding an unknown role answered %d, want 400", w.Code)
		}
		rows := r.audit.entries()
		if len(rows) != 2 {
			t.Fatalf("the audit rows = %+v, want the mandatory success row and the failed one", rows)
		}
		if rows[0].Action != actionBindingPut || rows[0].Outcome != outcomeSuccess {
			t.Fatalf("the first row = %+v, want the mandatory success row for %s", rows[0], actionBindingPut)
		}
		if rows[1].Action != actionBindingPut || rows[1].Outcome != outcomeFailed {
			t.Fatalf("the second row = %+v, want the failed row for %s", rows[1], actionBindingPut)
		}
		id0, id1 := rows[0].Detail[detailActionID], rows[1].Detail[detailActionID]
		if id0 == "" || id0 != id1 {
			t.Fatalf("the rows are not linked by %s: %q vs %q", detailActionID, id0, id1)
		}
	})
}

// TestADenialLeavesNoAuditRowAndNoWrite checks that a denial leaves no audit row and no write.
func TestADenialLeavesNoAuditRowAndNoWrite(t *testing.T) {
	t.Run("the guard's denial", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)

		if w.Code != http.StatusForbidden {
			t.Fatalf("a denied binding write answered %d, want 403", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %+v on a denied write", got)
		}
		if got := r.audit.entries(); len(got) != 0 {
			t.Fatalf("the audit recorded %+v on a denied write", got)
		}
	})

	t.Run("an anonymous request", func(t *testing.T) {
		r := newRig(t)
		w := r.serveAnonymous(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
			"/api/v1/system/tenants/t1/bindings/acct-1/editor",
			params("tenant", "t1", "account", "acct-1", "role", "editor"))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("an anonymous binding write answered %d, want 401", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %+v for an anonymous request", got)
		}
		if got := r.audit.entries(); len(got) != 0 {
			t.Fatalf("the audit recorded %+v for an anonymous request", got)
		}
	})
}
