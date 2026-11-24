package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestListingRolesRequiresRoleRead checks that listing roles requires role read.
func TestListingRolesRequiresRoleRead(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor", Builtin: false, DescriptionKey: "authz.role.editor"},
	}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet, "/api/v1/system/tenants/t1/roles",
		params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the read asked the guard for %v, want [authz.role.read]", got)
	}
	got := r.manager.recorded()
	if len(got) != 2 || got[0].op != "Roles" || got[0].tenant != "t1" || got[1].op != "GrantCounts" || got[1].tenant != "t1" {
		t.Fatalf("the manager calls = %+v, want Roles and GrantCounts for the path identifier t1", got)
	}
	if !strings.Contains(w.Body.String(), "editor") {
		t.Fatalf("the answer %q does not carry the listed role", w.Body.String())
	}
}

// TestPutRoleRequiresRoleManageAndWritesTheIdentifierAsTenant checks that put role requires role manage and writes the identifier as tenant.
func TestPutRoleRequiresRoleManageAndWritesTheIdentifierAsTenant(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"),
		map[string]any{"builtin": true, "description": "Runs the operations."})
	if w.Code != http.StatusNoContent {
		t.Fatalf("writing a role answered %d, want 204", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the write asked the guard for %v, want [authz.role.manage]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole", got)
	}
	rec := got[0].roleRec
	if rec.Tenant != "t1" {
		t.Fatalf("the record's Tenant = %q, want the path identifier t1", rec.Tenant)
	}
	if string(rec.Role) != "ops" {
		t.Fatalf("the record's Role = %q, want the path role ops", rec.Role)
	}
	if rec.Builtin {
		t.Fatalf("the record = %+v, want the client's builtin member to reach nothing", rec)
	}
	if rec.Description != "Runs the operations." {
		t.Fatalf("the record = %+v, want the body's description forwarded", rec)
	}
}

// TestDeletingABuiltinRoleIsRefused checks that deleting a builtin role is refused.
func TestDeletingABuiltinRoleIsRefused(t *testing.T) {
	t.Run("a builtin role is a 409", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrBuiltinRole)
		w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/system/tenants/t1/roles/ops",
			params("tenant", "t1", "role", "ops"), nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("deleting a builtin role answered %d, want 409", w.Code)
		}
		got := r.manager.recorded()
		if len(got) != 1 || got[0].op != "DeleteRole" || got[0].tenant != "t1" || got[0].role != authz.Role("ops") {
			t.Fatalf("the manager calls = %+v, want one DeleteRole for tenant t1 role ops", got)
		}
	})
	t.Run("a plain role deletes as a 204", func(t *testing.T) {
		r := newRig(t)
		w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/system/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"), nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("deleting a plain role answered %d, want 204", w.Code)
		}
	})
}

// TestAnUnknownRoleIsRefused checks that an unknown role is refused.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrUnknownRole)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/ghost",
		params("tenant", "t1", "role", "ghost"), roleBody{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("writing a role the manager does not know answered %d, want 400", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole that judged the unknown role", got)
	}
}

// TestAnEmptyTenantIsForwardedAndItsRefusalIsABadRequest checks that an empty tenant is forwarded and its refusal is a bad request.
func TestAnEmptyTenantIsForwardedAndItsRefusalIsABadRequest(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrTenantMismatch)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants//roles/editor",
		params("tenant", "", "role", "editor"), roleBody{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a write with an empty tenant answered %d, want 400", w.Code)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "PutRole" || calls[0].tenant != "" {
		t.Fatalf("the manager calls = %+v, want one PutRole carrying the empty path tenant forwarded verbatim", calls)
	}
}

// TestAnUnauthorisedRoleWriteReachesNoManagerAndWritesNoAudit checks that an unauthorised role write reaches no manager and writes no audit.
func TestAnUnauthorisedRoleWriteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised role write answered %d, want 403", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the guard was asked %v, want [authz.role.manage]", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on an unauthorised write", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("an audit row was written %v on an unauthorised write", got)
	}
}

// TestARoleWriteWhoseAuditDoesNotLandIsA503AndDoesNothing checks that a role write whose audit does not land is a 503 and does nothing.
func TestARoleWriteWhoseAuditDoesNotLandIsA503AndDoesNothing(t *testing.T) {
	r := newRig(t)
	r.audit.fail(errors.New("the audit store is down"))
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/system/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a write whose mandatory audit did not land answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
	}
}

// TestARoleWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID checks that a role write writes the mandatory row before the action and a failed row links by action ID.
func TestARoleWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID(t *testing.T) {
	r := newRig(t)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/system/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a role write whose action failed answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 1 || got[0].op != "DeleteRole" {
		t.Fatalf("the manager calls = %+v, want one DeleteRole", got)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want the mandatory row and the failed one", rows)
	}
	if rows[0].Action != actionRoleDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the mandatory row = %+v, want a success row for %s", rows[0], actionRoleDelete)
	}
	if rows[1].Action != actionRoleDelete || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the failed row = %+v, want a failed row for %s", rows[1], actionRoleDelete)
	}
	id := rows[0].Detail[detailActionID]
	if id == "" || rows[1].Detail[detailActionID] != id {
		t.Fatalf("the rows are not linked by the action id: %q vs %q", id, rows[1].Detail[detailActionID])
	}
}
