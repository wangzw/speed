package tenantadmin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// The anti-escalation rules, each with a case that holds the refusal and holds
// that nothing downstream ran: no write guard question, no audit row and no
// manager call.

// response is one answer a case drives.
type response struct{ status int }

// TestPlatformAdminIsRefusedByRoleNameUnconditionally holds the first rule: a
// write whose role segment is platform-admin is refused with the protected code
// on every route, and nothing downstream runs. The refusal does not depend on
// the tenant, the caller's permissions or the role existing.
func TestPlatformAdminIsRefusedByRoleNameUnconditionally(t *testing.T) {
	const reserved = string(authz.PlatformAdminRole)
	cases := map[string]func(*rig) *response{
		"the role write": func(r *rig) *response {
			w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/"+reserved,
				params("tenant", "t1", "role", reserved), roleBody{})
			return &response{w.Code}
		},
		"the role delete": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/roles/"+reserved, params("tenant", "t1", "role", reserved), nil)
			return &response{w.Code}
		},
		"the grant write": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
				"/api/v1/tenants/t1/roles/"+reserved+"/grants/"+string(authz.PermissionTenantRoleRead),
				params("tenant", "t1", "role", reserved, "permission", string(authz.PermissionTenantRoleRead)), nil)
			return &response{w.Code}
		},
		"the grant delete": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/roles/"+reserved+"/grants/"+string(authz.PermissionTenantRoleRead),
				params("tenant", "t1", "role", reserved, "permission", string(authz.PermissionTenantRoleRead)), nil)
			return &response{w.Code}
		},
		"the binding write": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
				"/api/v1/tenants/t1/bindings/acct-1/"+reserved,
				params("tenant", "t1", "account", "acct-1", "role", reserved), nil)
			return &response{w.Code}
		},
		"the binding delete": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/bindings/acct-1/"+reserved,
				params("tenant", "t1", "account", "acct-1", "role", reserved), nil)
			return &response{w.Code}
		},
	}
	for name, drive := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead)
			got := drive(r)
			if got.status != http.StatusForbidden {
				t.Fatalf("the write answered %d, want 403", got.status)
			}
			assertRefusedBeforeDownstream(t, r)
		})
	}
}

// TestTheTenantAdminRoleRowAndItsGrantsAreProtected holds the second rule: the
// tenant-administrator role row and its grants cannot be created, rewritten or
// deleted through this surface, and nothing downstream runs.
func TestTheTenantAdminRoleRowAndItsGrantsAreProtected(t *testing.T) {
	const reserved = string(authz.TenantAdminRole)
	cases := map[string]func(*rig) *response{
		"the role write": func(r *rig) *response {
			w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/"+reserved,
				params("tenant", "t1", "role", reserved), roleBody{})
			return &response{w.Code}
		},
		"the role delete": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, roleDeleteOperation.Handle(r.p.handleRoleDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/roles/"+reserved, params("tenant", "t1", "role", reserved), nil)
			return &response{w.Code}
		},
		"the grant write": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
				"/api/v1/tenants/t1/roles/"+reserved+"/grants/"+string(authz.PermissionTenantRoleRead),
				params("tenant", "t1", "role", reserved, "permission", string(authz.PermissionTenantRoleRead)), nil)
			return &response{w.Code}
		},
		"the grant delete": func(r *rig) *response {
			w := r.serve(writeRoute(r.p, grantDeleteOperation.Handle(r.p.handleGrantDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/roles/"+reserved+"/grants/"+string(authz.PermissionTenantRoleRead),
				params("tenant", "t1", "role", reserved, "permission", string(authz.PermissionTenantRoleRead)), nil)
			return &response{w.Code}
		},
	}
	for name, drive := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, authz.PermissionTenantRoleRead)
			got := drive(r)
			if got.status != http.StatusForbidden {
				t.Fatalf("the write answered %d, want 403", got.status)
			}
			assertRefusedBeforeDownstream(t, r)
		})
	}
}

