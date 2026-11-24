package tenantadmin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
)

// TestAnUnauthorisedRoleDeleteReachesNoManagerAndWritesNoAudit checks that an unauthorised role delete reaches no manager and writes no audit.
func TestAnUnauthorisedRoleDeleteReachesNoManagerAndWritesNoAudit(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor"), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised role delete answered %d, want 403", w.Code)
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

// TestDeletingARoleAsksForTenantRoleManageAndLandsOneSuccessRow checks that deleting a role asks for tenant role manage and lands one success row.
func TestDeletingARoleAsksForTenantRoleManageAndLandsOneSuccessRow(t *testing.T) {
	r := newRig(t)
	w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete,
		"/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleting a role answered %d, want 204", w.Code)
	}
	if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionTenantRoleManage {
		t.Fatalf("the delete asked the guard for %v, want [authz.tenant.role.manage]", got)
	}
	got := r.manager.recorded()
	if len(got) != 1 || got[0].op != "DeleteRole" {
		t.Fatalf("the manager calls = %+v, want one DeleteRole", got)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionRoleDelete || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the audit rows = %+v, want one success row for %s", rows, actionRoleDelete)
	}
	if rows[0].Object != "editor" {
		t.Fatalf("the mandatory row's object = %q, want the role editor", rows[0].Object)
	}
}

// TestARoleListWhoseManagerFailsIsMapped checks that a role list whose manager fails is mapped.
func TestARoleListWhoseManagerFailsIsMapped(t *testing.T) {
	t.Run("a tenant mismatch is a 400", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrTenantMismatch)
		w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
			"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a role list whose manager refused the tenant answered %d, want 400", w.Code)
		}
	})
	t.Run("any other failure is a 503 with no list", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(errors.New("the store is down"))
		w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
			"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a role list whose manager failed answered %d, want 503", w.Code)
		}
		if hasKey(t, w.Body.String(), "roles") {
			t.Fatalf("the refusal %q carries a roles view, want none", w.Body.String())
		}
	})
}

type grantCountsFailingManager struct {
	*stubManager
}

func (grantCountsFailingManager) GrantCounts(context.Context, string) (map[authz.Role]int, error) {
	return nil, errors.New("the grant table is down")
}

// TestAFailedGrantCountIsA503 checks that a failed grant count is a 503.
func TestAFailedGrantCountIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.p.manager = grantCountsFailingManager{stubManager: r.manager}
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a failed grant count answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "roles") {
		t.Fatalf("the refusal %q carries a role list, want none", w.Body.String())
	}
}

// TestAnonymousRequestsAreRefusedOnEveryRoleRoute checks that anonymous requests are refused on every role route.
func TestAnonymousRequestsAreRefusedOnEveryRoleRoute(t *testing.T) {
	cases := map[string]struct {
		handler func(*product) gin.HandlerFunc
		method  string
		path    string
		params  gin.Params
	}{
		"the list":   {func(p *product) gin.HandlerFunc { return readRoute(p, roleListOperation.Handle(p.handleRoleList)) }, http.MethodGet, "/api/v1/tenants/t1/roles", params("tenant", "t1")},
		"the write":  {func(p *product) gin.HandlerFunc { return rolePutRoute(p) }, http.MethodPut, "/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor")},
		"the delete": {func(p *product) gin.HandlerFunc { return writeRoute(p, roleDeleteOperation.Handle(p.handleRoleDelete)) }, http.MethodDelete, "/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor")},
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
			if got := r.audit.entries(); len(got) != 0 {
				t.Fatalf("an audit row was written %v for an anonymous request", got)
			}
		})
	}
}

// TestARolePutFailureLeavesAFailedRow checks that a role put failure leaves a failed row.
func TestARolePutFailureLeavesAFailedRow(t *testing.T) {
	r := newRig(t)
	r.manager.fail(errors.New("the store is down"))
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a put whose action failed answered %d, want 503", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 2 {
		t.Fatalf("the audit rows = %+v, want the mandatory row and the failed one", rows)
	}
	if rows[0].Action != actionRolePut || rows[0].Outcome != outcomeSuccess {
		t.Fatalf("the mandatory row = %+v, want a success row for %s", rows[0], actionRolePut)
	}
	if rows[1].Action != actionRolePut || rows[1].Outcome != outcomeFailed {
		t.Fatalf("the failed row = %+v, want a failed row for %s", rows[1], actionRolePut)
	}
	id := rows[0].Detail[detailActionID]
	if id == "" || rows[1].Detail[detailActionID] != id {
		t.Fatalf("the rows are not linked by the action id: %q vs %q", id, rows[1].Detail[detailActionID])
	}
}

