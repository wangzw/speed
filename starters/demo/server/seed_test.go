package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	authnaccount "github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/admission"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

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

// TestSeedCreatesTheAdministratorRoleWithoutTheMark holds that the role is created without the builtin
// mark.
func TestSeedCreatesTheAdministratorRoleWithoutTheMark(t *testing.T) {
	t.Parallel()
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

// TestSeedMarksTheAdministratorRoleBuiltin holds that the mark is set through the manager's marking
// method once the grants are in place.
func TestSeedMarksTheAdministratorRoleBuiltin(t *testing.T) {
	t.Parallel()
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

// TestSeedLeavesAnExistingBuiltinRoleAlone holds that a second seed writes nothing for a role already
// builtin.
func TestSeedLeavesAnExistingBuiltinRoleAlone(t *testing.T) {
	t.Parallel()
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

type recordingReconciler struct {
	authz.Reconciler
	calls   []reconcileCall
	added   int
	removed int
	err     error
}

type reconcileCall struct {
	tenant  string
	role    authz.Role
	grants  []authz.Grant
	retired []authz.Grant
}

func (r *recordingReconciler) ReconcileGrants(_ context.Context, tenant string, role authz.Role,
	grants, retired []authz.Grant,
) (int, int, error) {
	r.calls = append(r.calls, reconcileCall{
		tenant: tenant, role: role, grants: slices.Clone(grants), retired: slices.Clone(retired),
	})
	return r.added, r.removed, r.err
}

type fixedCatalog struct {
	pairs    map[authz.Permission][2]string
	governed map[authz.Permission]bool
}

func (c fixedCatalog) Declared() []authz.Permission {
	out := make([]authz.Permission, 0, len(c.pairs))
	for permission := range c.pairs {
		out = append(out, permission)
	}
	return out
}

func (c fixedCatalog) TenantGoverned() []authz.Permission {
	out := make([]authz.Permission, 0, len(c.governed))
	for permission := range c.governed {
		if _, declared := c.pairs[permission]; declared {
			out = append(out, permission)
		}
	}
	slices.Sort(out)
	return out
}

func (c fixedCatalog) Governed(permission authz.Permission) bool {
	_, governed := c.governed[permission]
	_, declared := c.pairs[permission]
	return governed && declared
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

func (c fixedCatalog) PermissionOf(object, action string) (authz.Permission, bool) {
	for permission, pair := range c.pairs {
		if pair[0] == object && pair[1] == action {
			return permission, true
		}
	}
	return "", false
}

func (c fixedCatalog) DescriptionKey(permission authz.Permission) (string, bool) {
	_, ok := c.pairs[permission]
	return "", ok
}

func (c fixedCatalog) Metadata(permission authz.Permission) (authz.PermissionMeta, bool) {
	_, ok := c.pairs[permission]
	return authz.PermissionMeta{}, ok
}

// TestSeedGrantsSeedsEveryDeclaredPermission holds that every translated declared permission reaches
// SeedGrants and the added count is reported.
func TestSeedGrantsSeedsEveryDeclaredPermission(t *testing.T) {
	t.Parallel()
	seeder := &grantSeeder{added: 2}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read":  {"deal", "read"},
		"crm.deal.write": {"deal", "write"},
	}}
	if err := seedGrants(t.Context(), seeder, "system", platformAdminRole, declaredGrants(catalog)); err != nil {
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

// TestSeedReportsASeedingFailure holds that a seeding failure names the domain and role and preserves
// the surface's error.
func TestSeedReportsASeedingFailure(t *testing.T) {
	t.Parallel()
	refusal := errors.New("authz/policy: the table is unavailable")
	seeder := &grantSeeder{err: refusal}
	catalog := fixedCatalog{pairs: map[authz.Permission][2]string{
		"crm.deal.read": {"deal", "read"},
	}}
	err := seedGrants(t.Context(), seeder, "system", platformAdminRole, declaredGrants(catalog))
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

// TestTheAdministrationRolesTakeDifferentGrantSets holds that platform-admin takes every declared pair
// and tenant-admin exactly the governed set.
func TestTheAdministrationRolesTakeDifferentGrantSets(t *testing.T) {
	t.Parallel()
	catalog := fixedCatalog{
		pairs: map[authz.Permission][2]string{
			"crm.deal.read":      {"deal", "read"},
			"crm.deal.write":     {"deal", "write"},
			"authn.account.read": {"account", "read"},
		},
		governed: map[authz.Permission]bool{"crm.deal.read": true},
	}

	declaredWant := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "deal", Action: "read"},
		{Resource: "deal", Action: "write"},
	}
	if got := declaredGrants(catalog); !slices.Equal(got, declaredWant) {
		t.Errorf("the platform administrator's grants = %+v, want the full declared set %+v", got, declaredWant)
	}
	governedWant := []authz.Grant{{Resource: "deal", Action: "read"}}
	if got := governedGrants(catalog); !slices.Equal(got, governedWant) {
		t.Errorf("the tenant administrator's grants = %+v, want the governed set %+v", got, governedWant)
	}
	retiredWant := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "deal", Action: "write"},
	}
	if got := retiredTenantAdminGrants(catalog); !slices.Equal(got, retiredWant) {
		t.Errorf("the retired tenant-admin grants = %+v, want the declared-but-not-governed set %+v", got, retiredWant)
	}
}

// TestTheSeedDoesNotConvergeWhenTheAuditRowFails holds that a convergence whose audit row cannot be
// written does not run.
func TestTheSeedDoesNotConvergeWhenTheAuditRowFails(t *testing.T) {
	t.Parallel()
	reconciler := &recordingReconciler{}
	auditor := newRecordedAudit()
	auditor.err = errors.New("the audit table is unavailable")

	err := reconcileTenantAdminGrants(t.Context(), auditor, reconciler, "acme", nil, nil)
	if err == nil {
		t.Fatal("the convergence ran although its audit row could not be written")
	}
	if len(reconciler.calls) != 0 {
		t.Errorf("the reconciler was called %+v without its audit row", reconciler.calls)
	}
}

// TestTheSeedRecordsTheConvergenceBeforeAsking holds that the success row is written before the
// reconciler is asked.
func TestTheSeedRecordsTheConvergenceBeforeAsking(t *testing.T) {
	t.Parallel()
	reconciler := &recordingReconciler{}
	auditor := newRecordedAudit()
	target := []authz.Grant{{Resource: "deal", Action: "read"}}

	if err := reconcileTenantAdminGrants(t.Context(), auditor, reconciler, "acme", target, nil); err != nil {
		t.Fatalf("reconcileTenantAdminGrants: %v", err)
	}
	rows := auditor.rows()
	if len(rows) != 1 || rows[0].Action != seedReconcileAction ||
		rows[0].Object != "acme" || rows[0].Outcome != seedReconcileOutcome {
		t.Fatalf("the audit rows = %+v, want one success row for the convergence of acme", rows)
	}
	if len(reconciler.calls) != 1 {
		t.Fatalf("the reconciler was called %d time(s), want once", len(reconciler.calls))
	}
	call := reconciler.calls[0]
	if call.tenant != "acme" || call.role != tenantAdminRole || !slices.Equal(call.grants, target) {
		t.Errorf("the reconciler got %+v, want the acme tenant-admin target %+v", call, target)
	}
}

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

func withMemberships(t *testing.T, dsn string,
	check func(context.Context, authn.Accounts, authn.Memberships),
) {
	t.Helper()
	withSeededAccounts(t, dsn,
		func(ctx context.Context, accounts authn.Accounts, _ authn.AccountAdmin, memberships authn.Memberships) {
			check(ctx, accounts, memberships)
		})
}

func withSeededAccounts(t *testing.T, dsn string,
	check func(context.Context, authn.Accounts, authn.AccountAdmin, authn.Memberships),
) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"authn":{"account":{}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), authnaccount.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(authnaccount.Module())
	reg.Register(provisioningtest.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, authnaccount.ModuleName, provisioningtest.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			accountAdmin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			check(ctx, accounts, accountAdmin, memberships)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded database: %v", err)
	}
}

