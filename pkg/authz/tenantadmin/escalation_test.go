package tenantadmin

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

type response struct{ status int }

// TestPlatformAdminIsRefusedByRoleNameUnconditionally checks that platform admin is refused by role name unconditionally.
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

// TestTheTenantAdminRoleRowAndItsGrantsAreProtected checks that the tenant admin role row and its grants are protected.
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

// TestBindingTenantAdminIsAllowedSubjectToTheOtherRules checks that binding tenant admin is allowed subject to the other rules.
func TestBindingTenantAdminIsAllowedSubjectToTheOtherRules(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
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

// TestAGrantOutsideTheGovernedSetIsABadRequest checks that a grant outside the governed set is a bad request.
func TestAGrantOutsideTheGovernedSetIsABadRequest(t *testing.T) {
	const ungoverned = authz.PermissionSessionRead
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

// TestAGrantTheCallerDoesNotHoldIsProtected checks that a grant the caller does not hold is protected.
func TestAGrantTheCallerDoesNotHoldIsProtected(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	r.subject.answer(authz.PermissionTenantRoleRead)
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

// TestAnUnreadableHeldSetIsA503 checks that an unreadable held set is a 503.
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

// TestABindingToARoleHigherThanTheCallerIsProtected checks that a binding to a role higher than the caller is protected.
func TestABindingToARoleHigherThanTheCallerIsProtected(t *testing.T) {
	r := newRig(t, authz.PermissionTenantRoleRead, authz.PermissionTenantRoleManage)
	r.subject.answer(authz.PermissionTenantRoleRead)
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

// TestABindingToARoleWithinTheCallerSetIsAllowed checks that a binding to a role within the caller set is allowed.
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

// TestABindingToANonMemberIsABadRequest checks that a binding to a non member is a bad request.
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

// TestAMembershipCheckerThatCannotAnswerIsA503 checks that a membership checker that cannot answer is a 503.
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

// TestAnEmptyBindingAccountIsRefusedBeforeAnythingRuns checks that an empty binding account is refused before anything runs.
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

// TestARoleTheManagerRefusesIsMapped checks that a role the manager refuses is mapped.
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
