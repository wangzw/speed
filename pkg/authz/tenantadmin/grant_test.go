package tenantadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

// TestAnUnauthorisedGrantDeleteReachesNoManagerAndWritesNoAudit checks that an unauthorised grant delete reaches no manager and writes no audit.
func TestAnUnauthorisedGrantDeleteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.guard.allow = false
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised grant delete answered %d, want 403", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionTenantRoleManage {
		t.Fatalf("the delete asked the guard for %v, want [authz.tenant.role.manage]", got)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v on an unauthorised delete", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Fatalf("an audit row was written %v on an unauthorised delete", got)
	}
}

// TestDeletingAGrantAsksForTenantRoleManageAndLandsOneSuccessRow checks that deleting a grant asks for tenant role manage and lands one success row.
func TestDeletingAGrantAsksForTenantRoleManageAndLandsOneSuccessRow(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting a grant answered %d, want 204", w.Code)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteGrant" {
		t.Fatalf("the manager calls = %+v, want one DeleteGrant", got)
	}
	object, action, _ := translation.Lookup(authz.PermissionTenantRoleRead)
	if got[0].grant.Tenant != "t1" || string(got[0].grant.Role) != "r1" ||
		got[0].grant.Resource != object || got[0].grant.Action != action {
		t.Fatalf("the delete carried %+v, want tenant t1 role r1 pair (%q, %q)", got[0].grant, object, action)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionGrantDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionGrantDelete)
	}
	if rows[0].Object != string(authz.PermissionTenantRoleRead) {
		t.Fatalf("the mandatory row's object = %q, want the permission", rows[0].Object)
	}
}

// TestDeletingAGrantWhoseActionFailsLinksAFailedRow checks that deleting a grant whose action fails links a failed row.
func TestDeletingAGrantWhoseActionFailsLinksAFailedRow(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
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

// TestADeleteTheManagerRefusesIsMapped checks that a delete the manager refuses is mapped.
func TestADeleteTheManagerRefusesIsMapped(t *testing.T) {
	t.Run("a builtin role is a 409", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.fail(authz.ErrBuiltinRole)
		w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
			"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("deleting a grant on a builtin role answered %d, want 409", w.Code)
		}
	})
	t.Run("a store failure is a 503", func(t *testing.T) {
		r := newRig(t, authz.PermissionTenantRoleRead)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
			"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
			params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a grant delete whose store failed answered %d, want 503", w.Code)
		}
	})
}

// TestDeletingAGrantWithoutATranslationRowIsRefused checks that deleting a grant without a translation row is refused.
func TestDeletingAGrantWithoutATranslationRowIsRefused(t *testing.T) {
	r := newRig(t, authz.Permission("authn.unknown.thing"))
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/r1/grants/authn.unknown.thing",
		params("tenant", "t1", "role", "r1", "permission", "authn.unknown.thing"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("deleting a grant with no translation row answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a permission with no translation row", got)
	}
}

// TestDeletingAGrantDoesNotRequireTheGovernedSet checks that deleting a grant does not require the governed set.
func TestDeletingAGrantDoesNotRequireTheGovernedSet(t *testing.T) {
	r := newRig(t, authz.PermissionSessionRead)
	w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionSessionRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionSessionRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting an ungoverned grant answered %d, want 204: the governed rule is the write's", w.Code)
	}
	if got := r.catalog.governedAsked(); len(got) != 0 {
		t.Fatalf("the delete asked the governed set %v, want no question", got)
	}
}

// TestAListingGrantsWhoseManagerFailsIsMapped checks that a listing grants whose manager fails is mapped.
func TestAListingGrantsWhoseManagerFailsIsMapped(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a grant list whose manager failed answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "grants") {
		t.Fatalf("the refusal %q carries a grants view, want none", w.Body.String())
	}
}

