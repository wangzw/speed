package tenantadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// rolesByRole decodes the role list answer and indexes its views by role name.
func rolesByRole(t *testing.T, body string) map[string]map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]map[string]json.RawMessage)
	for _, view := range roleViews(t, body) {
		out[viewString(t, view, "role")] = view
	}
	return out
}

// TestTheRoleListCountsOnlyActiveMembers holds the member count's measure: one
// tenant-wide binding read, one batch identity read, and only the accounts the
// reader reports active are counted.
func TestTheRoleListCountsOnlyActiveMembers(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.withIdentities()
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor"},
		{Tenant: "t1", Role: "viewer"},
		{Tenant: "t1", Role: "unused"},
	}
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-1", Role: "editor"},
		{Tenant: "t1", Account: "acct-2", Role: "editor"},
		{Tenant: "t1", Account: "acct-ghost", Role: "editor"},
		{Tenant: "t1", Account: "acct-3", Role: "viewer"},
	}
	r.identities.answer(map[string]authz.AccountIdentity{
		"acct-1": {DisplayName: "Alice", Active: true},
		"acct-2": {DisplayName: "Bob", Active: false},
		"acct-3": {DisplayName: "Carol", Active: true},
	})
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	views := rolesByRole(t, w.Body.String())
	if got := viewInt(t, views["editor"], "member_count"); got != 1 {
		t.Errorf("editor's member count = %d, want the one active holder", got)
	}
	if got := viewInt(t, views["viewer"], "member_count"); got != 1 {
		t.Errorf("viewer's member count = %d, want the one active holder", got)
	}
	if got := viewInt(t, views["unused"], "member_count"); got != 0 {
		t.Errorf("a role with no binding counts %d active members, want zero", got)
	}
	if got := r.identities.readCount(); got != 1 {
		t.Errorf("the identity reader was read %d times, want one batch read", got)
	}
	asked := r.identities.lastAsked()
	if len(asked) != 4 {
		t.Errorf("the identity read named %v, want the four distinct bound accounts", asked)
	}
	seen := make(map[string]bool, len(asked))
	for _, account := range asked {
		if seen[account] {
			t.Errorf("the identity read names %q twice", account)
		}
		seen[account] = true
	}
}

// TestTheRoleListMemberCountIsUnknownWithoutAReader holds the absent seam's
// degradation: a tenant with bindings still reports an unknown count, not a
// count of zero.
func TestTheRoleListMemberCountIsUnknownWithoutAReader(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the roles answered %d, want 200", w.Code)
	}
	view := rolesByRole(t, w.Body.String())["editor"]
	if !viewIsNull(t, view, "member_count") {
		t.Errorf("the member count = %s, want null with no identity reader", view["member_count"])
	}
}

// TestTheRoleListWhoseIdentityReaderFailsIsA503 holds the error rule: an
// identity read that cannot answer fails the whole role list, never a count of
// zero.
func TestTheRoleListWhoseIdentityReaderFailsIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.withIdentities()
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
	r.identities.fail(errors.New("the account store is down"))
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a role list whose identity read failed answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "roles") {
		t.Fatalf("the refusal %q carries a role list, want none", w.Body.String())
	}
}

// TestTheRoleListWhoseHeldSetCannotBeReadIsA503 holds can_manage's failure
// rule: the caller's held set is read for the field, and an unreadable one is a
// 503 rather than a list that says the caller may manage nothing.
func TestTheRoleListWhoseHeldSetCannotBeReadIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.subject.fail(errors.New("the policy snapshot is unavailable"))
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a role list whose held set could not be read answered %d, want 503", w.Code)
	}
}

// TestThePermissionCatalogWhoseHeldSetCannotBeReadIsA503 holds grantable's
// failure rule.
func TestThePermissionCatalogWhoseHeldSetCannotBeReadIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.subject.fail(errors.New("the policy snapshot is unavailable"))
	w := r.serve(readRoute(r.p, permissionListOperation.Handle(r.p.handlePermissionList)), http.MethodGet,
		"/api/v1/tenants/t1/permissions", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a catalog read whose held set could not be read answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "permissions") {
		t.Fatalf("the refusal %q carries a permission list, want none", w.Body.String())
	}
}

