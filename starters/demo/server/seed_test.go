package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
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
	if err := seedRole(t.Context(), manager, "system", platformAdminRole, platformAdminDescriptionKey, time.Now().UTC()); err != nil {
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
	if err := markRoleBuiltin(t.Context(), manager, "system", platformAdminRole); err != nil {
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
	if err := markRoleBuiltin(t.Context(), manager, "system", platformAdminRole); err != nil {
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
	if err := seedGrants(t.Context(), seeder, catalog, "system", platformAdminRole); err != nil {
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
	err := seedGrants(t.Context(), seeder, catalog, "system", platformAdminRole)
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

// memberConfig extends the configuration template's seed section with a
// members list and points the stack at a database in a file, so that what the
// seed command writes outlives its process and the case can read it back
// through the account module.
func memberConfig(t *testing.T, dsn, members string) string {
	t.Helper()
	body := configBody(t, freePorts(t, listenerCount), dsn)
	const needle = "  tenants:\n    - name: demo\n      admins:\n        - operator\n"
	replaced := strings.Replace(body, needle, needle+members, 1)
	if replaced == body {
		t.Fatalf("the configuration template holds no seed tenants section to extend")
	}
	return writeConfigFile(t, replaced)
}

// withMemberships opens the database a seed run wrote through the real
// account module and the real engine, and runs check while the assembly is
// up: a read after Drive returns would find the database closed. What the
// check sees is the product seam a caller of pkg/authn sees, not the table.
func withMemberships(t *testing.T, dsn string,
	check func(context.Context, authn.Accounts, authn.Memberships),
) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"authn":{"account":{}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			check(ctx, accounts, memberships)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded database: %v", err)
	}
}

// operatorID is the account identifier the configuration's operator was
// seeded with.
func operatorID(t *testing.T, ctx context.Context, accounts authn.Accounts) authn.AccountID {
	t.Helper()
	id, err := accounts.Lookup(ctx, authn.IdentifierUsername, "operator")
	if err != nil {
		t.Fatalf("looking up the seeded operator: %v", err)
	}
	return id
}

// A members entry writes an active membership through the membership seam, so
// IsMember answers true for the account and the tenant afterwards.
func TestSeedWritesAnActiveMembership(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "members.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n")

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: member operator in demo") {
		t.Errorf("the seed did not report the membership it wrote:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := operatorID(t, ctx, accounts)
		member, err := memberships.IsMember(ctx, id, "demo")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if !member {
			t.Error("the seeded account does not read as a member of demo")
		}
		held, found, err := memberships.Membership(ctx, "demo", id)
		if err != nil {
			t.Fatalf("Membership: %v", err)
		}
		if !found || !held.Active {
			t.Errorf("the membership = %+v found %t, want an active one", held, found)
		}
	})
}

// A member marked suspended is written, and it reads as suspended: the row is
// there with Active false, so a later resume is an update rather than a
// creation.
func TestSeedWritesASuspendedMembership(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "suspended.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n"+
		"      suspended: true\n")

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: member operator in demo (suspended)") {
		t.Errorf("the seed did not report the suspended membership it wrote:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := operatorID(t, ctx, accounts)
		member, err := memberships.IsMember(ctx, id, "demo")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if member {
			t.Error("a suspended membership reads as membership")
		}
		held, found, err := memberships.Membership(ctx, "demo", id)
		if err != nil {
			t.Fatalf("Membership: %v", err)
		}
		if !found {
			t.Fatal("the suspended membership row is not there")
		}
		if held.Active {
			t.Errorf("the suspended membership reads active: %+v", held)
		}
	})
}

// A member list the seed cannot write fails the command before it writes
// anything, and the message names the entry that has to be corrected.
func TestSeedRefusesAMemberListItCannotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		members string
		want    []string
	}{
		{
			name:    "unknown tenant",
			members: "  members:\n    - tenant: \"ghost\"\n      account: \"operator\"\n",
			want:    []string{"ghost", "seed.tenants"},
		},
		{
			name:    "unknown account",
			members: "  members:\n    - tenant: \"demo\"\n      account: \"nobody\"\n",
			want:    []string{"nobody", "seed.accounts"},
		},
		{
			name: "duplicate pair",
			members: "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n" +
				"    - tenant: \"demo\"\n      account: \"operator\"\n",
			want: []string{"operator", "demo", "twice"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			config := memberConfig(t, ":memory:", c.members)
			res := runCommand(t, config, "sqlite", "seed")
			if res.err == nil {
				t.Fatalf("the seed accepted a member list it cannot write:\n%s", res.combined())
			}
			for _, want := range c.want {
				if !strings.Contains(res.stderr, want) {
					t.Errorf("the refusal does not name %q:\n%s", want, res.combined())
				}
			}
			if strings.Contains(res.stdout, "seeded: setting") {
				t.Errorf("the seed wrote something before refusing the member list:\n%s", res.stdout)
			}
		})
	}
}

