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

// TestAnUnauthorisedGrantDeleteReachesNoManagerAndWritesNoAudit holds the grant
// delete route's decision.
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

// TestDeletingAGrantAsksForTenantRoleManageAndLandsOneSuccessRow holds the
// delete route's permission, its translated pair and its single audit row.
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

// TestDeletingAGrantWhoseActionFailsLinksAFailedRow holds the delete's audit
// order.
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

// TestADeleteTheManagerRefusesIsMapped holds the delete route's error mapping.
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

// TestDeletingAGrantWithoutATranslationRowIsRefused holds the delete route's
// translation guard.
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

// TestDeletingAGrantDoesNotRequireTheGovernedSet holds that the governed-set
// rule belongs to the grant write: a permission that has left the set can still
// be revoked.
func TestDeletingAGrantDoesNotRequireTheGovernedSet(t *testing.T) {
	r := newRig(t, authz.PermissionSessionRead) // declared and translated, but not governed
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

// TestAListingGrantsWhoseManagerFailsIsMapped holds the grant read's failure
// path.
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

// TestTheGrantListViewCarriesEveryField holds the grant list's response shape.
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

// TestGrantingAGovernedPermissionTheCallerHoldsWritesIt holds the write's happy
// path: the governed permission is translated and written, one success row
// lands, and the caller's held set is read in the tenant domain.
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

// TestGrantingASecondPermissionUsesItsOwnTranslation holds that the write
// translates each permission by the shared table.
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

// TestGrantingAPermissionTheTranslationLacksIsRefused holds the write's second
// shape guard.
func TestGrantingAPermissionTheTranslationLacksIsRefused(t *testing.T) {
	const permission = authz.Permission("vendor.custom.read")
	r := newRig(t, permission) // declared and governed, but absent from the translation
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

// TestAnonymousRequestsAreRefusedOnEveryGrantRoute holds the caller check on the
// grant routes.
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

// TestTheGrantViewCarriesThePermissionCode holds the grant view the tenant
// administration face reads: a grant written in the permission vocabulary comes
// back with that permission code, so a client can name and edit it without
// translating the engine's (resource, action) pair. A grant whose pair no
// declared permission translates to still comes back, with an empty permission
// and its pair, so an operator can diagnose a stale row rather than losing it.
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
	// The engine's pair stays on the view for diagnosis.
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