// TestTheGrantListViewCarriesEveryField checks that the grant list view carries every field.
func TestTheGrantListViewCarriesEveryField(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.manager.grants = []authz.GrantRecord{{Tenant: "t1", Role: "r1", Resource: "tenant_role", Action: "read"}}
	w := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the grants answered %d, want 200", w.Code)
	}
	var got struct {
		Grants []AuthzTenantAdminGrantView `json:"grants"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Grants) != 1 {
		t.Fatalf("the answer carries %d views, want one", len(got.Grants))
	}
	v := got.Grants[0]
	if v.Tenant != "t1" || v.Role != "r1" || v.Resource != "tenant_role" || v.Action != "read" {
		t.Fatalf("the view = %+v, want tenant t1, role r1, pair (tenant_role, read)", v)
	}
}

// TestGrantingAGovernedPermissionTheCallerHoldsWritesIt checks that granting a governed permission the caller holds writes it.
func TestGrantingAGovernedPermissionTheCallerHoldsWritesIt(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("granting a governed permission answered %d, want 204; body = %q", w.Code, w.Body.String())
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "PutGrant" {
		t.Fatalf("the manager calls = %+v, want one PutGrant", got)
	}
	object, action, _ := translation.Lookup(authz.PermissionTenantRoleRead)
	if got[0].grant.Resource != object || got[0].grant.Action != action {
		t.Fatalf("the grant pair = (%q, %q), want (%q, %q) from the translation",
			got[0].grant.Resource, got[0].grant.Action, object, action)
	}
	domains, subjects := r.subject.asked()
	if len(domains) != 1 || domains[0] != "t1" {
		t.Fatalf("the held set was read in %v, want [t1]", domains)
	}
	if len(subjects) != 1 || subjects[0] == "" {
		t.Fatalf("the held set was read for %v, want the caller's account", subjects)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Object != string(authz.PermissionTenantRoleRead) ||
		rows[0].Detail[detailTenant] != "t1" || rows[0].Detail[detailRole] != "r1" ||
		rows[0].Detail[detailPermission] != string(authz.PermissionTenantRoleRead) {
		t.Fatalf("the mandatory row = %+v, want the permission with tenant, role and permission detail", rows)
	}
}

// TestGrantingASecondPermissionUsesItsOwnTranslation checks that granting a second permission uses its own translation.
func TestGrantingASecondPermissionUsesItsOwnTranslation(t *testing.T) {
	object, action, ok := translation.Lookup(authz.PermissionTenantRoleManage)
	if !ok {
		t.Fatal("authz.tenant.role.manage has no translation row")
	}
	r := newRig(t, authz.PermissionTenantRoleManage)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleManage),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleManage)), nil)
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

// TestGrantingAPermissionTheTranslationLacksIsRefused checks that granting a permission the translation lacks is refused.
func TestGrantingAPermissionTheTranslationLacksIsRefused(t *testing.T) {
	const permission = authz.Permission("vendor.custom.read")
	r := newRig(t, permission)
	r.catalog.govern(permission)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/vendor.custom.read",
		params("tenant", "t1", "role", "r1", "permission", "vendor.custom.read"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("granting a permission with no translation row answered %d, want 400", w.Code)
	}
	if got := r.manager.recorded(); len(got) != 0 {
		t.Fatalf("the manager was called %v for a permission with no translation row", got)
	}
}

// TestAnonymousRequestsAreRefusedOnEveryGrantRoute checks that anonymous requests are refused on every grant route.
func TestAnonymousRequestsAreRefusedOnEveryGrantRoute(t *testing.T) {
	cases := map[string]struct {
		handler func(*product) gin.HandlerFunc
		method  string
		path    string
		params  gin.Params
	}{
		"the list":  {func(p *product) gin.HandlerFunc { return readRoute(p, grantListOperation.Handle(p.handleGrantList)) }, http.MethodGet, "/api/v1/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1")},
		"the write": {func(p *product) gin.HandlerFunc { return writeRoute(p, grantPutOperation.Handle(p.handleGrantPut)) }, http.MethodPut, "/api/v1/tenants/t1/roles/r1/grants/authz.tenant.role.read", params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead))},
		"the delete": {func(p *product) gin.HandlerFunc {
			return writeRoute(p, grantDeleteOperation.Handle(p.handleGrantDelete))
		}, http.MethodDelete, "/api/v1/tenants/t1/roles/r1/grants/authz.tenant.role.read", params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead))},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead)
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

// TestTheGrantViewCarriesThePermissionCode checks that the grant view carries the permission code.
func TestTheGrantViewCarriesThePermissionCode(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	object, action, ok := translation.Lookup(authz.PermissionTenantRoleManage)
	if !ok {
		t.Fatal("authz.tenant.role.manage has no translation row, so the case cannot hold its pair")
	}
	r.manager.grants = []authz.GrantRecord{
		{Tenant: "t1", Role: "r1", Resource: object, Action: action},
		{Tenant: "t1", Role: "r1", Resource: "retired_object", Action: "retired_action"},
	}

	w := r.serve(readRoute(r.p, grantListOperation.Handle(r.p.handleGrantList)), http.MethodGet,
		"/api/v1/tenants/t1/roles/r1/grants", params("tenant", "t1", "role", "r1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the grants answered %d, want 200; body %s", w.Code, w.Body.String())
	}
	var got struct {
		Grants []map[string]json.RawMessage `json:"grants"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Grants) != 2 {
		t.Fatalf("the answer carries %d views, want two", len(got.Grants))
	}

	raw, present := got.Grants[0]["permission"]
	if !present {
		t.Fatal("the grant view carries no permission field")
	}
	var permission string
	if err := json.Unmarshal(raw, &permission); err != nil {
		t.Fatalf("decoding the permission field: %v", err)
	}
	if want := string(authz.PermissionTenantRoleManage); permission != want {
		t.Errorf("the grant's permission = %q, want %q", permission, want)
	}
	for _, key := range []string{"resource", "action"} {
		if _, ok := got.Grants[0][key]; !ok {
			t.Errorf("the grant view carries no %s field", key)
		}
	}

	raw, present = got.Grants[1]["permission"]
	if !present {
		t.Fatal("a grant with no translated pair carries no permission field, want the empty field")
	}
	permission = "not empty"
	if err := json.Unmarshal(raw, &permission); err != nil {
		t.Fatalf("decoding the unknown pair's permission field: %v", err)
	}
	if permission != "" {
		t.Errorf("a grant with no translated pair carries permission %q, want the empty string", permission)
	}
}