// Running the same seed twice writes each membership once: the second run
// finds the row, reports it skipped and adds no duplicate.
func TestSeedMembershipsAreIdempotent(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "idempotent.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "skipped: member operator in demo (exists)") {
		t.Errorf("the second run did not report the membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, _ authn.Accounts, memberships authn.Memberships) {
		members, err := memberships.Members(ctx, "demo", authn.Page{})
		if err != nil {
			t.Fatalf("Members: %v", err)
		}
		if len(members) != 1 {
			t.Errorf("the tenant holds %d memberships, want one: %+v", len(members), members)
		}
	})
}

// A membership an operator suspended through the management surface stays
// suspended: an account that already exists is not reconciled to the declared
// state, so the operator's decision survives every restart.
func TestSeedLeavesAnOperatorsSuspendedMembershipSuspended(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "suspended-by-operator.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := operatorID(t, ctx, accounts)
		held, found, err := memberships.Membership(ctx, "demo", id)
		if err != nil {
			t.Fatalf("Membership: %v", err)
		}
		if !found {
			t.Fatal("the seeded membership is not there")
		}
		held.Active = false
		if err := memberships.Put(ctx, held); err != nil {
			t.Fatalf("suspending the membership as the operator: %v", err)
		}
	})
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "skipped: member operator in demo (exists)") {
		t.Errorf("the second run did not report the membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, _ authn.Accounts, memberships authn.Memberships) {
		members, err := memberships.Members(ctx, "demo", authn.Page{})
		if err != nil {
			t.Fatalf("Members: %v", err)
		}
		if len(members) != 1 {
			t.Fatalf("the tenant holds %d memberships, want one: %+v", len(members), members)
		}
		if members[0].Active {
			t.Errorf("the seed resumed a membership the operator suspended: %+v", members[0])
		}
	})
}

// A membership the operator removed is not written again: once the account
// exists, its memberships are runtime state, so a missing row is the operator's
// deletion and not a gap for the seed to fill.
func TestSeedDoesNotWriteBackAMembershipTheOperatorRemoved(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "removed.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		if err := memberships.Remove(ctx, operatorID(t, ctx, accounts), "demo"); err != nil {
			t.Fatalf("removing the seeded membership: %v", err)
		}
	})
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "skipped: member operator in demo (account already present)") {
		t.Errorf("the second run did not report the removed membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := operatorID(t, ctx, accounts)
		member, err := memberships.IsMember(ctx, id, "demo")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if member {
			t.Error("the seed wrote back a membership the operator removed")
		}
		if _, found, err := memberships.Membership(ctx, "demo", id); err != nil {
			t.Fatalf("Membership: %v", err)
		} else if found {
			t.Error("the removed membership row is back")
		}
	})
}

// The seed never rewrites a membership row, so the source and SSO exemption
// another writer set — an invitation's, or the operator's through the
// management surface — survive a later run along with the state.
func TestSeedLeavesAMembershipsSourceAndExemptionAlone(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "source-kept.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"operator\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		err := memberships.Put(ctx, authn.Membership{
			Account:   operatorID(t, ctx, accounts),
			Tenant:    "demo",
			Active:    false,
			Source:    "invite",
			SSOExempt: true,
		})
		if err != nil {
			t.Fatalf("writing the membership as another writer: %v", err)
		}
	})
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, _ authn.Accounts, memberships authn.Memberships) {
		members, err := memberships.Members(ctx, "demo", authn.Page{})
		if err != nil {
			t.Fatalf("Members: %v", err)
		}
		if len(members) != 1 {
			t.Fatalf("the tenant holds %d memberships, want one: %+v", len(members), members)
		}
		held := members[0]
		if held.Active {
			t.Errorf("the seed resumed the membership: %+v", held)
		}
		if held.Source != "invite" {
			t.Errorf("the membership source = %q, want %q", held.Source, "invite")
		}
		if !held.SSOExempt {
			t.Errorf("the membership lost its SSO exemption: %+v", held)
		}
	})
}

// A seed section with no members list is what every existing configuration
// holds, and it still seeds: the members item is optional.
func TestSeedWithoutAMembersListStillSeeds(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "no-members.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("a configuration without a members list failed to seed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: account operator") {
		t.Errorf("the seed wrote no account:\n%s", res.stdout)
	}
}

