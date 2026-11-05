package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
)

// TestTheRoleListViewCountsActiveMembers holds the member count: it counts the
// active accounts holding each role, leaves out an account the host does not
// know, and reads the identities of every bound account in one batch. A role
// with no active member is zero rather than null; the null value is reserved for
// an assembly with no identity reader.
func TestTheRoleListViewCountsActiveMembers(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{
		{Tenant: "t1", Role: "editor"},
		{Tenant: "t1", Role: "viewer"},
	}
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-a", Role: "editor"},
		{Tenant: "t1", Account: "acct-b", Role: "editor"},
		{Tenant: "t1", Account: "acct-c", Role: "editor"},
		{Tenant: "t1", Account: "acct-a", Role: "viewer"},
	}
	reader := &stubIdentityReader{}
	reader.answer(map[string]authz.AccountIdentity{
		"acct-a": {DisplayName: "Alice", Active: true},
		"acct-b": {DisplayName: "Bob", Active: false},
		// acct-c is unknown to the host.
	})
	r.withIdentities(reader)

	views := listRoles(t, r)
	byRole := make(map[string]map[string]any, len(views))
	for _, view := range views {
		byRole[view["role"].(string)] = view
	}
	if got := byRole["editor"]["member_count"]; got != float64(1) {
		t.Errorf("editor member_count = %v, want 1: only the active account counts", got)
	}
	if got := byRole["viewer"]["member_count"]; got != float64(1) {
		t.Errorf("viewer member_count = %v, want 1", got)
	}
	asked := reader.asked()
	if len(asked) != 1 || asked[0].tenant != "t1" {
		t.Fatalf("the identity reads = %+v, want one read for tenant t1", asked)
	}
	if len(asked[0].accounts) != 3 {
		t.Errorf("the batch read named %v, want every bound account once", asked[0].accounts)
	}
}

// TestTheMemberCountIsNullWithoutAnIdentityReader holds the absent seam: the
// count cannot be read, so the view publishes null and the binding count is not
// used as a stand-in.
func TestTheMemberCountIsNullWithoutAnIdentityReader(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.bindings = []authz.Binding{
		{Tenant: "t1", Account: "acct-a", Role: "editor"},
	}
	views := listRoles(t, r)
	if got, ok := views[0]["member_count"]; !ok || got != nil {
		t.Fatalf("member_count = %v (present %v), want null", got, ok)
	}
	for _, call := range r.manager.recorded() {
		if call.op == "Bindings" {
			t.Fatalf("the handler read the bindings with no identity reader: %+v", call)
		}
	}
}

// TestARoleListWhoseIdentityReadFailsIsA503 holds the failure rule: an identity
// read that cannot answer is a 503 with no role list, never a count of zero and
// never an inactive set. A count of zero would tell a client that no member
// holds the role, which is a conclusion the fault does not support.
func TestARoleListWhoseIdentityReadFailsIsA503(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.manager.bindings = []authz.Binding{{Tenant: "t1", Account: "acct-a", Role: "editor"}}
	reader := &stubIdentityReader{}
	reader.fail(errors.New("the account store is down"))
	r.withIdentities(reader)

	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable identity answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "roles") {
		t.Fatalf("the refusal %q carries a role list, want none", w.Body.String())
	}
}

// TestCanManageFollowsTheCallersPlatformPermissions holds the hint: the role
// list reads the caller's held permissions in the platform domain and reports
// authz.role.manage. A caller holding only the read is false; a caller holding
// the write is true.
func TestCanManageFollowsTheCallersPlatformPermissions(t *testing.T) {
	t.Run("the read alone is false", func(t *testing.T) {
		r := newRig(t)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
		r.subject.answer(authz.PermissionRoleRead)
		if got := listRoles(t, r)[0]["can_manage"]; got != false {
			t.Fatalf("can_manage = %v, want false for a caller holding role.read only", got)
		}
	})
	t.Run("the write is true", func(t *testing.T) {
		r := newRig(t)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
		r.subject.answer(authz.PermissionRoleRead, authz.PermissionRoleManage)
		if got := listRoles(t, r)[0]["can_manage"]; got != true {
			t.Fatalf("can_manage = %v, want true for a caller holding role.manage", got)
		}
	})
	t.Run("the read asks in the platform domain for the effective account", func(t *testing.T) {
		r := newRig(t)
		r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
		listRoles(t, r)
		asked := r.subject.asked()
		if len(asked) != 1 {
			t.Fatalf("the permission read was made %d times, want once", len(asked))
		}
		if asked[0].domain != authz.PlatformDomain || asked[0].subject != string(authntest.DefaultAccount) {
			t.Fatalf("the permission read asked %+v, want the platform domain and the request's account", asked[0])
		}
	})
}

// TestAnUnreadableCallerPermissionSetIsA503 holds the failure rule: a permission
// read that cannot answer is a 503, never can_manage=false. Hiding the controls
// on a fault would tell the client a caller may not manage roles when the answer
// was simply not readable.
func TestAnUnreadableCallerPermissionSetIsA503(t *testing.T) {
	r := newRig(t)
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	r.subject.fail(errors.New("the policy snapshot is unavailable"))
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable permission set answered %d, want 503", w.Code)
	}
}

// grantCountsFailingManager fails the grouped grant read the role list makes,
// so a case holds that a failure there is mapped like any other manager failure.
type grantCountsFailingManager struct {
	*stubManager
}

func (grantCountsFailingManager) GrantCounts(context.Context, string) (map[authz.Role]int, error) {
	return nil, errors.New("the grant table is down")
}

// TestAFailedGrantCountIsA503 holds the permission count's failure rule: a
// grouped read that cannot answer is a 503 with no list, never a count of zero.
func TestAFailedGrantCountIsA503(t *testing.T) {
	r := newRig(t)
	r.p.manager = grantCountsFailingManager{stubManager: r.manager}
	r.manager.roles = []authz.RoleRecord{{Tenant: "t1", Role: "editor"}}
	w := r.serve(readRoute(r.p, roleListOperation.Handle(r.p.handleRoleList)), http.MethodGet,
		"/api/v1/system/tenants/t1/roles", params("tenant", "t1"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a failed grant count answered %d, want 503", w.Code)
	}
	if hasKey(t, w.Body.String(), "roles") {
		t.Fatalf("the refusal %q carries a role list, want none", w.Body.String())
	}
}
