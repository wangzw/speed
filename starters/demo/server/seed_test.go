package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
)

// roleManager answers the two manager calls the seed's role steps make. The
// embedded interface is nil on purpose: a call this case does not expect panics
// rather than passing silently, so the fixture stays honest about what it
// exercises.
type roleManager struct {
	authz.Manager
	roles  []authz.RoleRecord
	writes []authz.RoleRecord
}

func (m *roleManager) Roles(context.Context, string) ([]authz.RoleRecord, error) {
	return slices.Clone(m.roles), nil
}

func (m *roleManager) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.writes = append(m.writes, r)
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
// locks the operator out of every console page.
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
	if len(manager.writes) != 1 {
		t.Fatalf("markRoleBuiltin wrote %d roles, want one", len(manager.writes))
	}
	marked := manager.writes[0]
	if !marked.Builtin {
		t.Errorf("the role was not marked builtin: %+v", marked)
	}
	if marked.DescriptionKey != "authz.role.custom" {
		t.Errorf("the mark replaced the description key with %q", marked.DescriptionKey)
	}
	if !marked.CreatedAt.Equal(created) {
		t.Errorf("the mark moved the creation time to %v, want %v", marked.CreatedAt, created)
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
	if len(manager.writes) != 0 {
		t.Errorf("markRoleBuiltin rewrote an existing builtin role: %+v", manager.writes)
	}
}