// The settings have to be in force when the seeded accounts are written, so
// the seed writes them before the accounts. The declared minimum is the
// domain's floor, so the proof raises it: with twelve in force the seeded
// eight-character password is refused, where writing the account first would
// have accepted it under the default.
func TestSeedAppliesTheSettingsBeforeCreatingAccounts(t *testing.T) {
	t.Parallel()
	body := configBody(t, freePorts(t, listenerCount), ":memory:")
	body = strings.Replace(body,
		`password: "operator-dev-secret"`, `password: "abcd1234"`, 1)
	body = strings.Replace(body, `    - key: "authn.mfa.sensitive-min-age"
      value: "1h"`, `    - key: "authn.mfa.sensitive-min-age"
      value: "1h"
    - key: "authn.account.password-min-length"
      value: "12"`, 1)
	config := writeConfigFile(t, body)

	res := runCommand(t, config, "sqlite", "seed")
	if res.err == nil {
		t.Fatalf("seed succeeded under the raised policy; the settings were not in force before the accounts:\n%s", res.combined())
	}
	if !strings.Contains(res.combined(), authn.ErrPasswordPolicy.Error()) {
		t.Errorf("seed failed for a reason other than the password policy: %v\n%s", res.err, res.combined())
	}
	if strings.Contains(res.stdout, "seeded: account operator") {
		t.Errorf("the account was seeded despite the raised policy:\n%s", res.stdout)
	}
}

// A tenant with no administrator would be a tenant nobody can enter: the
// tenant-admin binding is the only way to hold that role. The command refuses
// the list before writing anything.
func TestSeedRefusesATenantWithoutAnAdministrator(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts: []seedAccount{{Identifier: "operator", Password: "operator-dev-secret"}},
		Tenants:  []seedTenantEntry{{Name: "acme"}},
	}
	err := checkTenants(in)
	if err == nil {
		t.Fatal("checkTenants accepted a tenant with no administrator")
	}
	if !strings.Contains(err.Error(), "names no administrator") {
		t.Errorf("the refusal = %q, want it to name the missing administrator", err)
	}
}

// An administrator has to be one of the declared accounts: the binding would
// otherwise name an account no seed run can write.
func TestSeedRefusesAnAdministratorItCannotName(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts: []seedAccount{{Identifier: "operator", Password: "operator-dev-secret"}},
		Tenants:  []seedTenantEntry{{Name: "acme", Admins: []string{"ghost"}}},
	}
	err := checkTenants(in)
	if err == nil {
		t.Fatal("checkTenants accepted an administrator the account list does not declare")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "seed.accounts") {
		t.Errorf("the refusal = %q, want it to name the offender and seed.accounts", err)
	}
}

// The first administrator is written first, so it is the tenant's first
// administrator: the binding order is the list's order.
func TestSeedBindsTheTenantAdministratorsInTheDeclaredOrder(t *testing.T) {
	t.Parallel()
	manager := &orderManager{}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{"crm.deal.read": {"deal", "read"}}}
	seeder := &orderSeeder{manager: manager}
	if err := seedAdministration(t.Context(), manager, seeder, catalog,
		"acme", tenantAdminRole, tenantAdminDescriptionKey); err != nil {
		t.Fatalf("seedAdministration: %v", err)
	}
	for _, id := range []authn.AccountID{"acct-1", "acct-2"} {
		if err := seedBinding(t.Context(), manager, "acme", tenantAdminRole, id); err != nil {
			t.Fatalf("seedBinding: %v", err)
		}
	}
	want := []string{"role:tenant-admin", "seed:tenant-admin", "mark:tenant-admin", "bind:acct-1", "bind:acct-2"}
	if got := manager.order; !slices.Equal(got, want) {
		t.Errorf("the writes were %v, want %v", got, want)
	}
}

// orderManager records the administration writes in order.
type orderManager struct {
	authz.Manager
	order []string
	roles []authz.RoleRecord
}

func (m *orderManager) Roles(context.Context, string) ([]authz.RoleRecord, error) {
	return slices.Clone(m.roles), nil
}
func (m *orderManager) PutRole(_ context.Context, r authz.RoleRecord) error {
	m.order = append(m.order, "role:"+string(r.Role))
	m.roles = append(m.roles, r)
	return nil
}
func (m *orderManager) MarkRoleBuiltin(_ context.Context, _ string, role authz.Role) error {
	m.order = append(m.order, "mark:"+string(role))
	return nil
}
func (m *orderManager) BindingsOf(context.Context, string, string) ([]authz.Binding, error) {
	return nil, nil
}
func (m *orderManager) PutBinding(_ context.Context, b authz.Binding) error {
	m.order = append(m.order, "bind:"+b.Account)
	return nil
}

// orderSeeder records a seeding call in the manager's order.
type orderSeeder struct {
	authz.Seeder
	manager *orderManager
}

func (s *orderSeeder) SeedGrants(context.Context, string, authz.Role, []authz.Grant) (int, error) {
	s.manager.order = append(s.manager.order, "seed:"+string(authz.TenantAdminRole))
	return 0, nil
}
