package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

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

func bindTenantAdmin(t *testing.T, p *product, tenantID, account string) {
	t.Helper()
	if err := p.crossing(t.Context()).Exec(
		"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
		tenantID, account, string(authz.TenantAdminRole), time.Now().UTC()).Error; err != nil {
		t.Fatalf("seeding the binding of %q in %q: %v", account, tenantID, err)
	}
}

func activeTenants(ids ...string) []tenant.Tenant {
	out := make([]tenant.Tenant, 0, len(ids))
	for _, id := range ids {
		out = append(out, tenant.Tenant{ID: tenant.ID(id), Name: id, Status: tenant.StatusActive})
	}
	return out
}

// TestSoleTenantAdminTenantFindsTheOnlyBusinessAdministrator checks that SoleTenantAdminTenant finds the only business administrator.
func TestSoleTenantAdminTenantFindsTheOnlyBusinessAdministrator(t *testing.T) {
	p := newProductOver(t)
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

// TestASecondTenantAdministratorAllowsTheDeletion checks that a second tenant administrator allows the deletion.
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

// TestAPersonalTenantIsNotCounted checks that a personal tenant is not counted.
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

// TestADisabledTenantIsNotCounted checks that a disabled tenant is not counted.
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

// TestThePlatformDomainIsNotCounted checks that the platform domain is not counted.
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

// TestAnAccountWithNoTenantAdminBindingIsNotSole checks that an account with no tenant admin binding is not sole.
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

// TestTheCounterFailsClosedWhenTheRosterCannotAnswer checks that the counter fails closed when the roster cannot answer.
func TestTheCounterFailsClosedWhenTheRosterCannotAnswer(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{err: errors.New("the roster is down")}

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error although the roster could not be read")
	}
}

// TestTheCounterFailsClosedWhenTheGuardCannotAnswer checks that the counter fails closed when the guard cannot answer.
func TestTheCounterFailsClosedWhenTheGuardCannotAnswer(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")
	p.roster = counterRoster{tenants: activeTenants("acme")}
	p.writeGuard = counterWriteGuard{err: errors.New("the guard is down")}

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error although the write guard could not be read")
	}
}

// TestWithoutAWriteGuardEveryActiveTenantCounts checks that without a write guard every active tenant counts.
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

// TestTheCounterFailsClosedWithoutARoster checks that the counter fails closed without a roster.
func TestTheCounterFailsClosedWithoutARoster(t *testing.T) {
	p := newProductOver(t)
	bindTenantAdmin(t, p, "acme", "acc1")

	if _, _, err := p.SoleTenantAdminTenant(t.Context(), "acc1"); err == nil {
		t.Fatal("SoleTenantAdminTenant answered no error with no roster assembled")
	}
}

// TestTheCounterAnswersTheFirstTenantByIdentifier checks that the counter answers the first tenant by identifier.
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

// TestLockAccountAdministrationSerialisesOneTenantsDeletions checks that lock account administration serialises one tenants deletions.
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

// TestLockAccountAdministrationLocksNothingForAnUnboundAccount checks that lock account administration locks nothing for an unbound account.
func TestLockAccountAdministrationLocksNothingForAnUnboundAccount(t *testing.T) {
	p := newProductOver(t)
	release, err := p.LockAccountAdministration(t.Context(), "acc-with-no-binding")
	if err != nil {
		t.Fatalf("LockAccountAdministration: %v", err)
	}
	release()
	release()
}
