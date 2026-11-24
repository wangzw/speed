package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// counterRoster is a tenant catalog carrying a fixed list. The counter reads
// the roster through List; a case that scripts a failure answers it instead.
type counterRoster struct {
	tenant.Catalog
	tenants []tenant.Tenant
	err     error
}

func (r counterRoster) List(context.Context) ([]tenant.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return append([]tenant.Tenant(nil), r.tenants...), nil
}

// counterWriteGuard scripts which domains the host refuses as personal or
// otherwise off-limits to business management, and an unreadable answer.
type counterWriteGuard struct {
	denied map[string]bool
	err    error
}

func (g counterWriteGuard) Denied(_ context.Context, id string, _ authz.WriteOp) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	return g.denied[id], nil
}

// bindTenantAdmin writes one tenant-administrator binding straight into the
// table, bypassing the manager so that a case sets up exactly the rows it
// names.
func bindTenantAdmin(t *testing.T, p *product, tenantID, account string) {
	t.Helper()
	if err := p.crossing(t.Context()).Exec(
		"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
		tenantID, account, string(authz.TenantAdminRole), time.Now().UTC()).Error; err != nil {
		t.Fatalf("seeding the binding of %q in %q: %v", account, tenantID, err)
	}
}

// activeTenants builds the roster rows the counter judges.
func activeTenants(ids ...string) []tenant.Tenant {
	out := make([]tenant.Tenant, 0, len(ids))
	for _, id := range ids {
		out = append(out, tenant.Tenant{ID: tenant.ID(id), Name: id, Status: tenant.StatusActive})
	}
	return out
}

// TestSoleTenantAdminTenantFindsTheOnlyBusinessAdministrator holds the hit: the
// account holds the one tenant-administrator binding of an active business
// tenant, so this tenant is the one a deletion would leave ungoverned.
func TestSoleTenantAdminTenantFindsTheOnlyBusinessAdministrator(t *testing.T) {
	p := newProductOver(t)
	// acc1 shares acme with acc2 and is globex's only administrator, so globex
	// is the tenant the answer names.
	bindTenantAdmin(t, p, "acme", "acc1")
	bindTenantAdmin(t, p, "acme", "acc2")
	bindTenantAdmin(t, p, "globex", "acc1")
	p.roster = counterRoster{tenants: activeTenants("acme", "globex")}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if !sole || id != "globex" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want (globex, true)", id, sole)
	}
}

// A second tenant administrator means the tenant can lose this account and stay
// governed: the answer is not sole.
func TestASecondTenantAdministratorAllowsTheDeletion(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	bindTenantAdmin(t, p, "acme", "acc2")
	p.roster = counterRoster{tenants: activeTenants("acme")}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if sole || id != "" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want no sole tenant", id, sole)
	}
}

// A personal tenant is the host's policy and is not a business tenant: the
// host's write guard refuses it, so the counter leaves it out.
func TestAPersonalTenantIsNotCounted(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "personal-acc1", "acc1")
	p.roster = counterRoster{tenants: []tenant.Tenant{
		{ID: "personal-acc1", Name: "acc1", Status: tenant.StatusActive},
	}}
	p.writeGuard = counterWriteGuard{denied: map[string]bool{"personal-acc1": true}}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if sole || id != "" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want a personal tenant left out", id, sole)
	}
}

// A disabled tenant is not in service, so its administrator floor is not a
// reason to keep the account.
func TestADisabledTenantIsNotCounted(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{tenants: []tenant.Tenant{
		{ID: "acme", Name: "acme", Status: tenant.StatusDisabled},
	}}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if sole || id != "" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want a disabled tenant left out", id, sole)
	}
}

// The platform domain is not a business tenant.
func TestThePlatformDomainIsNotCounted(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, string(authz.PlatformDomain), "acc1")
	p.roster = counterRoster{tenants: activeTenants(string(authz.PlatformDomain))}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if sole || id != "" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want the platform domain left out", id, sole)
	}
}

