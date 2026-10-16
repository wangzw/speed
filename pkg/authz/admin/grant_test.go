package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

// TestGrantingAPermissionOutsideTheCatalogIsRefused holds the grant write's own
// check: a permission the catalog does not hold is a 400 and the manager is not
// called.
//
// The permission is one the translation has a row for, so the refusal is the
// catalog's and not the translation's: an implementation that skipped the
// catalog check would reach the manager.
func TestGrantingAPermissionOutsideTheCatalogIsRefused(t *testing.T) {
	r := newRig(t) // the catalog declares no permission
	w := r.serve(r.p.handleGrantPut, http.MethodPut,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("granting a permission outside the catalog answered %d, want 400", w.Code)
	}
	if got := r.catalog.asked(); len(got) != 1 || got[0] != authz.PermissionAccountRead {
		t.Fatalf("the catalog was asked %v, want [authn.account.read]", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a permission outside the catalog", got)
	}
}

// TestGrantingATranslatedPermissionCallsPutGrantWithThePair holds the write's
// translation: the permission named in the path reaches the manager as the
// (resource, action) pair the engine matches on, alongside the tenant and role
// from the path.
//
// The expected pair is read off the translation the handler shares rather than
// written down here, so a hardcoded wrong pair in the handler is caught.
func TestGrantingATranslatedPermissionCallsPutGrantWithThePair(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	w := r.serve(r.p.handleGrantPut, http.MethodPut,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("granting a declared permission answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutGrant" {
		t.Fatalf("the manager calls = %+v, want one PutGrant", got)
	}
	grant := got[0].grant
	if grant.Tenant != "t1" || string(grant.Role) != "r1" {
		t.Fatalf("the grant names tenant %q role %q, want t1 and r1", grant.Tenant, grant.Role)
	}
	object, action, ok := translation.Lookup(authz.PermissionAccountRead)
	if !ok {
		t.Fatal("authn.account.read has no translation row, so the case cannot hold the pair")
	}
	if grant.Resource != object || grant.Action != action {
		t.Fatalf("the grant pair = (%q, %q), want the translation (%q, %q)",
			grant.Resource, grant.Action, object, action)
	}
}

// TestAGrantOnABuiltinPositionIsRefused holds that a grant the manager refuses
// because the role is builtin is a 409: the target exists but its permissions
// cannot be changed. The manager judges the builtin role, so it is called and its
// refusal is mapped.
func TestAGrantOnABuiltinPositionIsRefused(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.manager.fail(authz.ErrBuiltinRole)
	w := r.serve(r.p.handleGrantPut, http.MethodPut,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("a grant on a builtin role answered %d, want 409", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 1 || got[0].op != "PutGrant" {
		t.Fatalf("the manager calls = %+v, want one PutGrant that judged the builtin role", got)
	}
	rows := r.audit.entries()
	if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the audit rows = %+v, want a permitted row and a failed one", rows)
	}
}

// TestDeletingAnAbsentGrantIsIdempotent holds that revoking a grant that is not
// there is a success and not a missing-target answer: the manager's delete is
// idempotent, so the write lands as a 204 carrying the translated pair.
func TestDeletingAnAbsentGrantIsIdempotent(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	w := r.serve(r.p.handleGrantDelete, http.MethodDelete,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting an absent grant answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteGrant" {
		t.Fatalf("the manager calls = %+v, want one DeleteGrant", got)
	}
	object, action, _ := translation.Lookup(authz.PermissionAccountRead)
	if got[0].grant.Resource != object || got[0].grant.Action != action {
		t.Fatalf("the delete carried the pair (%q, %q), want the translation (%q, %q)",
			got[0].grant.Resource, got[0].grant.Action, object, action)
	}
}

// TestAWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID
// holds the grant write's audit order: the mandatory success row lands before
// the action, so even a write the manager refuses leaves it, and the failed row
// that closes the refusal is linked to it by the action identifier.
//
// That the mandatory row is written before the action is what its survival of a
// refused action observes: an implementation that wrote it only after a
// successful action would have one row here, not two.
func TestAWriteWritesTheMandatoryRowBeforeTheActionAndAFailedRowLinksByActionID(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(r.p.handleGrantPut, http.MethodPut,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a write whose action failed answered %d, want 503", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 1 || got[0].op != "PutGrant" {
		t.Fatalf("the manager calls = %+v, want one PutGrant", got)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want the mandatory row and the failed one", rows)
	}
	if rows[0].Action != actionGrantPut || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the mandatory row = %+v, want a success row for %s", rows[0], actionGrantPut)
	}
	if rows[1].Action != actionGrantPut || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the failed row = %+v, want a failed row for %s", rows[1], actionGrantPut)
	}
	id := rows[0].Detail[detailActionID]
	if id == "" || rows[1].Detail[detailActionID] != id {
		t.Fatalf("the rows are not linked by the action id: %q vs %q", id, rows[1].Detail[detailActionID])
	}
}

// TestAnUnauthorisedWriteReachesNoManagerAndWritesNoAudit holds that a shortfall
// from the guard ends the write before anything else runs: the guard is asked for
// the write's permission, and neither the manager is called nor an audit row is
// written.
func TestAnUnauthorisedWriteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.guard.allow = false
	w := r.serve(r.p.handleGrantPut, http.MethodPut,
		"/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised write answered %d, want 403", w.Code)
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

// TestListingGrantsAnswersTheRowsAsViews holds the grant read: it asks for
// role.read and answers 200 with a view per grant the manager returned.
func TestListingGrantsAnswersTheRowsAsViews(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "r1", Resource: "account", Action: "read"}}
	w := r.serve(r.p.handleGrantList, http.MethodGet,
		"/authz/admin/tenants/t1/roles/r1/grants",
		params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the grants answered %d, want 200", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleRead {
		t.Fatalf("the read asked for %v, want [authz.role.read]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "Grants" || got[0].tenant != "t1" || got[0].role != "r1" {
		t.Fatalf("the manager calls = %+v, want one Grants for tenant t1 role r1", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"account"`) || !strings.Contains(body, `"read"`) {
		t.Fatalf("the answer %q does not carry the grant's (account, read) pair", body)
	}
}