// TestTheRoleListViewCarriesEveryField checks that the role list view carries every field.
func TestTheRoleListViewCarriesEveryField(t *testing.T) {
	created := time.Date(2021, 6, 7, 8, 9, 10, 0, time.FixedZone("plus-one", 3600))
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor", Builtin: true, DescriptionKey: "authz.role.editor", CreatedAt: created},
	}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200", w.Code)
	}
	var got struct {
		Roles []AuthzTenantAdminRoleView `json:"roles"`
	}
	decodeInto(t, w.Body.String(), &got)
	if len(got.Roles) != 1 {
		t.Fatalf("the answer carries %d views, want one", len(got.Roles))
	}
	v := got.Roles[0]
	if v.Tenant != "t1" || v.Role != "editor" || !v.Builtin {
		t.Fatalf("the view = %+v, want tenant t1, role editor, builtin true", v)
	}
	if v.Description != "authz.role.editor" {
		t.Fatalf("the view's description = %q, want the key itself with no localizer", v.Description)
	}
	if !v.CreatedAt.Equal(created) || v.CreatedAt.Location() != time.UTC {
		t.Fatalf("the view's created_at = %v (%v), want the same instant in UTC", v.CreatedAt, v.CreatedAt.Location())
	}
}

// TestARoleWriteAuditCarriesTheObjectAndDetail checks that a role write audit carries the object and detail.
func TestARoleWriteAuditCarriesTheObjectAndDetail(t *testing.T) {
	r := newRig(t)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusNoContent {
		t.Fatalf("writing a role answered %d, want 204", w.Code)
	}
	rows := r.audit.entries()
	if len(rows) != 1 {
		t.Fatalf("the audit rows = %+v, want one", rows)
	}
	if rows[0].Object != "editor" {
		t.Fatalf("the mandatory row's object = %q, want the role editor", rows[0].Object)
	}
	if rows[0].Detail[detailTenant] != "t1" || rows[0].Detail[detailRole] != "editor" ||
		rows[0].Detail[detailChange] != roleChange {
		t.Fatalf("the mandatory row's detail = %+v, want tenant t1, role editor, change %s", rows[0].Detail, roleChange)
	}
}

// TestAMalformedRoleBodyIsRefused checks that a malformed role body is refused.
func TestAMalformedRoleBodyIsRefused(t *testing.T) {
	for _, raw := range []string{`{"builtin":`, `[1, 2, 3]`, `not json at all`} {
		t.Run(raw, func(t *testing.T) {
			r := newRig(t)
			w := r.serveRaw(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
				params("tenant", "t1", "role", "editor"), raw)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("a malformed body answered %d, want 400", w.Code)
			}
			if got := r.manager.recorded(); len(got) != 0 {
				t.Fatalf("the manager was called %v for a malformed body", got)
			}
			if got := r.audit.entries(); len(got) != 0 {
				t.Fatalf("an audit row was written %v for a malformed body", got)
			}
		})
	}
}

// TestTheRoleRefusalProblemTypes checks that the role refusal problem types.
func TestTheRoleRefusalProblemTypes(t *testing.T) {
	t.Run("a 400 is the bad-request code", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrUnknownRole)
		w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/ghost",
			params("tenant", "t1", "role", "ghost"), roleBody{})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("the answer = %d, want 400", w.Code)
		}
		if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:bad-request" {
			t.Fatalf("the 400's type = %q, want urn:speed:authz:bad-request", p.Type)
		}
	})
	t.Run("a 409 is the conflict code", func(t *testing.T) {
		r := newRig(t)
		r.manager.fail(authz.ErrBuiltinRole)
		w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete,
			"/api/v1/tenants/t1/roles/editor", params("tenant", "t1", "role", "editor"), nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("the answer = %d, want 409", w.Code)
		}
		if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:conflict" {
			t.Fatalf("the 409's type = %q, want urn:speed:authz:conflict", p.Type)
		}
	})
}

// TestAnAnonymousRoleRequestWritesA401WithoutAType checks that an anonymous role request writes a 401 without a type.
func TestAnAnonymousRoleRequestWritesA401WithoutAType(t *testing.T) {
	r := newRig(t)
	w := r.serveAnonymous(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous request answered %d, want 401", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); strings.HasPrefix(p.Type, "urn:speed:authz:") {
		t.Fatalf("the 401's type = %q, want none of this capability's codes", p.Type)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != realmChallenge("api") {
		t.Fatalf("WWW-Authenticate = %q, want the realm challenge %q", got, realmChallenge("api"))
	}
}
