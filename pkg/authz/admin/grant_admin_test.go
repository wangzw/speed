package admin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

// TestAnUnauthorisedGrantDeleteReachesNoManagerAndWritesNoAudit holds the grant
// delete route's decision: the guard is asked for role.manage and a shortfall
// ends the handler before the manager and before any audit row. Without this the
// delete's permission check could be weakened to role.read, or dropped, and no
// case would fail.
func TestAnUnauthorisedGrantDeleteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.guard.allow = false
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised grant delete answered %d, want 403", w.Code)
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

// TestDeletingAGrantAsksForRoleManageAndLandsOneSuccessRow holds the delete
// route's permission and its single audit row: it asks for role.manage, calls
// DeleteGrant with the tenant, the role and the translated pair from the path,
// and writes exactly one success row whose object is the permission, before any
// other answer. The row's tenant and role are held too, so a record written
// under the wrong tenant or role fails.
func TestDeletingAGrantAsksForRoleManageAndLandsOneSuccessRow(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting a grant answered %d, want 204", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionRoleManage {
		t.Fatalf("the delete asked the guard for %v, want [authz.role.manage]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteGrant" {
		t.Fatalf("the manager calls = %+v, want one DeleteGrant", got)
	}
	object, action, _ := translation.Lookup(authz.PermissionAccountRead)
	if got[0].grant.Tenant != "t1" || string(got[0].grant.Role) != "r1" ||
		got[0].grant.Resource != object || got[0].grant.Action != action {
		t.Fatalf("the delete carried %+v, want tenant t1 role r1 pair (%q, %q)", got[0].grant, object, action)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionGrantDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionGrantDelete)
	}
	if rows[0].Object != string(authz.PermissionAccountRead) {
		t.Fatalf("the mandatory row's object = %q, want the permission", rows[0].Object)
	}
}

// TestDeletingAGrantWhoseActionFailsLinksAFailedRow holds the delete's audit
// order: the mandatory row lands before the manager, so a delete the manager
// refuses leaves both the success row and a failed row linked by the action id.
// An implementation that wrote the row only after a landed delete would leave
// the failed row alone.
func TestDeletingAGrantWhoseActionFailsLinksAFailedRow(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a grant delete whose action failed answered %d, want 503", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want the mandatory row and the failed one", rows)
	}
	if rows[0].Action != actionGrantDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the mandatory row = %+v, want a success row for %s", rows[0], actionGrantDelete)
	}
	if rows[1].Action != actionGrantDelete || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the failed row = %+v, want a failed row for %s", rows[1], actionGrantDelete)
	}
	id := rows[0].Detail[detailActionID]
	if id == "" || rows[1].Detail[detailActionID] != id {
		t.Fatalf("the rows are not linked by the action id: %q vs %q", id, rows[1].Detail[detailActionID])
	}
}

// TestADeleteTheManagerRefusesIsMapped holds the delete route's error mapping: a
// builtin role the manager refuses is a 409, and a store failure is a 503. An
// implementation that swallowed the manager's error would report the delete as
// having landed.
func TestADeleteTheManagerRefusesIsMapped(t *testing.T) {
	t.Run("a builtin role is a 409", func(t *testing.T) {
		r := newRig(t, authz.PermissionAccountRead)
		r.manager.fail(authz.ErrBuiltinRole)
		w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
			"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("deleting a grant on a builtin role answered %d, want 409", w.Code)
		}
	})
	t.Run("a store failure is a 503", func(t *testing.T) {
		r := newRig(t, authz.PermissionAccountRead)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
			"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a grant delete whose store failed answered %d, want 503", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 1 || got[0].op != "DeleteGrant" {
			t.Fatalf("the manager calls = %+v, want one DeleteGrant", got)
		}
	})
}

// TestDeletingAGrantWithoutATranslationRowIsRefused holds the delete route's
// translation guard. The delete route has no catalog check, so a permission the
// translation has no row for is otherwise handed to the manager with an empty
// (resource, action). The case uses a permission outside the table and holds
// that the manager is not called.
func TestDeletingAGrantWithoutATranslationRowIsRefused(t *testing.T) {
	r := newRig(t, authz.Permission("authn.unknown.thing"))
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.unknown.thing",
		params("tenant", "t1", "role", "r1", "permission", "authn.unknown.thing"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("deleting a grant with no translation row answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a permission with no translation row", got)
	}
}

// TestDeletingAGrantDoesNotConsultTheCatalog holds the delete route's deliberate
// omission of the catalog check: a permission can be revoked even after it is no
// longer declared, because the delete translates without asking the catalog.
// The catalog is left empty and the permission still deletes, so an
// implementation that added a catalog check to the delete fails here.
func TestDeletingAGrantDoesNotConsultTheCatalog(t *testing.T) {
	r := newRig(t) // the catalog declares nothing
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting an undeclared grant answered %d, want 204: the delete does not consult the catalog", w.Code)
	}
	if got := r.catalog.asked(); len(got) != 0 {
		t.Fatalf("the delete asked the catalog %v, want none", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteGrant" {
		t.Fatalf("the manager calls = %+v, want one DeleteGrant", got)
	}
}

