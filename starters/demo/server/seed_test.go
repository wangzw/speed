package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// grantManager answers the grant calls seedGrants makes. Its PutGrant answers
// with the error the policy manager returns on a builtin role, so the case
// pins the seed's report of a frozen grant set.
type grantManager struct {
	authz.Manager
	attempted []authz.GrantRecord
	err       error
}

func (m *grantManager) Grants(context.Context, string, authz.Role) ([]authz.GrantRecord, error) {
	return nil, nil
}

func (m *grantManager) PutGrant(_ context.Context, g authz.GrantRecord) error {
	m.attempted = append(m.attempted, g)
	return m.err
}

// fixedCatalog is a catalog holding the permissions it is given, each with the
// (resource, action) pair it translates to.
type fixedCatalog struct {
	pairs map[authz.Permission][2]string
}

func (c fixedCatalog) Declared() []authz.Permission {
	out := make([]authz.Permission, 0, len(c.pairs))
	for permission := range c.pairs {
		out = append(out, permission)
	}
	return out
}

func (c fixedCatalog) Has(permission authz.Permission) bool {
	_, ok := c.pairs[permission]
	return ok
}

func (c fixedCatalog) ObjectAction(permission authz.Permission) (string, string, bool) {
	pair, ok := c.pairs[permission]
	if !ok {
		return "", "", false
	}
	return pair[0], pair[1], true
}

// A permission declared after the builtin mark cannot be added to the frozen
// role. The seed must report the refusal with the permission and the domain it
// named, and the manager's own error must survive the wrapping: swallowing it
// would turn a frozen grant set into a silent shortfall.
func TestSeedReportsAFrozenGrantRefusal(t *testing.T) {
	refusal := fmt.Errorf("%w: the role %q of tenant %q is builtin",
		authz.ErrBuiltinRole, platformAdminRole, "system")
	manager := &grantManager{err: refusal}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	err := seedGrants(t.Context(), manager, catalog, "system", time.Now().UTC())
	if !errors.Is(err, authz.ErrBuiltinRole) {
		t.Fatalf("seedGrants error = %v, want it to wrap authz.ErrBuiltinRole", err)
	}
	for _, want := range []string{
		"seed: granting crm.deal.read to system/" + string(platformAdminRole),
		refusal.Error(),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("seedGrants error = %q, want it to contain %q", err, want)
		}
	}
	if len(manager.attempted) != 1 {
		t.Fatalf("seedGrants attempted %d grant writes, want one", len(manager.attempted))
	}
	got := manager.attempted[0]
	if got.Resource != "deal" || got.Action != "read" || got.Role != platformAdminRole || got.Tenant != "system" {
		t.Errorf("the attempted grant = %+v, want deal/read on %s/system", got, platformAdminRole)
	}
}
