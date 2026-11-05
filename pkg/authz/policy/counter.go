package policy

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/vislake/speed/pkg/authn"
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

// OwnsActiveBusinessTenant implements authz.TenantOwnerCounter: it reads the
// roster and answers the first active business tenant whose owner column names
// account.
//
// A personal tenant is not a business tenant: the host's write guard is the
// statement of which domains are personal or otherwise outside management, and
// this capability does not infer a tenant's kind from its identifier or its
// grants. With no guard assembled every active tenant counts, which is the
// fail-closed side: the account is protected more widely, never less.
//
// A roster that is absent or cannot answer, and a guard that cannot answer, are
// failures. The caller treats them as unavailable; answering "owns nothing"
// would let a deletion or suspension through while the owner is unknown.
func (p *product) OwnsActiveBusinessTenant(ctx context.Context, account string) (string, bool, error) {
	if account == "" {
		return "", false, nil
	}
	if p.roster == nil {
		return "", false, fmt.Errorf("authz/policy: the tenant roster is not assembled, so no organization " +
			"owner can be judged")
	}
	tenants, err := p.roster.List(ctx)
	if err != nil {
		return "", false, fmt.Errorf("authz/policy: reading the roster: %w", err)
	}
	ids := make([]string, 0, 1)
	for _, t := range tenants {
		if t.Status != tenant.StatusActive || t.OwnerAccount != account {
			continue
		}
		id := string(t.ID)
		if id == string(authz.PlatformDomain) {
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
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	slices.Sort(ids)
	return ids[0], true, nil
}

// LockAccountAdministration implements authz.TenantAdminCounter: it takes the
// tenant lock of every tenant in which account holds a tenant-administrator
// binding and of every active business tenant in which account is an active
// member, in identifier order, and answers one release for all of them.
//
// The binding tenants are read from the policy table, not from the roster: the
// lock's job is to exclude every other removal that judges one of these tenants'
// floors, and a tenant that is not in service is still a floor some other path
// counts against. Locking it costs nothing and closing the set over the roster
// would make a changing status a hole in the exclusion.
//
// The membership tenants are the active business tenants the account belongs to
// without administering. An ownership transfer hands the organization to an
// active member and makes them its owner, so a suspension or deletion that
// judged only the tenants the account administers could interleave with that
// transfer and commit while the transfer makes the account an owner. Reading the
// memberships closes that hole. A personal or disabled tenant is left out, as
// the owner counter leaves it out: the transfer cannot make an account the owner
// of a domain the host has declared outside management. With no roster or write
// guard assembled the set is not narrowed, which is the fail-closed side.
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
	locked := make(map[string]bool, len(rows))
	for id, tally := range rows {
		if tally.holds {
			locked[id] = true
		}
	}
	if p.membershipReads != nil {
		memberOf, err := p.activeMembershipTenants(ctx, account)
		if err != nil {
			return nil, err
		}
		for id := range memberOf {
			locked[id] = true
		}
	}
	tenants := make([]string, 0, len(locked))
	for id := range locked {
		tenants = append(tenants, id)
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

// activeMembershipTenants is the set of active business tenants the account is
// an active member of, the tenants an ownership transfer could hand to it. A
// membership read, a roster read or a write-guard read that cannot answer is a
// failure: the caller must never take fewer locks than the transfers it is
// excluding.
func (p *product) activeMembershipTenants(ctx context.Context, account string) (map[string]bool, error) {
	memberships, err := p.membershipReads.Tenants(ctx, authn.AccountID(account))
	if err != nil {
		return nil, fmt.Errorf("authz/policy: reading the memberships of account %q: %w", account, err)
	}
	if len(memberships) == 0 {
		return nil, nil
	}
	var inService map[string]bool
	if p.roster != nil {
		inService, err = p.serviceableTenants(ctx)
		if err != nil {
			return nil, err
		}
	}
	out := make(map[string]bool, len(memberships))
	for _, member := range memberships {
		if !member.Active {
			continue
		}
		id := member.Tenant
		if id == string(authz.PlatformDomain) {
			continue
		}
		if inService != nil && !inService[id] {
			continue
		}
		if p.writeGuard != nil {
			denied, guardErr := p.writeGuard.Denied(ctx, id, authz.WriteBinding)
			if guardErr != nil {
				return nil, fmt.Errorf("authz/policy: asking the write guard about tenant %q: %w", id, guardErr)
			}
			if denied {
				continue
			}
		}
		out[id] = true
	}
	return out, nil
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
