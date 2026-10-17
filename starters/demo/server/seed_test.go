package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// roleManager answers the manager calls the seed's role steps make. The
// embedded interface is nil on purpose: a call this case does not expect panics
// rather than passing silently, so the fixture stays honest about what it
// exercises.
type roleManager struct {
	authz.Manager
	roles  []authz.RoleRecord
	writes []authz.RoleRecord
	marks  []authz.Role
}

func (m *roleManager) Roles(context.Context, string) ([]authz.RoleRecord, error) {
	return slices.Clone(m.roles), nil
}

func (m *roleManager) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.writes = append(m.writes, r)
	return nil
}

func (m *roleManager) MarkRoleBuiltin(_ context.Context, _ string, role authz.Role) error {
	m.marks = append(m.marks, role)
	return nil
}

// The role is created without the builtin mark: the grants are written next,
// and the manager refuses every grant write on a builtin role, so a role created
// with the mark would be frozen holding nothing.
func TestSeedCreatesTheAdministratorRoleWithoutTheMark(t *testing.T) {
	manager := &roleManager{}
	if err := seedRole(t.Context(), manager, "system", time.Now().UTC()); err != nil {
		t.Fatalf("seedRole: %v", err)
	}
	if len(manager.writes) != 1 {
		t.Fatalf("seedRole wrote %d roles, want one", len(manager.writes))
	}
	written := manager.writes[0]
	if written.Role != platformAdminRole || written.Tenant != "system" {
		t.Errorf("seedRole wrote %q in %q, want %q in system",
			written.Role, written.Tenant, platformAdminRole)
	}
	if written.DescriptionKey != platformAdminDescriptionKey {
		t.Errorf("the seeded role's description key = %q, want %q",
			written.DescriptionKey, platformAdminDescriptionKey)
	}
	if written.Builtin {
		t.Errorf("the role was written builtin before its grants; no grant could be written after: %+v", written)
	}
}

// The mark is what keeps the role — which holds every declared permission — off
// the console's delete control. Without it the console offers a deletion that
// locks the operator out of every console page. It is set through the manager's
// marking method, not by rewriting the record: a role write carrying the mark
// is refused, and the rewrite would also touch the description and the
// creation time the step has no business changing.
func TestSeedMarksTheAdministratorRoleBuiltin(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	manager := &roleManager{roles: []authz.RoleRecord{
		{
			Tenant:         "system",
			Role:           platformAdminRole,
			Builtin:        false,
			DescriptionKey: "authz.role.custom",
			CreatedAt:      created,
		},
	}}
	if err := markRoleBuiltin(t.Context(), manager, "system"); err != nil {
		t.Fatalf("markRoleBuiltin: %v", err)
	}
	if len(manager.writes) != 0 {
		t.Fatalf("markRoleBuiltin rewrote the role record: %+v", manager.writes)
	}
	if len(manager.marks) != 1 || manager.marks[0] != platformAdminRole {
		t.Fatalf("the marks = %v, want one for %q", manager.marks, platformAdminRole)
	}
}

// A builtin role that is already there is left alone: a second seed writes
// nothing.
func TestSeedLeavesAnExistingBuiltinRoleAlone(t *testing.T) {
	manager := &roleManager{roles: []authz.RoleRecord{
		{Tenant: "system", Role: platformAdminRole, Builtin: true, DescriptionKey: "kept"},
	}}
	if err := markRoleBuiltin(t.Context(), manager, "system"); err != nil {
		t.Fatalf("markRoleBuiltin: %v", err)
	}
	if len(manager.writes) != 0 || len(manager.marks) != 0 {
		t.Errorf("markRoleBuiltin touched an existing builtin role: writes %+v, marks %v",
			manager.writes, manager.marks)
	}
}