func createTenantInRoster(t *testing.T, dsn, id string) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"tenant":{"catalog":{}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), tenantcatalog.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(tenantcatalog.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, tenantcatalog.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return err
			}
			return roster.Create(ctx, tenant.Tenant{
				ID: tenant.ID(id), Name: id, Status: tenant.StatusActive,
			})
		},
	})
	if err != nil {
		t.Fatalf("registering the tenant %q in the roster: %v", id, err)
	}
}

func deleteTenantInRoster(t *testing.T, dsn, id string) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"tenant":{"catalog":{}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), tenantcatalog.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(tenantcatalog.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, tenantcatalog.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return err
			}
			return roster.Delete(ctx, tenant.ID(id))
		},
	})
	if err != nil {
		t.Fatalf("deleting the tenant %q from the roster: %v", id, err)
	}
}

func withRawHandle(t *testing.T, dsn string, check func(context.Context, *gorm.DB)) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			check(ctx, database.DB())
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded database: %v", err)
	}
}

func operatorID(t *testing.T, ctx context.Context, accounts authn.Accounts) authn.AccountID {
	t.Helper()
	return accountIDByName(t, ctx, accounts, "operator")
}

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

// TestSeedWritesAnActiveMembership holds that a members entry writes an active membership.
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

// TestSeedWritesASuspendedMembership holds that a suspended entry is written with Active false.
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

// TestSeedRefusesAMemberListItCannotWrite holds that an unnameable entry fails the command before it
// writes anything.
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

// TestSeedMembershipsAreIdempotent holds that a second run finds each membership and adds no
// duplicate.
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
		members, err := memberships.Members(ctx, "demo", authn.MemberQuery{})
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

// TestSeedLeavesAnOperatorsSuspendedMembershipSuspended holds that an existing account's suspended
// membership survives.
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

// TestSeedDoesNotWriteBackAMembershipTheOperatorRemoved holds that a missing membership of an existing
// account is not written again.
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

