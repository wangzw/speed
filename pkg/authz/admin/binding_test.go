package admin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestListingBindingsRequiresRoleReadAndPassesThePathTenant holds the binding
// list route: it asks the guard for authz.role.read — a read's permission, not
// the write's — and answers the tenant named in the path by passing it, as the
// policy domain's identifier, to the manager's Bindings.
func TestListingBindingsRequiresRoleReadAndPassesThePathTenant(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}

	w := r.serve(r.p.handleBindingList, http.MethodGet,
		"/api/v1/authz/admin/tenants/t1/bindings", params("tenant", "t1"), nil)

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

// TestListingAnAccountsBindingsCallsBindingsOf holds that the account in the
// path reaches the manager's BindingsOf — the method that narrows to one
// account — and not the whole-tenant Bindings.
func TestListingAnAccountsBindingsCallsBindingsOf(t *testing.T) {
	r := newRig(t)
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-9", Role: "editor"}}

	w := r.serve(r.p.handleBindingOf, http.MethodGet,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-9", params("tenant", "t1", "account", "acct-9"), nil)

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

// TestBindingAnUnknownRoleIsRefused holds that a role the tenant's role table
// does not hold is a write-time refusal the manager reports as ErrUnknownRole,
// and that the module maps it to a 400.
func TestBindingAnUnknownRoleIsRefused(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrUnknownRole)

	w := r.serve(r.p.handleBindingPut, http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/bindings/acct-1/ghost",
		params("tenant", "t1", "account", "acct-1", "role", "ghost"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("binding an unknown role answered %d, want 400", w.Code)
	}
}

// TestBindingWithAnEmptyAccountIsRefused holds that the module refuses an empty
// account on shape, after the decision and before the manager is called: a
// binding addresses an account, and a request that names none is a 400 rather
// than a write the manager would refuse without a code this module maps. Nothing
// downstream runs — the manager is not called and no audit row is written.
func TestBindingWithAnEmptyAccountIsRefused(t *testing.T) {
	r := newRig(t)
	w := r.serve(r.p.handleBindingPut, http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/bindings//editor",
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

// TestRemovingABindingWithAnEmptyAccountIsRefused holds the same empty-account
// shape refusal on the delete verb: the route addresses a binding by its
// account, so an empty one is refused before the manager, which the case reads
// off the manager never being called.
func TestRemovingABindingWithAnEmptyAccountIsRefused(t *testing.T) {
	r := newRig(t)
	w := r.serve(r.p.handleBindingDelete, http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/bindings//editor",
		params("tenant", "t1", "account", "", "role", "editor"), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("removing a binding with an empty account answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %+v for an empty account, want it refused on shape", got)
	}
}

// TestAnEmptyTenantOnABindingWriteIsForwardedAndRefusedAsABadRequest holds that
// a binding write that names no tenant forwards the empty tenant verbatim to the
// manager — the module substitutes no domain for it — and maps the manager's
// tenant-mismatch refusal to a 400.
//
// The empty tenant is refused where the design puts the check — the policy
// adapter rejects a statement that names no tenant, and the manager reports it
// as a tenant mismatch which the module maps to a 400. The manager the module
// reads is a stand-in, so the case drives the refusal by making it fail; what it
// holds of the module is the mapping and that the module hands the path's tenant
// through untouched, empty included.
func TestAnEmptyTenantOnABindingWriteIsForwardedAndRefusedAsABadRequest(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrTenantMismatch)

	w := r.serve(r.p.handleBindingPut, http.MethodPut,
		"/api/v1/authz/admin/tenants//bindings/acct-1/editor",
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

// TestABindingWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID
// holds the write path's audit order and linkage on the binding route.
//
// The permitted row is written before the manager is called, so it is present
// even when the manager then refuses the write: a refused write leaves two rows,
// the mandatory success row and the failed row, and the two carry the same
// action identifier. Were the mandatory row written only after a landed write, a
// refused write would leave the failed row alone — which is what the two-row
// count rules out.
func TestABindingWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID(t *testing.T) {
	t.Run("a landed write leaves the mandatory success row", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(r.p.handleBindingPut, http.MethodPut,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
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
		w := r.serve(r.p.handleBindingPut, http.MethodPut,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/ghost",
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

// TestADenialLeavesNoAuditRowAndNoWrite holds the two refusals that precede the
// action on the binding route: a guard denial and an anonymous request each end
// the handler before the mandatory audit row is written and before the manager
// is called, so neither a row nor a write is left behind.
func TestADenialLeavesNoAuditRowAndNoWrite(t *testing.T) {
	t.Run("the guard's denial", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.serve(r.p.handleBindingPut, http.MethodPut,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
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
		w := r.serveAnonymous(r.p.handleBindingPut, http.MethodPut,
			"/api/v1/authz/admin/tenants/t1/bindings/acct-1/editor",
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