// TestBindingTenantAdminIsAllowedSubjectToTheOtherRules holds the deliberate
// boundary: the resident tenant administrator role row is protected, but
// binding an account to it is allowed when the caller holds the whole governed
// set, the account is a member and the last-administrator rule permits it.
func TestBindingTenantAdminIsAllowedSubjectToTheOtherRules(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	// The tenant-admin role holds the whole governed set.
	r.manager.grants = []authz.GrantRecord{
		{Tenant: "t1", Role: authz.TenantAdminRole, Resource: "tenant_role", Action: "read"},
		{Tenant: "t1", Role: authz.TenantAdminRole, Resource: "tenant_role", Action: "manage"},
	}
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/tenants/t1/bindings/acct-2/"+string(authz.TenantAdminRole),
		params("tenant", "t1", "account", "acct-2", "role", string(authz.TenantAdminRole)), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("binding tenant-admin answered %d, want 204; body = %q", w.Code, w.Body.String())
	}
	calls := writes(r.manager.recorded())
	if len(calls) != 1 || calls[0].op != "PutBinding" || calls[0].role != authz.TenantAdminRole {
		t.Fatalf("the manager calls = %+v, want one PutBinding of tenant-admin", calls)
	}
}

// TestAGrantOutsideTheGovernedSetIsABadRequest holds the third rule: a grant
// write names only a governed permission. The refusal is a 400 with the
// bad-request code, because the request named a code this surface cannot grant,
// and nothing downstream runs.
func TestAGrantOutsideTheGovernedSetIsABadRequest(t *testing.T) {
	const ungoverned = authz.PermissionSessionRead // declared and translated, but not governed
	r := newRig(t, ungoverned)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(ungoverned),
		params("tenant", "t1", "role", "r1", "permission", string(ungoverned)), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("granting an ungoverned permission answered %d, want 400", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:bad-request" {
		t.Fatalf("the 400's type = %q, want urn:speed:authz:bad-request", p.Type)
	}
	if got := r.catalog.governedAsked(); len(got) != 1 || got[0] != ungoverned {
		t.Fatalf("the governed set was asked %v, want [%s]", got, ungoverned)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestAGrantTheCallerDoesNotHoldIsProtected holds the fourth rule: a caller may
// not grant a permission it does not hold. The refusal is a 403 with the
// protected code — the target is not permitted, not the caller short a
// permission — and nothing downstream runs.
func TestAGrantTheCallerDoesNotHoldIsProtected(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	r.subject.answer(authz.PermissionTenantRoleRead) // holds read, not manage
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleManage),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleManage)), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("granting a permission the caller does not hold answered %d, want 403", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
		t.Fatalf("the 403's type = %q, want urn:speed:authz:protected", p.Type)
	}
	if domains, _ := r.subject.asked(); len(domains) != 1 || domains[0] != "t1" {
		t.Fatalf("the held set was read in %v, want [t1]", domains)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestAnUnreadableHeldSetIsA503 holds the direction of the permission read: a
// failure is a 503 and is never read as an empty set or a permit.
func TestAnUnreadableHeldSetIsA503(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead)
	r.subject.fail(authz.ErrPolicyUnavailable)
	w := r.serve(writeRoute(r.p, grantPutOperation.Handle(r.p.handleGrantPut)), http.MethodPut,
		"/api/v1/tenants/t1/roles/r1/grants/"+string(authz.PermissionTenantRoleRead),
		params("tenant", "t1", "role", "r1", "permission", string(authz.PermissionTenantRoleRead)), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable held set answered %d, want 503", w.Code)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestABindingToARoleHigherThanTheCallerIsProtected holds the fifth rule: a
// binding may not hand out a role holding a permission the caller does not
// hold. The role's grants are read and mapped back through the catalog; a grant
// the caller does not hold is a protected refusal.
func TestABindingToARoleHigherThanTheCallerIsProtected(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	r.subject.answer(authz.PermissionTenantRoleRead) // holds read only
	r.manager.grants = []authz.GrantRecord{
		{Tenant: "t1", Role: "editor", Resource: "tenant_role", Action: "manage"},
	}
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("binding a role higher than the caller answered %d, want 403", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
		t.Fatalf("the 403's type = %q, want urn:speed:authz:protected", p.Type)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestABindingToARoleWithinTheCallerSetIsAllowed holds the subset rule's
// boundary: every one of the role's grants is one the caller holds.
func TestABindingToARoleWithinTheCallerSetIsAllowed(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	r.manager.grants = []authz.GrantRecord{
		{Tenant: "t1", Role: "editor", Resource: "tenant_role", Action: "read"},
	}
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("binding a role within the caller's set answered %d, want 204", w.Code)
	}
}

// TestABindingToANonMemberIsABadRequest holds the membership rule: an account
// that is not an active member of the tenant is a 400, and nothing downstream
// runs.
func TestABindingToANonMemberIsABadRequest(t *testing.T) {
	r := newRig(t)
	r.membership.allow(false)
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/tenants/t1/bindings/outsider/editor",
		params("tenant", "t1", "account", "outsider", "role", "editor"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("binding a non-member answered %d, want 400", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:bad-request" {
		t.Fatalf("the 400's type = %q, want urn:speed:authz:bad-request", p.Type)
	}
	tenants, accounts := r.membership.asked()
	if len(tenants) != 1 || tenants[0] != "t1" || len(accounts) != 1 || accounts[0] != "outsider" {
		t.Fatalf("the membership check asked %v/%v, want t1/outsider", tenants, accounts)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestAMembershipCheckerThatCannotAnswerIsA503 holds the membership read's
// failure direction.
func TestAMembershipCheckerThatCannotAnswerIsA503(t *testing.T) {
	r := newRig(t)
	r.membership.fail(errors.New("the membership store is down"))
	w := r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
		"/api/v1/tenants/t1/bindings/acct-1/editor",
		params("tenant", "t1", "account", "acct-1", "role", "editor"), nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a membership checker that could not answer answered %d, want 503", w.Code)
	}
	assertRefusedBeforeDownstream(t, r)
}

// TestAnEmptyBindingAccountIsRefusedBeforeAnythingRuns holds the binding write
// and delete shape rule: the empty account names no row, so it is a 400 before
// the membership check, the write guard, the audit and the manager.
func TestAnEmptyBindingAccountIsRefusedBeforeAnythingRuns(t *testing.T) {
	for name, drive := range map[string]func(*rig) int{
		"the write": func(r *rig) int {
			return r.serve(writeRoute(r.p, bindingPutOperation.Handle(r.p.handleBindingPut)), http.MethodPut,
				"/api/v1/tenants/t1/bindings//editor",
				params("tenant", "t1", "account", "", "role", "editor"), nil).Code
		},
		"the delete": func(r *rig) int {
			return r.serve(writeRoute(r.p, bindingDeleteOperation.Handle(r.p.handleBindingDelete)), http.MethodDelete,
				"/api/v1/tenants/t1/bindings//editor",
				params("tenant", "t1", "account", "", "role", "editor"), nil).Code
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if got := drive(r); got != http.StatusBadRequest {
				t.Fatalf("the empty account answered %d, want 400", got)
			}
			if tenants, _ := r.membership.asked(); len(tenants) != 0 {
				t.Fatalf("the membership checker was asked %v for an empty account", tenants)
			}
			assertRefusedBeforeDownstream(t, r)
		})
	}
}

// TestARoleTheManagerRefusesIsMapped holds the closed failure mapping for a
// reserved role reported by the manager itself.
func TestARoleTheManagerRefusesIsMapped(t *testing.T) {
	r := newRig(t)
	r.manager.fail(authz.ErrReservedRole)
	w := r.serve(rolePutRoute(r.p), http.MethodPut, "/api/v1/tenants/t1/roles/editor",
		params("tenant", "t1", "role", "editor"), roleBody{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a manager reserved-role refusal answered %d, want 403", w.Code)
	}
	if p := decodeProblem(t, w.Body.String()); p.Type != "urn:speed:authz:protected" {
		t.Fatalf("the 403's type = %q, want urn:speed:authz:protected", p.Type)
	}
}

// assertRefusedBeforeDownstream holds that a refusal reached none of the stages
// after the anti-escalation checks: the host write guard was not asked, no audit
// row landed and the manager was not called.
func assertRefusedBeforeDownstream(t *testing.T, r *rig) {
	t.Helper()
	if got := r.writeGuard.asked(); len(got) != 0 {
		t.Errorf("the write guard was asked %+v, want no question", got)
	}
	if got := r.audit.entries(); len(got) != 0 {
		t.Errorf("an audit row was written %+v, want none", got)
	}
	if got := writes(r.manager.recorded()); len(got) != 0 {
		t.Errorf("the manager was called %+v, want no write", got)
	}
}
