package policy

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// SoleTenantAdminTenant implements authz.TenantAdminCounter: it reads every
// tenant-administrator binding the account holds, keeps the tenants where that
// binding is the domain's only one, and answers the first such tenant that is a
// business tenant still in service.
//
// The count is over the whole table, across tenants: a policy row carries its
// tenant in the row, and an account's bindings in several domains are one
// account's rows, so the read crosses tenants under the cross-tenant mark.
//
// A tenant is in service when the roster carries it with the active status. A
// tenant the host's write guard refuses is not a business tenant: the guard is
// the host's statement of which domains are personal or otherwise outside
// management, and this capability does not infer a tenant's kind from its
// identifier or its grants. With no guard assembled every active tenant counts,
// which is the fail-closed side — the deletion is protected more widely, never
// less.
//
// A roster that is absent or cannot answer, and a guard that cannot answer, are
// failures. The caller treats them as unavailable; answering "not sole" would
// let a deletion through while the floor is unknown.
func (p *product) SoleTenantAdminTenant(ctx context.Context, account string) (string, bool, error) {
	if account == "" {
		return "", false, nil
	}
	rows, err := p.adminBindings(ctx, account)
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", false, nil
	}
	if p.roster == nil {
		return "", false, fmt.Errorf("authz/policy: the tenant roster is not assembled, so no tenant " +
			"administrator binding can be judged in service")
	}

	inService, err := p.serviceableTenants(ctx)
	if err != nil {
		return "", false, err
	}
	// The answer is the first tenant by identifier, so a case with several
	// qualifying tenants is stable across replicas and reads.
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	for _, id := range ids {
		tally := rows[id]
		if tally.total != 1 || !tally.holds || id == string(authz.PlatformDomain) {
			continue
		}
		if !inService[id] {
			continue
		}
		if p.writeGuard != nil {
			denied, guardErr := p.writeGuard.Denied(ctx, id, authz.WriteBinding)
			if guardErr != nil {
				return "", false, fmt.Errorf("authz/policy: asking the write guard about tenant %q: %w", id, guardErr)
			}
			if denied {
				continue
			}
		}
		return id, true, nil
	}
	return "", false, nil
}

// LockAccountAdministration implements authz.TenantAdminCounter: it takes the
// tenant lock of every tenant in which account holds a tenant-administrator
// binding, in identifier order, and answers one release for all of them.
//
// The tenants are read from the bindings, not from the roster: the lock's job
// is to exclude every other removal that judges one of these tenants' floors,
// and a tenant that is not in service is still a floor some other path counts
// against. Locking it costs nothing and closing the set over the roster would
// make a changing status a hole in the exclusion.
//
// The release is idempotent, and a lock that cannot be taken releases the ones
// already taken before returning the failure, so a deletion that does not
// proceed holds nothing.
func (p *product) LockAccountAdministration(ctx context.Context, account string) (func(), error) {
	if account == "" {
		return func() {}, nil
	}
	rows, err := p.adminBindings(ctx, account)
	if err != nil {
		return nil, err
	}
	tenants := make([]string, 0, len(rows))
	for id, tally := range rows {
		if tally.holds {
			tenants = append(tenants, id)
		}
	}
	slices.Sort(tenants)

	held := make([]func(), 0, len(tenants))
	var once sync.Once
	release := func() {
		once.Do(func() {
			// Reverse order, so the locks come back the way they were taken.
			for i := len(held) - 1; i >= 0; i-- {
				held[i]()
			}
		})
	}
	for _, id := range tenants {
		lease, lockErr := p.tenantLock.Lock(ctx, id)
		if lockErr != nil {
			release()
			return nil, fmt.Errorf("authz/policy: taking the tenant lock of %q for account %q: %w",
				id, account, lockErr)
		}
		held = append(held, lease)
	}
	return release, nil
}

// adminTally is one tenant's tenant-administrator bindings: how many the
// tenant holds, and whether the account this read is about holds one.
type adminTally struct {
	total int
	holds bool
}

// adminBindings reads every tenant-administrator binding in the table, grouped
// by tenant, and which of them name account.
func (p *product) adminBindings(ctx context.Context, account string) (map[string]adminTally, error) {
	var rows []struct {
		TenantID  string `gorm:"column:tenant_id"`
		AccountID string `gorm:"column:account_id"`
	}
	query := p.crossing(ctx).Model(&bindingRow{}).
		Select("tenant_id, account_id").
		Where("role = ?", string(authz.TenantAdminRole)).
		Order("tenant_id, account_id")
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz/policy: reading the tenant administrators: %w", err)
	}
	tally := make(map[string]adminTally, len(rows))
	for _, row := range rows {
		entry := tally[row.TenantID]
		entry.total++
		if row.AccountID == account {
			entry.holds = true
		}
		tally[row.TenantID] = entry
	}
	return tally, nil
}

// serviceableTenants is the set of tenants the roster carries as active. The
// read is one snapshot read; a tenant disabled after it is judged on the next
// call, and a tenant another replica registered since is simply absent here.
func (p *product) serviceableTenants(ctx context.Context) (map[string]bool, error) {
	tenants, err := p.roster.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("authz/policy: reading the roster: %w", err)
	}
	active := make(map[string]bool, len(tenants))
	for _, t := range tenants {
		if t.Status == tenant.StatusActive {
			active[string(t.ID)] = true
		}
	}
	return active, nil
}