// TestSeedLeavesAMembershipsSourceAndExemptionAlone holds that an existing row's source and SSO
// exemption survive.
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

// TestSeedWithoutAMembersListStillSeeds holds that the optional members list may be absent.
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

// TestSeedAppliesTheSettingsBeforeCreatingAccounts holds that a setting in force refuses the seed's
// account password, which proves it is written first.
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

func tenantSettingsConfig(t *testing.T, dsn, entry string) string {
	t.Helper()
	body := configBody(t, freePorts(t, listenerCount), dsn)
	const needle = "  tenants:\n    - name: demo\n      admins:\n        - operator\n"
	replaced := strings.Replace(body, needle, needle+entry, 1)
	if replaced == body {
		t.Fatalf("the configuration template holds no seed tenant entry to extend")
	}
	return writeConfigFile(t, replaced)
}

func withSettings(t *testing.T, dsn string,
	check func(context.Context, settings.Store),
) {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"event":{"memory":{}}}`, dsn)
	reader := authntest.ConfigReaderAll(t, body, sqlite.Module(), eventmemory.Module(),
		settings.Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(sqlite.Module())
	reg.Register(eventmemory.Module())
	reg.Register(settings.Module())
	reg.Register(admission.Module())
	reg.Supply(reader, config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{sqlite.ModuleName, eventmemory.ModuleName, settings.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			store, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return err
			}
			check(ctx, store)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reading the seeded settings: %v", err)
	}
}

// TestSeedWritesATenantSettingIntoThatTenantsLayer holds that a tenant setting lands in that tenant's
// layer, not the global one.
func TestSeedWritesATenantSettingIntoThatTenantsLayer(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "tenant-setting.db")
	config := tenantSettingsConfig(t, dsn,
		"      settings:\n        - key: \"tenant.admission.require-sso\"\n          value: \"true\"\n")

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	if !strings.Contains(res.stdout, "seeded: setting tenant.admission.require-sso=true in tenant demo") {
		t.Errorf("the seed did not report the tenant setting it wrote:\n%s", res.stdout)
	}
	withSettings(t, dsn, func(ctx context.Context, store settings.Store) {
		got, err := settings.Get[bool](store, settings.ForTenant("demo"), "tenant.admission.require-sso")
		if err != nil {
			t.Fatalf("reading the tenant setting: %v", err)
		}
		if !got {
			t.Error("the tenant layer does not hold the seeded switch")
		}
		global, err := settings.Get[bool](store, settings.Global(), "tenant.admission.require-sso")
		if err != nil {
			t.Fatalf("reading the global layer: %v", err)
		}
		if global {
			t.Error("the tenant setting is visible in the global layer")
		}
		rows, err := store.List(ctx, settings.ForTenant("demo"))
		if err != nil {
			t.Fatalf("listing the tenant's rows: %v", err)
		}
		if len(rows) != 1 || rows[0].Tenant != "demo" ||
			rows[0].Key != "tenant.admission.require-sso" || rows[0].Value != "true" {
			t.Errorf("the tenant's rows = %+v, want the one seeded switch under demo", rows)
		}
	})
}

// TestSeedWithoutTenantSettingsWritesNothingToTheTenantLayer holds that a tenant entry with no
// settings writes nothing.
func TestSeedWithoutTenantSettingsWritesNothingToTheTenantLayer(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "no-tenant-settings.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	withSettings(t, dsn, func(ctx context.Context, store settings.Store) {
		rows, err := store.List(ctx, settings.ForTenant("demo"))
		if err != nil {
			t.Fatalf("listing the tenant's rows: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("a tenant entry with no settings list wrote %+v, want none", rows)
		}
		global, err := store.List(ctx, settings.Global())
		if err != nil {
			t.Fatalf("listing the global rows: %v", err)
		}
		found := false
		for _, row := range global {
			if row.Key == "authn.mfa.sensitive-min-age" {
				found = true
			}
		}
		if !found {
			t.Errorf("the global settings the configuration names are not there: %+v", global)
		}
	})
}

// TestSeedRefusesATenantSettingKeyTheRegistryDoesNotDeclare holds that an undeclared tenant key fails
// and is named.
func TestSeedRefusesATenantSettingKeyTheRegistryDoesNotDeclare(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "bad-tenant-setting.db")
	config := tenantSettingsConfig(t, dsn,
		"      settings:\n        - key: \"tenant.admission.no-such-key\"\n          value: \"true\"\n")

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err == nil {
		t.Fatalf("the seed accepted a tenant setting key no declaration covers:\n%s", res.combined())
	}
	for _, want := range []string{"tenant.admission.no-such-key", settings.ErrUnknownKey.Error()} {
		if !strings.Contains(res.combined(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, res.combined())
		}
	}
}

// TestSeedRefusesATenantWithoutAnAdministrator holds that a tenant listing no administrator is refused
// before anything is written.
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

// TestSeedRefusesATenantNameOutsideTheSlugForm holds that a tenant name that is not a slug is refused.
func TestSeedRefusesATenantNameOutsideTheSlugForm(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Acme_Corp", "a.b", "ab"} {
		in := seedInput{
			Accounts: []seedAccount{{Identifier: "operator", Password: "operator-dev-secret"}},
			Tenants:  []seedTenantEntry{{Name: name, Admins: []string{"operator"}}},
		}
		err := checkTenants(in)
		if err == nil {
			t.Errorf("checkTenants accepted the tenant name %q, which is not a slug", name)
			continue
		}
		if !strings.Contains(err.Error(), "slug") {
			t.Errorf("the refusal for %q = %q, want it to name the slug form", name, err)
		}
	}
}

// TestSeedRefusesAnAdministratorItCannotName holds that an administrator must be one of the declared
// accounts.
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

// TestSeedRefusesASystemAdministratorItCannotName holds that a system administrator must be a declared
// account.
func TestSeedRefusesASystemAdministratorItCannotName(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts:     []seedAccount{{Identifier: "operator", Password: "operator-dev-secret", PlatformAdmin: true}},
		SystemAdmins: []string{"ghost"},
	}
	err := checkSystemAdmins(in)
	if err == nil {
		t.Fatal("checkSystemAdmins accepted an account the configuration does not declare")
	}
	for _, want := range []string{"ghost", "seed.accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal = %q, want it to name %q", err, want)
		}
	}
}

// TestSeedRefusesASystemAdministratorTwice holds that one account cannot administer the system tenant
// twice.
func TestSeedRefusesASystemAdministratorTwice(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts:     []seedAccount{{Identifier: "operator", Password: "operator-dev-secret", PlatformAdmin: true}},
		SystemAdmins: []string{"operator", "operator"},
	}
	err := checkSystemAdmins(in)
	if err == nil {
		t.Fatal("checkSystemAdmins accepted the same system administrator twice")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("the refusal = %q, want it to name the duplicate", err)
	}
}

// TestSeedWritesTheSystemAdministratorsMembershipAndTenantAdminBinding holds that a seed.system-admins
// entry gets a system membership and tenant-admin binding and never platform-admin.
func TestSeedWritesTheSystemAdministratorsMembershipAndTenantAdminBinding(t *testing.T) {
	t.Parallel()
	memberships := newFakeMemberships()
	manager := newFakeManager(newFakeGrants())
	ids := map[string]authn.AccountID{"system-admin": "acct-system"}

	if err := seedSystemTenantAdmins(t.Context(), memberships, manager,
		[]string{"system-admin"}, ids); err != nil {
		t.Fatalf("seedSystemTenantAdmins: %v", err)
	}

	held, found, err := memberships.Membership(t.Context(), string(authz.PlatformDomain), "acct-system")
	if err != nil {
		t.Fatalf("reading the membership: %v", err)
	}
	if !found || !held.Active {
		t.Fatalf("the system membership = %+v, found %v; want an active row", held, found)
	}
	bindings, err := manager.BindingsOf(t.Context(), string(authz.PlatformDomain), "acct-system")
	if err != nil {
		t.Fatalf("reading the bindings: %v", err)
	}
	if !slices.ContainsFunc(bindings, func(b authz.Binding) bool { return b.Role == tenantAdminRole }) {
		t.Fatalf("the system administrator's bindings = %+v, want the tenant-admin role", bindings)
	}
	if slices.ContainsFunc(bindings, func(b authz.Binding) bool { return b.Role == platformAdminRole }) {
		t.Errorf("the system administrator holds the platform-administrator role: %+v", bindings)
	}
}

// TestSeedRefusesAConfigurationWithNoPlatformAdministrator holds that an account list with nobody
// marked platform-admin is refused.
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

// TestSeedRefusesTheSystemTenantInTheTenantList holds that a tenant list naming the system tenant is
// refused.
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

// TestSeedRefusesAPersonalPrefixedTenantInTheTenantList holds that a tenant list naming a
// personal-prefixed tenant is refused.
func TestSeedRefusesAPersonalPrefixedTenantInTheTenantList(t *testing.T) {
	t.Parallel()
	in := seedInput{
		Accounts: []seedAccount{{
			Identifier: "operator", Password: "operator-dev-secret", PlatformAdmin: true,
		}},
		Tenants: []seedTenantEntry{{
			Name: tenantpersonal.IDPrefix + "opaque", Admins: []string{"operator"},
		}},
	}
	err := checkTenants(in)
	if err == nil {
		t.Fatal("checkTenants accepted a personal-prefixed tenant in seed.tenants")
	}
	for _, want := range []string{tenantpersonal.IDPrefix, "seed.tenants", "account creation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal = %q, want it to name %q", err, want)
		}
	}
}

// TestSeedWritesAMissingAdministratorsMembershipOnEveryRun holds that a missing administrator
// membership is filled rather than preserved.
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
	members, err := memberships.Members(ctx, "acme", authn.MemberQuery{})
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("the tenant holds %d memberships, want one", len(members))
	}
}

// TestSeedLeavesASuspendedAdministratorsMembershipSuspended holds that an existing suspended
// membership is neither resumed nor bound.
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

// TestSeedAddsAMissingAdministratorsBindingOnEveryRun holds that a missing tenant-admin binding is
// added and a second run writes nothing.
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

// TestSeedRestoresARemovedTenantAdministratorsMembership holds that the seed reconciles a declared
// administrator's membership and binding on every run.
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

// TestSeedTenantWritesTheConfiguredOwner holds that a seeded tenant carries the configured owner,
// repairing a row that predates the rule.
func TestSeedTenantWritesTheConfiguredOwner(t *testing.T) {
	t.Parallel()

	roster := newFakeRoster()
	if err := seedTenant(t.Context(), roster, "acme", "acct-new"); err != nil {
		t.Fatalf("seedTenant: %v", err)
	}
	created, found, err := roster.Get(t.Context(), "acme")
	if err != nil || !found {
		t.Fatalf("the seeded tenant is %+v, %v", created, err)
	}
	if created.OwnerAccount != "acct-new" {
		t.Errorf("the seeded tenant's owner is %q, want acct-new", created.OwnerAccount)
	}

	legacy := newFakeRoster()
	legacy.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
	if err := seedTenant(t.Context(), legacy, "acme", "acct-repair"); err != nil {
		t.Fatalf("seedTenant on the ownerless row: %v", err)
	}
	repaired, _, _ := legacy.Get(t.Context(), "acme")
	if repaired.OwnerAccount != "acct-repair" {
		t.Errorf("the ownerless row's owner is %q, want acct-repair", repaired.OwnerAccount)
	}

	held := newFakeRoster()
	held.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive, OwnerAccount: "acct-standing"}
	if err := seedTenant(t.Context(), held, "acme", "acct-other"); err != nil {
		t.Fatalf("seedTenant over a standing owner: %v", err)
	}
	kept, _, _ := held.Get(t.Context(), "acme")
	if kept.OwnerAccount != "acct-standing" {
		t.Errorf("the standing owner became %q, want it left alone", kept.OwnerAccount)
	}

	tombstone := newFakeRoster()
	tombstone.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusDeleted}
	if err := seedTenant(t.Context(), tombstone, "acme", "acct-any"); err != nil {
		t.Fatalf("seedTenant on a tombstone: %v", err)
	}
	deleted, _, _ := tombstone.Get(t.Context(), "acme")
	if deleted.OwnerAccount != "" {
		t.Errorf("the tombstone's owner is %q, want it left without one", deleted.OwnerAccount)
	}
}

// TestSeedTenantLeavesATransferThatLandsDuringTheRepair holds that an ownership transfer landing
// before the repair is not overwritten.
func TestSeedTenantLeavesATransferThatLandsDuringTheRepair(t *testing.T) {
	t.Parallel()

	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{
		ID: "acme", Name: "acme", Status: tenant.StatusActive, OwnerAccount: "acct-transferred",
	}
	roster.ownerless = []tenant.ID{"acme"}

	if err := seedTenant(t.Context(), roster, "acme", "acct-seed"); err != nil {
		t.Fatalf("seedTenant: %v", err)
	}
	if got := roster.rows["acme"].OwnerAccount; got != "acct-transferred" {
		t.Errorf("the repair overwrote the transferred owner with %q", got)
	}
}

// TestSeedFailsOnASystemNameConflict holds that a business tenant already holding the name "system"
// fails the seed with the conflict named.
func TestSeedFailsOnASystemNameConflict(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.createErr = fmt.Errorf("%w: name %q is held by tenant %q",
		tenant.ErrNameConflict, string(authz.PlatformDomain), "legacy")
	err := seedTenant(context.Background(), roster, string(authz.PlatformDomain), "")
	if err == nil {
		t.Fatal("seedTenant accepted a name conflict as an existing tenant")
	}
	if !errors.Is(err, tenant.ErrNameConflict) {
		t.Errorf("the refusal = %v, want it to carry ErrNameConflict", err)
	}
	if !strings.Contains(err.Error(), string(authz.PlatformDomain)) {
		t.Errorf("the refusal = %q, want it to name the tenant whose name conflicts", err)
	}
}

// TestSeedBindsTheTenantAdministratorsInTheDeclaredOrder holds that the binding order is the
// configured list's order.
func TestSeedBindsTheTenantAdministratorsInTheDeclaredOrder(t *testing.T) {
	t.Parallel()
	manager := &orderManager{}
	catalog := fixedCatalog{
		pairs:    map[authz.Permission][2]string{"crm.deal.read": {"deal", "read"}},
		governed: map[authz.Permission]bool{"crm.deal.read": true},
	}
	seeder := &orderSeeder{manager: manager}
	if err := seedAdministration(t.Context(), manager, seeder,
		"acme", tenantAdminRole, tenantAdminDescriptionKey, governedGrants(catalog)); err != nil {
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

// TestSeedMarksADeclaredTenantsExistingRoleBuiltin holds that an existing non-builtin role keeps its
// row, gains missing grants and is marked builtin.
func TestSeedMarksADeclaredTenantsExistingRoleBuiltin(t *testing.T) {
	t.Parallel()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{{Tenant: "acme", Role: tenantAdminRole}}
	catalog := fixedCatalog{
		pairs: map[authz.Permission][2]string{
			"crm.deal.read":  {"deal", "read"},
			"crm.deal.write": {"deal", "write"},
		},
		governed: map[authz.Permission]bool{
			"crm.deal.read":  true,
			"crm.deal.write": true,
		},
	}
	seeder := newFakeSeeder(grants)

	if err := seedAdministration(t.Context(), manager, seeder,
		"acme", tenantAdminRole, tenantAdminDescriptionKey, governedGrants(catalog)); err != nil {
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

// TestSeedRepairsAnUndeclaredTenantsAdministration holds that a roster tenant the configuration does
// not list still gets its role, grants and builtin mark.
func TestSeedRepairsAnUndeclaredTenantsAdministration(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "roster-repair.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)

	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("the first seed run failed: %v\n%s", res.err, res.combined())
	}
	createTenantInRoster(t, dsn, "rogue")

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("the second seed run failed: %v\n%s", res.err, res.combined())
	}
	for _, want := range []string{
		"seeded: role rogue/" + string(tenantAdminRole),
		"seeded: role rogue/" + string(tenantAdminRole) + " (builtin mark)",
		"grant(s) on rogue/" + string(tenantAdminRole),
	} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("the second run does not report %q:\n%s", want, res.stdout)
		}
	}
	for _, line := range strings.Split(res.stdout, "\n") {
		if strings.Contains(line, "member") && strings.Contains(line, "rogue") {
			t.Errorf("the seed wrote an administrator row for a tenant the configuration does not declare: %s", line)
		}
	}
}

// TestSeedRepairsEveryNonPersonalTenantInTheRoster holds that every non-personal tenant is repaired
// while a personal domain is left to the host.
func TestSeedRepairsEveryNonPersonalTenantInTheRoster(t *testing.T) {
	t.Parallel()
	personal := tenantpersonal.IDPrefix + "acct-1"
	domains := []string{"acme", string(authz.PlatformDomain), personal}
	roster := newFakeRoster()
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	for _, id := range domains {
		name := id
		if id == personal {
			name = "alice"
		}
		roster.rows[tenant.ID(id)] = tenant.Tenant{
			ID: tenant.ID(id), Name: name, Status: tenant.StatusActive,
		}
		manager.roles[id] = []authz.RoleRecord{{Tenant: id, Role: tenantAdminRole}}
	}
	catalog := fixedCatalog{
		pairs:    map[authz.Permission][2]string{"crm.deal.read": {"deal", "read"}},
		governed: map[authz.Permission]bool{"crm.deal.read": true},
	}
	seeder := newFakeSeeder(grants)

	if err := seedRosterAdministration(t.Context(), roster, manager, seeder,
		&recordingReconciler{}, newRecordedAudit(), catalog); err != nil {
		t.Fatalf("seedRosterAdministration: %v", err)
	}
	for _, id := range domains[:2] {
		roles, err := manager.Roles(t.Context(), id)
		if err != nil {
			t.Fatalf("reading the roles of %q: %v", id, err)
		}
		if len(roles) != 1 || roles[0].Role != tenantAdminRole || !roles[0].Builtin {
			t.Errorf("the roles of %q = %+v, want one builtin tenant-admin", id, roles)
		}
		if held := grants.held(id, tenantAdminRole); len(held) != 1 {
			t.Errorf("%q holds %d grant(s), want the declared one", id, len(held))
		}
	}
	roles, err := manager.Roles(t.Context(), personal)
	if err != nil {
		t.Fatalf("reading the roles of the personal domain: %v", err)
	}
	if len(roles) != 1 || roles[0].Builtin {
		t.Errorf("the personal domain's roles = %+v, want one left unfrozen", roles)
	}
	if held := grants.held(personal, tenantAdminRole); len(held) != 0 {
		t.Errorf("the personal domain holds %d seeded grant(s), want none", len(held))
	}
	if manager.markBuiltinCalls != 2 {
		t.Errorf("the seed marked %d role(s) builtin, want the two non-personal domains", manager.markBuiltinCalls)
	}
}

// TestSeedLeavesADeletedTenantAlone holds that a tombstone's roles, grants and builtin mark are not
// recreated.
func TestSeedLeavesADeletedTenantAlone(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.rows["gone"] = tenant.Tenant{ID: "gone", Name: "gone", Status: tenant.StatusDeleted}
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{{Tenant: "acme", Role: tenantAdminRole}}
	catalog := fixedCatalog{
		pairs:    map[authz.Permission][2]string{"crm.deal.read": {"deal", "read"}},
		governed: map[authz.Permission]bool{"crm.deal.read": true},
	}

	if err := seedRosterAdministration(t.Context(), roster, manager, newFakeSeeder(grants),
		&recordingReconciler{}, newRecordedAudit(), catalog); err != nil {
		t.Fatalf("seedRosterAdministration: %v", err)
	}

	roles, err := manager.Roles(t.Context(), "gone")
	if err != nil {
		t.Fatalf("reading the deleted tenant's roles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("the deleted tenant holds roles %+v, want none", roles)
	}
	if held := grants.held("gone", tenantAdminRole); len(held) != 0 {
		t.Errorf("the deleted tenant holds grants %+v, want none", held)
	}
	if manager.markBuiltinCalls != 1 {
		t.Errorf("the seed marked %d role(s) builtin, want only the active tenant's", manager.markBuiltinCalls)
	}
	if roles, err := manager.Roles(t.Context(), "acme"); err != nil || len(roles) != 1 || !roles[0].Builtin {
		t.Errorf("the active tenant's roles = %+v (%v), want one builtin tenant-admin", roles, err)
	}
}

// TestSeedConvergesAnExistingTenantToTheGovernedSet holds that an existing tenant-admin role gains
// every governed grant and retires only the declared permissions that are no longer governed.
func TestSeedConvergesAnExistingTenantToTheGovernedSet(t *testing.T) {
	t.Parallel()
	roster := newFakeRoster()
	roster.rows["acme"] = tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	manager.roles["acme"] = []authz.RoleRecord{
		{Tenant: "acme", Role: tenantAdminRole, Builtin: true, DescriptionKey: tenantAdminDescriptionKey},
	}
	catalog := fixedCatalog{
		pairs: map[authz.Permission][2]string{
			"authz.tenant.role.read": {"authz.tenant.role", "read"},
			"crm.deal.read":          {"deal", "read"},
			"crm.deal.write":         {"deal", "write"},
		},
		governed: map[authz.Permission]bool{
			"authz.tenant.role.read": true,
			"crm.deal.read":          true,
		},
	}
	reconciler := &recordingReconciler{}

	if err := seedRosterAdministration(t.Context(), roster, manager, newFakeSeeder(grants),
		reconciler, newRecordedAudit(), catalog); err != nil {
		t.Fatalf("seedRosterAdministration: %v", err)
	}

	want := governedGrants(catalog)
	got := grants.held("acme", tenantAdminRole)
	if len(got) != len(want) {
		t.Fatalf("the existing tenant-admin holds %v, want the governed set %v", got, want)
	}
	for _, grant := range want {
		if !slices.Contains(got, grant) {
			t.Errorf("the existing tenant-admin does not hold the governed grant %+v; it holds %v", grant, got)
		}
	}

	if len(reconciler.calls) != 1 {
		t.Fatalf("the reconciler was called %d time(s), want once", len(reconciler.calls))
	}
	call := reconciler.calls[0]
	if call.tenant != "acme" || call.role != tenantAdminRole {
		t.Errorf("the reconciler was asked for %s/%s, want acme/%s", call.tenant, call.role, tenantAdminRole)
	}
	if !slices.Equal(call.grants, want) {
		t.Errorf("the reconciler's target = %+v, want the governed set %+v", call.grants, want)
	}
	if retired := retiredTenantAdminGrants(catalog); !slices.Equal(call.retired, retired) {
		t.Errorf("the reconciler's retired set = %+v, want %+v", call.retired, retired)
	}
}

// TestTheSeedRoleDescriptionKeysMatchTheRoleCatalog holds the seed's builtin role keys to the i18n
// catalog in every locale.
func TestTheSeedRoleDescriptionKeysMatchTheRoleCatalog(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		platformAdminDescriptionKey: true,
		tenantAdminDescriptionKey:   true,
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		raw := roleCatalogBytes(t, locale)
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("the role catalog for %s is not a JSON object of message text: %v", locale, err)
		}
		if len(entries) != len(want) {
			t.Errorf("the role catalog for %s carries %d key(s), want the seed's %d role keys",
				locale, len(entries), len(want))
		}
		for key := range want {
			if _, ok := entries[key]; !ok {
				t.Errorf("the role catalog for %s carries no entry for %q, a key the seed writes into a builtin role",
					locale, key)
			}
		}
		for key := range entries {
			if !want[key] {
				t.Errorf("the role catalog for %s carries %q, which the seed never writes into a builtin role",
					locale, key)
			}
		}
	}
}

func roleCatalogBytes(t *testing.T, locale string) []byte {
	t.Helper()
	for _, catalog := range core.Resources[i18n.Catalog](core.ProcessRegistry) {
		if catalog.Value.Pattern != "rolelocales/*.json" {
			continue
		}
		raw, err := fs.ReadFile(catalog.Value.FS, "rolelocales/"+locale+".json")
		if err != nil {
			t.Fatalf("reading the compiled-in role catalog for %s: %v", locale, err)
		}
		return raw
	}
	t.Fatal("the process registry declares no role catalog whose pattern is rolelocales/*.json")
	return nil
}

// TestSeedLeavesAPersonalDomainsRoleUnfrozen holds that a declared personal-prefixed tenant gets its
// role and grants but no builtin mark.
func TestSeedLeavesAPersonalDomainsRoleUnfrozen(t *testing.T) {
	t.Parallel()
	domain := tenantpersonal.IDPrefix + "acct-1"
	grants := newFakeGrants()
	manager := newFakeManager(grants)
	catalog := fixedCatalog{
		pairs:    map[authz.Permission][2]string{"crm.deal.read": {"deal", "read"}},
		governed: map[authz.Permission]bool{"crm.deal.read": true},
	}
	seeder := newFakeSeeder(grants)

	if err := seedAdministration(t.Context(), manager, seeder,
		domain, tenantAdminRole, tenantAdminDescriptionKey, governedGrants(catalog)); err != nil {
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

type orderSeeder struct {
	authz.Seeder
	manager *orderManager
}

func (s *orderSeeder) SeedGrants(context.Context, string, authz.Role, []authz.Grant) (int, error) {
	s.manager.order = append(s.manager.order, "seed:"+string(authz.TenantAdminRole))
	return 0, nil
}

// TestSeedRefusesAnAccountThatNamesOnlyAnEmailAddress holds that an email entry with no username is
// refused before anything is written.
func TestSeedRefusesAnAccountThatNamesOnlyAnEmailAddress(t *testing.T) {
	t.Parallel()
	emailOnly := seedAccount{Identifier: "operator@example.com", Password: "operator-dev-secret"}
	parseErr := emailOnly.check()
	if parseErr == nil || !strings.Contains(parseErr.Error(), "seed.accounts") {
		t.Fatalf("an email-only account gave %v, want a refusal naming seed.accounts", parseErr)
	}
	err := validateSeedInput(seedInput{Accounts: []seedAccount{{
		Identifier: "operator@example.com", Password: "operator-dev-secret", PlatformAdmin: true,
	}}})
	if err == nil || !strings.Contains(err.Error(), "seed.accounts") {
		t.Fatalf("the seed input gave %v, want a refusal naming seed.accounts", err)
	}

	named := seedAccount{
		Identifier: "operator@example.com", Username: "operator", Password: "operator-dev-secret",
	}
	if err = named.check(); err != nil {
		t.Errorf("an email account with a username gave %v, want nil", err)
	}
	in := seedCreateAccount(authn.IdentifierEmail, named.Identifier, named)
	if in.Username != "operator" || in.Email != "operator@example.com" ||
		in.EmailVerifiedVia.Via != authn.VerifiedViaAdmin {
		t.Errorf("the seeded account is %+v, want the address as an admin assertion and operator as its username", in)
	}
}

// TestSeedSkipsEveryPathIntoATombstone holds that a tombstone gains no administrator membership,
// binding or settings row and the run still succeeds.
func TestSeedSkipsEveryPathIntoATombstone(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "tombstone-seed.db")
	body := configBody(t, freePorts(t, listenerCount), dsn)
	const needle = "  tenants:\n    - name: demo\n      admins:\n        - operator\n"
	replacement := needle +
		"      settings:\n        - key: \"tenant.admission.require-sso\"\n          value: \"true\"\n" +
		"  members:\n    - tenant: \"demo\"\n      account: \"ops\"\n"
	replaced := strings.Replace(body, needle, replacement, 1)
	if replaced == body {
		t.Fatalf("the configuration template holds no seed tenant entry to extend")
	}
	config := writeConfigFile(t, replaced)

	createTenantInRoster(t, dsn, "demo")
	deleteTenantInRoster(t, dsn, "demo")

	res := runCommand(t, config, "sqlite", seedCommand)
	if res.err != nil {
		t.Fatalf("seeding a database containing a tombstone failed: %v\n%s", res.err, res.combined())
	}
	for _, want := range []string{"skipped: tenant demo (deleted)", "skipped: member ops in demo (deleted tenant)"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("the seed did not report %q:\n%s", want, res.stdout)
		}
	}

	withMemberships(t, dsn, func(ctx context.Context, accounts authn.Accounts, memberships authn.Memberships) {
		for _, id := range []authn.AccountID{operatorID(t, ctx, accounts), memberID(t, ctx, accounts)} {
			if _, found, err := memberships.Membership(ctx, "demo", id); err != nil {
				t.Fatalf("reading the membership of %s in demo: %v", id, err)
			} else if found {
				t.Errorf("the tombstone gained a membership for %s", id)
			}
		}
	})
	withSettings(t, dsn, func(ctx context.Context, store settings.Store) {
		rows, err := store.List(ctx, settings.ForTenant("demo"))
		if err != nil {
			t.Fatalf("listing the tombstone's settings: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("the tombstone gained %d settings row(s): %+v", len(rows), rows)
		}
	})
	withRawHandle(t, dsn, func(_ context.Context, handle *gorm.DB) {
		var n int64
		if err := handle.Table("authz_bindings").Where("tenant_id = ?", "demo").Count(&n).Error; err != nil {
			t.Fatalf("counting the tombstone's bindings: %v", err)
		}
		if n != 0 {
			t.Errorf("the tombstone gained %d binding(s)", n)
		}
	})
}
