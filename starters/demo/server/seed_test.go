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
	"github.com/vislake/speed/pkg/tenant"
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
	return accountIDByName(t, ctx, accounts, "operator")
}

// memberID is the account identifier of the configuration's second account,
// the one the seed.members cases use: it administers no tenant, so a membership
// the operator removes is not reconciled back as an administrator's.
func memberID(t *testing.T, ctx context.Context, accounts authn.Accounts) authn.AccountID {
	t.Helper()
	return accountIDByName(t, ctx, accounts, "ops")
}

func accountIDByName(t *testing.T, ctx context.Context, accounts authn.Accounts, name string) authn.AccountID {
	t.Helper()
	id, err := accounts.Lookup(ctx, authn.IdentifierUsername, name)
	if err != nil {
		t.Fatalf("looking up the seeded account %q: %v", name, err)
	}
	return id
}

// A members entry writes an active membership through the membership seam, so
// IsMember answers true for the account and the tenant afterwards.
func TestSeedWritesAnActiveMembership(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "members.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n")

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: member ops in demo") {
		t.Errorf("the seed did not report the membership it wrote:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := memberID(t, ctx, accounts)
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
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n"+
		"      suspended: true\n")

	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: member ops in demo (suspended)") {
		t.Errorf("the seed did not report the suspended membership it wrote:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := memberID(t, ctx, accounts)
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
			members: "  members:\n    - tenant: \"ghost\"\n      account: \"ops\"\n",
			want:    []string{"ghost", "seed.tenants"},
		},
		{
			name:    "unknown account",
			members: "  members:\n    - tenant: \"demo\"\n      account: \"nobody\"\n",
			want:    []string{"nobody", "seed.accounts"},
		},
		{
			name: "duplicate pair",
			members: "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n" +
				"    - tenant: \"demo\"\n      account: \"ops\"\n",
			want: []string{"ops", "demo", "twice"},
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
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "skipped: member ops in demo (exists)") {
		t.Errorf("the second run did not report the membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := memberID(t, ctx, accounts)
		members, err := memberships.Members(ctx, "demo", authn.Page{})
		if err != nil {
			t.Fatalf("Members: %v", err)
		}
		held := 0
		for _, member := range members {
			if member.Account == id {
				held++
			}
		}
		if held != 1 {
			t.Errorf("the account holds %d memberships in demo, want one: %+v", held, members)
		}
	})
}

// A membership an operator suspended through the management surface stays
// suspended: an account that already exists is not reconciled to the declared
// state, so the operator's decision survives every restart.
func TestSeedLeavesAnOperatorsSuspendedMembershipSuspended(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "suspended-by-operator.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := memberID(t, ctx, accounts)
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
	if !strings.Contains(res.stdout, "skipped: member ops in demo (exists)") {
		t.Errorf("the second run did not report the membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		held, found, err := memberships.Membership(ctx, "demo", memberID(t, ctx, accounts))
		if err != nil {
			t.Fatalf("Membership: %v", err)
		}
		if !found {
			t.Fatal("the membership row is gone")
		}
		if held.Active {
			t.Errorf("the seed resumed a membership the operator suspended: %+v", held)
		}
	})
}

// A membership the operator removed is not written again: once the account
// exists, its memberships are runtime state, so a missing row is the operator's
// deletion and not a gap for the seed to fill.
func TestSeedDoesNotWriteBackAMembershipTheOperatorRemoved(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "removed.db")
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		if err := memberships.Remove(ctx, memberID(t, ctx, accounts), "demo"); err != nil {
			t.Fatalf("removing the seeded membership: %v", err)
		}
	})
	res := runCommand(t, config, "sqlite", "seed")
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "skipped: member ops in demo (account already present)") {
		t.Errorf("the second run did not report the removed membership as skipped:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		id := memberID(t, ctx, accounts)
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
	config := memberConfig(t, dsn, "  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n")

	if res := runCommand(t, config, "sqlite", "seed"); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		err := memberships.Put(ctx, authn.Membership{
			Account:   memberID(t, ctx, accounts),
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
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		held, found, err := memberships.Membership(ctx, "demo", memberID(t, ctx, accounts))
		if err != nil {
			t.Fatalf("Membership: %v", err)
		}
		if !found {
			t.Fatal("the membership row is gone")
		}
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

// TestSeedRefusesAConfigurationWithNoPlatformAdministrator holds the deployment
// floor: a copy with no platform administrator would start a console nobody can
// reach, so the command refuses the account list and names the entries to
// correct.
func TestSeedRefusesAConfigurationWithNoPlatformAdministrator(t *testing.T) {
	t.Parallel()
	original := configBody(t, freePorts(t, listenerCount), ":memory:")
	body := strings.Replace(original, "      platform-admin: true\n", "", 1)
	if body == original {
		t.Fatal("the configuration template carries no platform-admin line to remove")
	}
	config := writeConfigFile(t, body)

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err == nil {
		t.Fatalf("the seed accepted a configuration with no platform administrator:\n%s", res.combined())
	}
	for _, want := range []string{"seed.accounts", "platform-admin"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, res.combined())
		}
	}
	if strings.Contains(res.stdout, "seeded:") {
		t.Errorf("the seed wrote something before refusing the account list:\n%s", res.stdout)
	}
}

// TestSeedRefusesTheSystemTenantInTheTenantList holds the system tenant apart
// from the configured ones: the command registers it itself, and a list entry
// naming it would let the run claim it administers a row it already wrote.
func TestSeedRefusesTheSystemTenantInTheTenantList(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts: []seedAccount{{
			Identifier: "operator", Password: "operator-dev-secret", PlatformAdmin: true,
		}},
		Tenants: []seedTenantEntry{{
			Name: string(authz.PlatformDomain), Admins: []string{"operator"},
		}},
	}
	err := checkTenants(in)
	if err == nil {
		t.Fatal("checkTenants accepted the system tenant in seed.tenants")
	}
	for _, want := range []string{string(authz.PlatformDomain), "seed.tenants"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal = %q, want it to name %q", err, want)
		}
	}
}