// TestAListingGrantsWhoseManagerFailsIsMapped holds the grant read's failure
// path: a manager outage is a 503, not a 200 with an empty body. A read that
// swallowed the error would report success.
func TestAListingGrantsWhoseManagerFailsIsMapped(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants",
		params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a grant list whose manager failed answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "grants") {
		t.Fatalf("the refusal %q carries a grants view, want none", w.Body.String())
	}
}

// TestTheGrantListViewCarriesEveryField holds the grant list's response shape:
// the answer's outer key is grants, it carries one view per row, and each view's
// fields are the tenant, the role, the resource and the action. A body that only
// substring-carried a permission name would not notice a renamed key, an extra
// element or a raw record leaked instead of a view.
func TestTheGrantListViewCarriesEveryField(t *testing.T) {
	r := newRig(t, authz.PermissionRoleRead)
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "r1", Resource: "account", Action: "read"}}
	w := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants",
		params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the grants answered %d, want 200", w.Code)
	}
	var got struct {
		Grants []AuthzAdminGrantView `json:"grants"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Grants) != 1 {
		t.Fatalf("the answer carries %d views, want one", len(got.Grants))
	}
	v := got.Grants[0]
	if v.Tenant != "t1" || v.Role != "r1" || v.Resource != "account" || v.Action != "read" {
		t.Fatalf("the view = %+v, want tenant t1, role r1, pair (account, read)", v)
	}
}

// TestGrantingASecondPermissionUsesItsOwnTranslation holds that the write
// translates each permission by the shared table and does not hardcode one pair:
// a second permission whose translation differs from the first must reach the
// manager as its own pair, so an implementation that hardcoded the first pair
// fails the second.
func TestGrantingASecondPermissionUsesItsOwnTranslation(t *testing.T) {
	object, action, ok := translation.Lookup(authz.PermissionSessionRead)
	if !ok {
		t.Fatal("authn.session.read has no translation row, so the case cannot hold the pair")
	}
	r := newRig(t, authz.PermissionSessionRead)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.session.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionSessionRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("granting a second permission answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutGrant" {
		t.Fatalf("the manager calls = %+v, want one PutGrant", got)
	}
	if got[0].grant.Resource != object || got[0].grant.Action != action {
		t.Fatalf("the grant pair = (%q, %q), want (%q, %q) from the translation",
			got[0].grant.Resource, got[0].grant.Action, object, action)
	}
}

// TestTheGrantPutMandatoryRowCarriesTheObjectAndDetail holds the put audit row's
// object and detail: the object is the permission and the detail names the
// tenant, the role and the permission. Asserting only the action, the outcome
// and the action-id link lets the detail be swapped or emptied.
func TestTheGrantPutMandatoryRowCarriesTheObjectAndDetail(t *testing.T) {
	r := newRig(t, authz.PermissionAccountRead)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read",
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("granting a permission answered %d, want 204", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit rows = %+v, want one", rows)
	}
	if rows[0].Object != string(authz.PermissionAccountRead) {
		t.Fatalf("the mandatory row's object = %q, want the permission", rows[0].Object)
	}
	if rows[0].Detail[detailTenant] != "t1" || rows[0].Detail[detailRole] != "r1" ||
		rows[0].Detail[detailPermission] != string(authz.PermissionAccountRead) {
		t.Fatalf("the mandatory row's detail = %+v, want tenant t1, role r1, the permission", rows[0].Detail)
	}
}

// TestGrantingAPermissionTheTranslationLacksIsRefused holds the write's second
// shape guard: a permission the catalog declares but the translation has no row
// for is a 400 before the manager, so the write never reaches the manager with
// an empty pair.
func TestGrantingAPermissionTheTranslationLacksIsRefused(t *testing.T) {
	r := newRig(t, authz.Permission("vendor.custom.read")) // declared, but absent from the translation
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/authz/admin/tenants/t1/roles/r1/grants/vendor.custom.read",
		params("tenant", "t1", "role", "r1", "permission", "vendor.custom.read"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("granting a permission with no translation row answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a permission with no translation row", got)
	}
}

// TestAnonymousRequestsAreRefusedOnEveryGrantRoute holds the caller check on the
// grant routes: each answers 401 before the guard is asked and before the
// manager is called. Without this the check could be dropped on the list or the
// delete — turning the module's 401 into the guard's 403 — while the write's own
// case still passed.
func TestAnonymousRequestsAreRefusedOnEveryGrantRoute(t *testing.T) {
	cases := map[string]struct {
		handler func(*product) gin.HandlerFunc
		method  string
		path    string
		params  gin.Params
	}{
		"the list":  {func(p *product) gin.HandlerFunc { return readRoute(p, grantListOperation.Handle(p.handleGrantList)) }, http.MethodGet, "/api/v1/authz/admin/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1")},
		"the write": {func(p *product) gin.HandlerFunc { return writeRoute(p, grantPutOperation.Handle(p.handleGrantPut)) }, http.MethodPut, "/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read", params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead))},
		"the delete": {func(p *product) gin.HandlerFunc {
			return writeRoute(p, grantDeleteOperation.Handle(p.handleGrantDelete))
		}, http.MethodDelete, "/api/v1/authz/admin/tenants/t1/roles/r1/grants/authn.account.read", params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionAccountRead))},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionAccountRead)
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