// An account with no tenant-administrator binding has no floor to protect.
func TestAnAccountWithNoTenantAdminBindingIsNotSole(t *testing.T) {
	p := newProductOver(t)
	p.roster = counterRoster{tenants: activeTenants("acme")}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if sole || id != "" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want no sole tenant", id, sole)
	}
}

// A roster that cannot answer is a failure, never "no sole tenant": reading it
// as permission would let the deletion through when the floor is unknown.
func TestTheCounterFailsClosedWhenTheRosterCannotAnswer(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{err: errors.New("the roster is down")}

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error although the roster could not be read")
	}
}

// A host write guard that cannot answer is a failure too.
func TestTheCounterFailsClosedWhenTheGuardCannotAnswer(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{tenants: activeTenants("acme")}
	p.writeGuard = counterWriteGuard{err: errors.New("the guard is down")}

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error although the write guard could not be read")
	}
}

// With no write guard assembled every active tenant counts as a business
// tenant.
func TestWithoutAWriteGuardEveryActiveTenantCounts(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "personal-acc1", "acc1")
	p.roster = counterRoster{tenants: []tenant.Tenant{
		{ID: "personal-acc1", Name: "acc1", Status: tenant.StatusActive},
	}}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if !sole || id != "personal-acc1" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want (personal-acc1, true) with no guard", id, sole)
	}
}

// With no roster assembled a process cannot say which tenants are in service,
// so the answer is a failure rather than a pass for an account holding any
// tenant-administrator binding.
func TestTheCounterFailsClosedWithoutARoster(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error with no roster assembled")
	}
}

// The answer is deterministic when several active business tenants each have
// this account as their only administrator: it names the first by identifier.
func TestTheCounterAnswersTheFirstTenantByIdentifier(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "zulu", "acc1")
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{tenants: activeTenants("zulu", "acme")}

	id, sole, err := p.SoleTenantAdminTenant(t.Context(), "acc1")
	if err != nil {
		t.Fatalf("SoleTenantAdminTenant: %v", err)
	}
	if !sole || id != "acme" {
		t.Errorf("SoleTenantAdminTenant = (%q, %t), want (acme, true)", id, sole)
	}
}

// TestLockAccountAdministrationSerialisesOneTenantsDeletions holds the lock
// a deletion takes: one holding of the lock excludes another account's
// deletion in the same tenant, so the two cannot each read the other's
// binding as present and then together leave the tenant with no
// administrator.
func TestLockAccountAdministrationSerialisesOneTenantsDeletions(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	bindTenantAdmin(t, p, "acme", "acc2")
	bindTenantAdmin(t, p, "globex", "acc1")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := p.LockAccountAdministration(ctx, "acc1")
	if err != nil {
		t.Fatalf("LockAccountAdministration(acc1): %v", err)
	}

	acquired := make(chan func(), 1)
	failed := make(chan error, 1)
	go func() {
		release, lockErr := p.LockAccountAdministration(ctx, "acc2")
		if lockErr != nil {
			failed <- lockErr
			return
		}
		acquired <- release
	}()

	select {
	case <-acquired:
		t.Fatal("a second deletion in the same tenant took the lock while the first held it")
	case lockErr := <-failed:
		t.Fatalf("the second LockAccountAdministration failed: %v", lockErr)
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing twice is idempotent; the second call must not release anything
	// the second deletion now holds.
	first()
	first()

	select {
	case release := <-acquired:
		release()
	case lockErr := <-failed:
		t.Fatalf("the second LockAccountAdministration failed: %v", lockErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the second deletion did not take the lock after the first was released")
	}
}

// An account with no tenant-administrator binding locks nothing and answers a
// release that is safe to call.
func TestLockAccountAdministrationLocksNothingForAnUnboundAccount(t *testing.T) {
	p := newProductOver(t)
	release, err := p.LockAccountAdministration(t.Context(), "acc-with-no-binding")
	if err != nil {
		t.Fatalf("LockAccountAdministration: %v", err)
	}
	release()
	release()
}