// TestSeedRefusesAPersonalPrefixedTenantInTheTenantList holds the personal
// domain apart from the configured business tenants: personal tenants come from
// account creation, and a configured one would be a second, unmanaged copy.
func TestSeedRefusesAPersonalPrefixedTenantInTheTenantList(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts: []seedAccount{{
			Identifier: "operator", Password: "operator-dev-secret", PlatformAdmin: true,
		}},
		Tenants: []seedTenantEntry{{
			Name: personalIDPrefix + "opaque", Admins: []string{"operator"},
		}},
	}
	err := checkTenants(in)
	if err == nil {
		t.Fatal("checkTenants accepted a personal-prefixed tenant in seed.tenants")
	}
	for _, want := range []string{personalIDPrefix, "seed.tenants", "account creation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal = %q, want it to name %q", err, want)
		}
	}
}

// TestSeedWritesAMissingAdministratorsMembershipOnEveryRun holds the
// reconciliation of seed.tenants[].admins: unlike seed.members, a missing
// administrator membership is a gap the seed fills, not an operator's removal
// it preserves.
func TestSeedWritesAMissingAdministratorsMembershipOnEveryRun(t *testing.T) {
	t.Parallel()
	memberships := newFakeMemberships()
	id := authn.AccountID("acct-1")
	ctx := context.Background()

	active, err := seedAdminMembership(ctx, memberships, "acme", id)
	if err != nil {
		t.Fatalf("seedAdminMembership: %v", err)
	}
	if !active {
		t.Error("the administrator's written membership did not read back active")
	}
	member, err := memberships.IsMember(ctx, id, "acme")
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
	if !member {
		t.Error("the seed did not write the administrator's membership")
	}

	active, err = seedAdminMembership(ctx, memberships, "acme", id)
	if err != nil {
		t.Fatalf("the second seedAdminMembership: %v", err)
	}
	if !active {
		t.Error("the existing administrator's membership did not read back active")
	}
	members, err := memberships.Members(ctx, "acme", authn.Page{})
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("the tenant holds %d memberships, want one", len(members))
	}
}

// TestSeedLeavesASuspendedAdministratorsMembershipSuspended holds the other
// side of reconciliation: an existing row is the operator's state, so the seed
// neither resumes it nor writes the binding its suspension makes ineffective.
func TestSeedLeavesASuspendedAdministratorsMembershipSuspended(t *testing.T) {
	t.Parallel()
	memberships := newFakeMemberships()
	id := authn.AccountID("acct-1")
	ctx := context.Background()
	if err := memberships.Put(ctx, authn.Membership{
		Account: id, Tenant: "acme", Active: false, Source: "admin",
	}); err != nil {
		t.Fatalf("writing the suspended membership: %v", err)
	}

	active, err := seedAdminMembership(ctx, memberships, "acme", id)
	if err != nil {
		t.Fatalf("seedAdminMembership: %v", err)
	}
	if active {
		t.Error("the seed resumed a membership the operator suspended")
	}
	held, found, err := memberships.Membership(ctx, "acme", id)
	if err != nil {
		t.Fatalf("Membership: %v", err)
	}
	if !found || held.Active {
		t.Errorf("the membership = %+v found %t, want the suspension left in place", held, found)
	}
}