// TestTheRoleBindingPageReportsIdentityAndCursors holds the role usage read's
// page: the role filter, the account cursor, the identity fields and the
// unknown identity state.
func TestTheRoleBindingPageReportsIdentityAndCursors(t *testing.T) {
	first := time.Date(2024, 5, 6, 7, 8, 9, 0, time.FixedZone("plus-one", 3600))
	r := newRig(t)
	r.withIdentities()
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-1", Role: "editor", GrantedAt: first},
		{Tenant: "t1", Account: "acct-2", Role: "editor", GrantedAt: first},
		{Tenant: "t1", Account: "acct-3", Role: "editor", GrantedAt: first},
		{Tenant: "t1", Account: "acct-9", Role: "viewer", GrantedAt: first},
	}
	active := true
	inactive := false
	r.identities.answer(map[string]authz.AccountIdentity{
		"acct-1": {DisplayName: "Alice", Active: active},
		"acct-2": {DisplayName: "Bob", Active: inactive},
	})
	e := r.engine()

	var page struct {
		Bindings   []AuthzTenantAdminRoleBindingView `json:"bindings"`
		NextCursor string                            `json:"next_cursor"`
	}
	w := r.send(e, http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings?limit=2")
	if w.Code != http.StatusOK {
		t.Fatalf("the role bindings route answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	decodeInto(t, w.Body.String(), &page)
	if len(page.Bindings) != 2 {
		t.Fatalf("the first page carries %d rows, want two", len(page.Bindings))
	}
	if page.NextCursor != "acct-2" {
		t.Errorf("the first page's next_cursor = %q, want acct-2", page.NextCursor)
	}
	if page.Bindings[0].Account != "acct-1" || page.Bindings[0].DisplayName != "Alice" {
		t.Errorf("the first row = %+v, want acct-1 named Alice", page.Bindings[0])
	}
	if page.Bindings[0].Active == nil || !*page.Bindings[0].Active {
		t.Errorf("the first row's active = %v, want true", page.Bindings[0].Active)
	}
	if !page.Bindings[0].GrantedAt.Equal(first) || page.Bindings[0].GrantedAt.Location() != time.UTC {
		t.Errorf("the first row's granted_at = %v, want the same instant in UTC", page.Bindings[0].GrantedAt)
	}
	if page.Bindings[1].Active == nil || *page.Bindings[1].Active {
		t.Errorf("the second row's active = %v, want false", page.Bindings[1].Active)
	}

	w = r.send(e, http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings?after=acct-2&limit=2")
	if w.Code != http.StatusOK {
		t.Fatalf("the second page answered %d, want 200; body = %q", w.Code, w.Body.String())
	}
	page = struct {
		Bindings   []AuthzTenantAdminRoleBindingView `json:"bindings"`
		NextCursor string                            `json:"next_cursor"`
	}{}
	decodeInto(t, w.Body.String(), &page)
	if len(page.Bindings) != 1 || page.Bindings[0].Account != "acct-3" {
		t.Fatalf("the second page = %+v, want the one remaining editor binding", page.Bindings)
	}
	if page.NextCursor != "" {
		t.Errorf("the last page's next_cursor = %q, want empty", page.NextCursor)
	}
	if page.Bindings[0].DisplayName != "" || page.Bindings[0].Active != nil {
		t.Errorf("an account the reader does not know = %+v, want an empty name and a null activity", page.Bindings[0])
	}

	calls := r.manager.recorded()
	last := calls[len(calls)-1]
	if last.op != "BindingsOfRole" || last.role != authz.Role("editor") || last.after != "acct-2" || last.limit != 3 {
		t.Errorf("the last manager call = %+v, want BindingsOfRole(editor, after acct-2, limit 3)", last)
	}
}

// TestTheRoleBindingPageIsGovernedReadOnly holds the route's decision and its
// shape refusals: it is a read behind authz.tenant.role.read, filters by the
// path role, refuses a negative limit, caps an oversized one and refuses the
// caller right after a failed identity read.
func TestTheRoleBindingPageIsGovernedReadOnly(t *testing.T) {
	t.Run("a refused caller reaches no manager", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings")
		if w.Code != http.StatusForbidden {
			t.Fatalf("an unauthorised read answered %d, want 403", w.Code)
		}
		if got := r.guard.askedFor(); len(got) != 1 || got[0] != authz.PermissionTenantRoleRead {
			t.Fatalf("the read asked the guard for %v, want [authz.tenant.role.read]", got)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v for a refused read", got)
		}
	})

	t.Run("an anonymous request is a 401", func(t *testing.T) {
		r := newRig(t)
		w := r.serveAnonymous(readRoute(r.p, roleBindingListOperation.Handle(r.p.handleRoleBindingList)),
			http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings", params("tenant", "t1", "role", "editor"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("an anonymous read answered %d, want 401", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v for an anonymous read", got)
		}
	})

	t.Run("the path role filters the page", func(t *testing.T) {
		r := newRig(t)
		r.manager.bindings = []authz.Binding{
			{Tenant: "t1", Account: "acct-1", Role: "editor"},
			{Tenant: "t1", Account: "acct-2", Role: "viewer"},
		}
		w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings")
		if w.Code != http.StatusOK {
			t.Fatalf("the read answered %d, want 200; body = %q", w.Code, w.Body.String())
		}
		var page struct {
			Bindings []AuthzTenantAdminRoleBindingView `json:"bindings"`
		}
		decodeInto(t, w.Body.String(), &page)
		if len(page.Bindings) != 1 || page.Bindings[0].Account != "acct-1" {
			t.Fatalf("the page = %+v, want the role's one binding", page.Bindings)
		}
	})

	t.Run("a negative limit is a 400 before the manager", func(t *testing.T) {
		r := newRig(t)
		w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings?limit=-1")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("a negative limit answered %d, want 400", w.Code)
		}
		if got := r.manager.recorded(); len(got) != 0 {
			t.Fatalf("the manager was called %v for a negative limit", got)
		}
	})

	t.Run("an oversized limit is capped", func(t *testing.T) {
		r := newRig(t)
		bindings := make([]authz.Binding, 0, maxRoleBindingPageSize+5)
		for i := 0; i < maxRoleBindingPageSize+5; i++ {
			bindings = append(bindings, authz.Binding{
				Tenant: "t1", Account: fmt.Sprintf("acct-%03d", i), Role: "editor",
			})
		}
		r.manager.bindings = bindings
		w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings?limit=1000")
		if w.Code != http.StatusOK {
			t.Fatalf("the read answered %d, want 200; body = %q", w.Code, w.Body.String())
		}
		var page struct {
			Bindings   []AuthzTenantAdminRoleBindingView `json:"bindings"`
			NextCursor string                            `json:"next_cursor"`
		}
		decodeInto(t, w.Body.String(), &page)
		if len(page.Bindings) != maxRoleBindingPageSize {
			t.Fatalf("the page carries %d rows, want the %d-row ceiling", len(page.Bindings), maxRoleBindingPageSize)
		}
		if page.NextCursor == "" {
			t.Errorf("a page with a successor carries no next_cursor")
		}
		calls := r.manager.recorded()
		last := calls[len(calls)-1]
		if last.limit != maxRoleBindingPageSize+1 {
			t.Errorf("the manager was asked for %d rows, want the ceiling plus the look-ahead row", last.limit)
		}
	})

	t.Run("an identity read that fails is a 503", func(t *testing.T) {
		r := newRig(t)
		r.withIdentities()
		r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-1", Role: "editor"}}
		r.identities.fail(errors.New("the account store is down"))
		w := r.send(r.engine(), http.MethodGet, "/api/v1/tenants/t1/roles/editor/bindings")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("a read whose identity read failed answered %d, want 503", w.Code)
		}
		if hasKey(t, w.Body.String(), "bindings") {
			t.Fatalf("the refusal %q carries a binding page, want none", w.Body.String())
		}
	})
}
