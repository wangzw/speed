package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestListingRolesRequiresRoleRead holds the role read: the handler asks the
// guard for authz.role.read and, when the decision allows, passes the tenant the
// path named to the manager as the policy domain's identifier and answers 200.
//
// The tenant the manager is called with is read off the path parameter and not
// off any alias: the case addresses tenant "t1" and holds that "t1" — the
// identifier itself — is what reached the manager, so an implementation that
// replaced it with a looked-up name would fail here.
func TestListingRolesRequiresRoleRead(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor", Builtin: false, DescriptionKey: "authz.role.editor"},
	}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet, "/api/v1/authz/admin/tenants/t1/roles",
		params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the read asked the guard for %v, want [authz.role.read]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "Roles" || got[0].tenant != "t1" {
		t.Fatalf("the manager calls = %+v, want one Roles for the path identifier t1", got)
	}
	if !strings.Contains(w.Body.String(), "editor") {
		t.Fatalf("the answer %q does not carry the listed role", w.Body.String())
	}
}

// TestPutRoleRequiresRoleManageAndWritesTheIdentifierAsTenant holds the role
// write: the handler asks the guard for authz.role.manage and, when allowed,
// writes the record the manager stores with the tenant the path named as the
// record's Tenant and the body's description key forwarded.
//
// The body is sent with a `builtin` member the schema no longer carries: a
// client that sends it anyway must not reach the manager with the mark set. The
// mark is what freezes a role for good, and the management surface is not a
// path that sets it.
func TestPutRoleRequiresRoleManageAndWritesTheIdentifierAsTenant(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/ops",
		params("tenant", "t1", "role", "ops"),
		map[string]any{"builtin": true, "description_key": "authz.role.ops"})
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
	if rec.DescriptionKey != "authz.role.ops" {
		t.Fatalf("the record = %+v, want the body's description_key forwarded", rec)
	}
}

// TestDeletingABuiltinRoleIsRefused holds that a builtin role the manager
// refuses to delete is a 409 — the target exists but cannot be changed — with
// the manager having judged it, and that a plain role is deleted as a 204, so a
// blanket refusal of every delete would fail the second case.
func TestDeletingABuiltinRoleIsRefused(t *testing.T) {
	t.Run("a builtin role is a 409", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrBuiltinRole)
		w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/authz/admin/tenants/t1/roles/ops",
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
		w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/authz/admin/tenants/t1/roles/editor",
			params("tenant", "t1", "role", "editor"), nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("deleting a plain role answered %d, want 204", w.Code)
		}
	})
}

// TestAnUnknownRoleIsRefused holds that a role the manager does not know is a
// 400: the request named something that is not there. The manager judges it, so
// it is called; the refusal is its sentinel mapped to the bad-request answer.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrUnknownRole)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/ghost",
		params("tenant", "t1", "role", "ghost"), roleBody{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("writing a role the manager does not know answered %d, want 400", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutRole" {
		t.Fatalf("the manager calls = %+v, want one PutRole that judged the unknown role", got)
	}
}

// TestAnEmptyTenantIsForwardedAndItsRefusalIsABadRequest holds the empty-tenant
// case. The empty tenant is not this module's to interpret: the path identifier
// is forwarded to the manager verbatim, and it is the manager — its store's
// tenant columns and checks — that refuses an empty tenant with ErrTenantMismatch,
// which the handler maps to a 400. The case holds that the empty tenant reaches
// the manager as the empty string, so an implementation that substituted a
// tenant of its own (the reserved platform domain, or an alias) would fail here.
func TestAnEmptyTenantIsForwardedAndItsRefusalIsABadRequest(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrTenantMismatch)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/authz/admin/tenants//roles/editor",
		params("tenant", "", "role", "editor"), roleBody{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a write with an empty tenant answered %d, want 400", w.Code)
	}
	calls := r.manager.recorded()
	if len(calls) != 1 || calls[0].op != "PutRole" || calls[0].tenant != "" {
		t.Fatalf("the manager calls = %+v, want one PutRole carrying the empty path tenant forwarded verbatim", calls)
	}
}

// TestAnUnauthorisedRoleWriteReachesNoManagerAndWritesNoAudit holds that a
// shortfall from the guard ends the write before anything else runs: the guard
// is asked for the write's permission, and neither the manager is called nor an
// audit row is written.
func TestAnUnauthorisedRoleWriteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/editor",
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

// TestARoleWriteWhoseAuditDoesNotLandIsA503AndDoesNothing holds the mandatory
// audit row's grip on the action: a row that does not land refuses the write
// with a 503 before the manager is called, so a change that could not be
// recorded is not made. An implementation that called the manager first would
// reach it here.
func TestARoleWriteWhoseAuditDoesNotLandIsA503AndDoesNothing(t *testing.T) {
	r := newRig(t)
	r.audit.fail(errors.New("the audit store is down"))
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a write whose mandatory audit did not land answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v when the mandatory audit did not land", got)
	}
}

// TestARoleWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID
// holds the role write's audit order: the mandatory success row lands before the
// action, so even a write the manager refuses leaves it, and the failed row that
// closes the refusal is linked to it by the action identifier.
//
// That the mandatory row is written before the action is what its survival of a
// refused action observes: an implementation that wrote it only after a
// successful action would have one row here, not two.
func TestARoleWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID(t *testing.T) {
	r := newRig(t)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete, "/api/v1/authz/admin/tenants/t1/roles/editor",
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
