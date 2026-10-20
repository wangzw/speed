package main

import (
	"context"
	"errors"
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

// The role is created without the builtin mark: it is marked only once the
// grants are in place, so the mark cannot freeze a role before the grants that
// give it meaning exist.
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

// grantSeeder answers the seeding call seedGrants makes. The number added and
// the error are fixed by the case, so the case pins what the seed reports and
// what it hands the seeding surface.
type grantSeeder struct {
	authz.Seeder
	tenant    string
	role      authz.Role
	attempted []authz.Grant
	added     int
	err       error
}

func (s *grantSeeder) SeedGrants(_ context.Context, tenant string, role authz.Role,
	grants []authz.Grant,
) (int, error) {
	s.tenant, s.role = tenant, role
	s.attempted = append(s.attempted, grants...)
	return s.added, s.err
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

// DescriptionKey answers no key: these cases exercise the seeding surface, which
// reads the declared set and the translation, never a permission's description.
func (c fixedCatalog) DescriptionKey(permission authz.Permission) (string, bool) {
	_, ok := c.pairs[permission]
	return "", ok
}

// A permission declared after the builtin mark reaches the role through the
// seeding surface: seedGrants hands every translated declared permission to
// SeedGrants, which is the one write that adds to a builtin role. The number
// the surface reports added is what the seed prints, so a grown set and an
// unchanged one are distinguished by the surface's own answer.
func TestSeedGrantsSeedsEveryDeclaredPermission(t *testing.T) {
	seeder := &grantSeeder{added: 2}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read":  {"deal", "read"},
		"crm.deal.write": {"deal", "write"},
	}}
	if err := seedGrants(t.Context(), seeder, catalog, "system"); err != nil {
		t.Fatalf("seedGrants: %v", err)
	}
	if seeder.tenant != "system" || seeder.role != platformAdminRole {
		t.Errorf("SeedGrants got tenant %q role %q, want system/%s",
			seeder.tenant, seeder.role, platformAdminRole)
	}
	got := slices.SortedFunc(slices.Values(seeder.attempted), func(a, b authz.Grant) int {
		if a.Resource != b.Resource {
			return strings.Compare(a.Resource, b.Resource)
		}
		return strings.Compare(a.Action, b.Action)
	})
	want := []authz.Grant{
		{Resource: "deal", Action: "read"},
		{Resource: "deal", Action: "write"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("SeedGrants got %+v, want %+v", got, want)
	}
}

// The seeding write's failure is reported with the domain and the role it
// named, and the surface's own error survives the wrapping: swallowing it would
// turn a failed seed into a role silently short of a permission.
func TestSeedReportsASeedingFailure(t *testing.T) {
	refusal := errors.New("authz/policy: the table is unavailable")
	seeder := &grantSeeder{err: refusal}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	err := seedGrants(t.Context(), seeder, catalog, "system")
	if !errors.Is(err, refusal) {
		t.Fatalf("seedGrants error = %v, want it to wrap the seeding failure", err)
	}
	for _, want := range []string{
		"seed: granting the declared permissions to system/" + string(platformAdminRole),
		refusal.Error(),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("seedGrants error = %q, want it to contain %q", err, want)
		}
	}
}

// A deployment may lower the account store's password policy through
// seed.settings. The setting has to be in force when the seeded accounts are
// written, so the seed writes the settings before the accounts: an
// eight-character password is refused under the declared minimum of twelve and
// accepted once the deployment's value lands first.
func TestSeedAppliesTheSettingsBeforeCreatingAccounts(t *testing.T) {
	t.Parallel()
	body := configBody(t, freePorts(t, listenerCount), ":memory:")
	body = strings.Replace(body,
		`password: "operator-dev-secret"`, `password: "abcd1234"`, 1)
	body = strings.Replace(body, `    - key: "authn.mfa.sensitive-min-age"
      value: "1h"`, `    - key: "authn.mfa.sensitive-min-age"
      value: "1h"
    - key: "authn.account.password-min-length"
      value: "8"`, 1)
	config := writeConfigFile(t, body)

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("seed with a lowered password policy failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: account operator@demo.test") {
		t.Errorf("the account with the eight-character password was not seeded:\n%s", res.stdout)
	}
}