// TestSeedAddsAMissingAdministratorsBindingOnEveryRun holds the binding half of
// the reconciliation: the tenant-admin binding is added when it is missing and
// a second run writes nothing.
func TestSeedAddsAMissingAdministratorsBindingOnEveryRun(t *testing.T) {
	t.Parallel()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	ctx := context.Background()

	if err := seedBinding(ctx, manager, "acme", tenantAdminRole, authn.AccountID("acct-1")); err != nil {
		t.Fatalf("seedBinding: %v", err)
	}
	if err := seedBinding(ctx, manager, "acme", tenantAdminRole, authn.AccountID("acct-1")); err != nil {
		t.Fatalf("the second seedBinding: %v", err)
	}
	bindings, err := manager.BindingsOf(ctx, "acme", "acct-1")
	if err != nil {
		t.Fatalf("BindingsOf: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Role != tenantAdminRole {
		t.Errorf("the administrator's bindings = %+v, want one tenant-admin", bindings)
	}
}

// TestSeedRestoresARemovedTenantAdministratorsMembership runs the
// reconciliation through the program: the administrator's membership and
// binding are runtime state the seed reconciles on every run, unlike a
// seed.members row.
func TestSeedRestoresARemovedTenantAdministratorsMembership(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "admin-restored.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		if err := memberships.Remove(ctx, operatorID(t, ctx, accounts), "demo"); err != nil {
			t.Fatalf("removing the administrator's membership: %v", err)
		}
	})
	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: member ") {
		t.Errorf("the second run did not report the restored administrator membership:\n%s", res.stdout)
	}
	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		member, err := memberships.IsMember(ctx, operatorID(t, ctx, accounts), "demo")
		if err != nil {
			t.Fatalf("IsMember: %v", err)
		}
		if !member {
			t.Error("the seed did not restore the removed administrator's membership")
		}
	})
}

// TestSeedFailsOnASystemAliasConflict holds the upgrade case where a business
// tenant already holds the alias "system": the system tenant's identifier is
// free, so the read finds nothing, and Create refuses the alias. Treating that
// refusal as "the tenant exists" skips the row the policy module requires and
// fails the startup later, far from the cause; the seed reports the conflict
// and names the tenant it was registering.
func TestSeedFailsOnASystemAliasConflict(t *testing.T) {
	roster := newFakeRoster()
	roster.createErr = fmt.Errorf("%w: alias %q is held by tenant %q",
		tenant.ErrAliasConflict, string(authz.PlatformDomain), "legacy")
	err := seedTenant(context.Background(), roster, string(authz.PlatformDomain))
	if err == nil {
		t.Fatal("seedTenant accepted an alias conflict as an existing tenant")
	}
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Errorf("the refusal = %v, want it to carry ErrAliasConflict", err)
	}
	if !strings.Contains(err.Error(), string(authz.PlatformDomain)) {
		t.Errorf("the refusal = %q, want it to name the tenant whose alias conflicts", err)
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

// TestSeedMarksADeclaredTenantsExistingRoleBuiltin holds the repair of a
// declared tenant: an existing non-builtin role keeps its row, receives the
// declared grants it lacks, and is marked builtin on the run.
func TestSeedMarksADeclaredTenantsExistingRoleBuiltin(t *testing.T) {
	t.Parallel()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{{Tenant: "acme", Role: tenantAdminRole}}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read":  {"deal", "read"},
		"crm.deal.write": {"deal", "write"},
	}}
	seeder := newFakeSeeder(grants)

	if err := seedAdministration(t.Context(), manager, seeder, catalog,
		"acme", tenantAdminRole, tenantAdminDescriptionKey); err != nil {
		t.Fatalf("seedAdministration: %v", err)
	}
	if manager.markBuiltinCalls != 1 {
		t.Errorf("the declared tenant's role was marked %d time(s), want one", manager.markBuiltinCalls)
	}
	roles, err := manager.Roles(t.Context(), "acme")
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Role != tenantAdminRole || !roles[0].Builtin {
		t.Errorf("the tenant's roles = %+v, want one builtin tenant-admin after repair", roles)
	}
	if held := grants.held("acme", tenantAdminRole); len(held) != 2 {
		t.Errorf("the repaired role holds %d grant(s), want the two declared pairs", len(held))
	}
}

// TestSeedLeavesAPersonalDomainsRoleUnfrozen holds the host's personal-domain
// exception on the seed's path: a declared personal-prefixed tenant receives
// its role and grants, and the role is not marked builtin, because the host
// revokes grants outside the allowlist through the manager.
func TestSeedLeavesAPersonalDomainsRoleUnfrozen(t *testing.T) {
	t.Parallel()
	domain := personalIDPrefix + "acct-1"
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	seeder := newFakeSeeder(grants)

	if err := seedAdministration(t.Context(), manager, seeder, catalog,
		domain, tenantAdminRole, tenantAdminDescriptionKey); err != nil {
		t.Fatalf("seedAdministration: %v", err)
	}
	if manager.markBuiltinCalls != 0 {
		t.Errorf("the personal domain's role was marked builtin %d time(s), want none", manager.markBuiltinCalls)
	}
	roles, err := manager.Roles(t.Context(), domain)
	if err != nil {
		t.Fatalf("reading the personal domain's roles: %v", err)
	}
	if len(roles) != 1 || roles[0].Role != tenantAdminRole || roles[0].Builtin {
		t.Errorf("the personal domain's roles = %+v, want one non-builtin tenant-admin", roles)
	}
	if held := grants.held(domain, tenantAdminRole); len(held) != 1 {
		t.Errorf("the personal domain's role holds %d grant(s), want the declared one", len(held))
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
