package policy

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/persist"
	tenancy "github.com/vislake/speed/pkg/tenant"
)

// The three tables' row shapes. Each embeds the tenant convention, so its
// tenant column leads the key.
type roleRow struct {
	tenancy.Model
	Role           string    `gorm:"column:role;primaryKey"`
	Builtin        bool      `gorm:"column:builtin;not null"`
	DescriptionKey string    `gorm:"column:description_key;not null"`
	Description    string    `gorm:"column:description;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (roleRow) TableName() string { return "authz_roles" }

type grantRow struct {
	tenancy.Model
	Role      string    `gorm:"column:role;primaryKey"`
	Resource  string    `gorm:"column:resource;primaryKey"`
	Action    string    `gorm:"column:action;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (grantRow) TableName() string { return "authz_grants" }

type bindingRow struct {
	tenancy.Model
	AccountID string    `gorm:"column:account_id;primaryKey"`
	Role      string    `gorm:"column:role;primaryKey"`
	GrantedAt time.Time `gorm:"column:granted_at;not null"`
}

func (bindingRow) TableName() string { return "authz_bindings" }

// crossing returns a session for statements that name their tenant themselves.
// The cross-tenant mark lifts the filter's condition and its fill, not the
// column, so a statement must name its tenant column explicitly.
func (p *product) crossing(ctx context.Context) *gorm.DB {
	return p.handle.WithContext(tenancy.CrossTenant(ctx))
}

// requireTenant refuses an empty tenant argument: a row it wrote would reach no
// tenant's read while the cross-tenant mark lets it through.
func requireTenant(tenant string) error {
	if tenant == "" {
		return fmt.Errorf("%w: the argument names no tenant, and a policy row belongs to one",
			authz.ErrTenantMismatch)
	}
	return nil
}

// Roles returns every role of a tenant, in role order.
func (p *product) Roles(ctx context.Context, tenant string) ([]authz.RoleRecord, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	var rows []roleRow
	if err := p.crossing(ctx).Where("tenant_id = ?", tenant).Order("role").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz/policy: listing the roles: %w", err)
	}
	out := make([]authz.RoleRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, authz.RoleRecord{
			Tenant:         string(r.TenantID),
			Role:           authz.Role(r.Role),
			Builtin:        r.Builtin,
			DescriptionKey: r.DescriptionKey,
			Description:    r.Description,
			CreatedAt:      r.CreatedAt,
		})
	}
	return out, nil
}

// Grants returns every grant a role holds within a tenant.
func (p *product) Grants(ctx context.Context, tenant string, role authz.Role) ([]authz.GrantRecord, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	var rows []grantRow
	err := p.crossing(ctx).Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Order("resource, action").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("authz/policy: listing the grants: %w", err)
	}
	out := make([]authz.GrantRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, authz.GrantRecord{
			Tenant:    string(r.TenantID),
			Role:      authz.Role(r.Role),
			Resource:  r.Resource,
			Action:    r.Action,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// Bindings returns every binding of a tenant.
func (p *product) Bindings(ctx context.Context, tenant string) ([]authz.Binding, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	var rows []bindingRow
	if err := p.crossing(ctx).Where("tenant_id = ?", tenant).
		Order("account_id, role").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz/policy: listing the bindings: %w", err)
	}
	return bindingsOf(rows), nil
}

// BindingsOf returns every binding a subject holds within a tenant.
func (p *product) BindingsOf(ctx context.Context, tenant, account string) ([]authz.Binding, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	var rows []bindingRow
	err := p.crossing(ctx).Where("tenant_id = ? AND account_id = ?", tenant, account).
		Order("role").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("authz/policy: listing the account's bindings: %w", err)
	}
	return bindingsOf(rows), nil
}

// BindingsOfRole returns one page of the bindings one role holds within a
// tenant, in account order. after is an exclusive account cursor; a non-positive
// limit returns every binding after it.
func (p *product) BindingsOfRole(ctx context.Context, tenant string, role authz.Role, after string, limit int) ([]authz.Binding, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	query := p.crossing(ctx).Where("tenant_id = ? AND role = ?", tenant, string(role))
	if after != "" {
		query = query.Where("account_id > ?", after)
	}
	query = query.Order("account_id")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []bindingRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz/policy: listing the bindings of the role %q of tenant %q: %w", role, tenant, err)
	}
	return bindingsOf(rows), nil
}

// GrantCounts returns how many grants each role holds within a tenant, from one
// GROUP BY read. A role with no grant is absent from the map and reads as zero.
func (p *product) GrantCounts(ctx context.Context, tenant string) (map[authz.Role]int, error) {
	if err := requireTenant(tenant); err != nil {
		return nil, err
	}
	var rows []struct {
		Role  string
		Count int
	}
	err := p.crossing(ctx).Model(&grantRow{}).
		Select("role, COUNT(*) AS count").
		Where("tenant_id = ?", tenant).
		Group("role").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("authz/policy: counting the grants of tenant %q: %w", tenant, err)
	}
	counts := make(map[authz.Role]int, len(rows))
	for _, row := range rows {
		counts[authz.Role(row.Role)] = row.Count
	}
	return counts, nil
}

func bindingsOf(rows []bindingRow) []authz.Binding {
	out := make([]authz.Binding, 0, len(rows))
	for _, r := range rows {
		out = append(out, authz.Binding{
			Tenant:    string(r.TenantID),
			Account:   r.AccountID,
			Role:      authz.Role(r.Role),
			GrantedAt: r.GrantedAt,
		})
	}
	return out
}

// PutRole creates or rewrites a role without the builtin mark. A record that
// carries the mark, or an existing builtin role, is refused: MarkRoleBuiltin is
// the one path that sets the mark.
func (p *product) PutRole(ctx context.Context, r authz.RoleRecord) error {
	row, err := roleRowFor(r)
	if err != nil {
		return err
	}
	changed := false
	err = p.write(ctx, func(tx *gorm.DB) error {
		var writeErr error
		changed, writeErr = putRoleIn(ctx, tx, r, row)
		return writeErr
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return p.afterWrite(ctx)
}

// roleRowFor judges a role write's arguments and builds the row it writes. It
// is shared by the context-taking write and the tx form, so the two refuse the
// same records.
func roleRowFor(r authz.RoleRecord) (roleRow, error) {
	if err := requireTenant(r.Tenant); err != nil {
		return roleRow{}, err
	}
	if r.Builtin {
		return roleRow{}, fmt.Errorf("%w: the role %q of tenant %q cannot be written with the builtin mark; "+
			"MarkRoleBuiltin sets it once the grants are in place", authz.ErrBuiltinRole, r.Role, r.Tenant)
	}
	if err := refuseReservedRole(r.Tenant, r.Role); err != nil {
		return roleRow{}, err
	}
	row := roleRow{
		Model:          tenancy.Model{TenantID: tenancy.ID(r.Tenant)},
		Role:           string(r.Role),
		DescriptionKey: r.DescriptionKey,
		Description:    r.Description,
		CreatedAt:      r.CreatedAt.UTC(),
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	return row, nil
}

// putRoleIn writes a role inside the transaction it is handed. It reports
// whether the call changed anything: a rewrite of a row that already carries
// the same description and description key is the same role, and a caller has
// nothing to reload or announce for it.
func putRoleIn(ctx context.Context, tx *gorm.DB, r authz.RoleRecord, row roleRow) (bool, error) {
	held, found, err := roleRowState(ctx, tx, r.Tenant, r.Role)
	if err != nil {
		return false, err
	}
	if found && held.Builtin {
		return false, fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, r.Role, r.Tenant)
	}
	if found && held.Description == row.Description && held.DescriptionKey == row.DescriptionKey {
		return false, nil
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "role"}},
		DoUpdates: clause.AssignmentColumns([]string{"description", "description_key"}),
	}).Create(&row).Error; err != nil {
		return false, fmt.Errorf("authz/policy: writing the role %q of tenant %q: %w", r.Role, r.Tenant, err)
	}
	return true, nil
}

// MarkRoleBuiltin freezes an existing role against the management surface: after
// it, this manager and the management surface refuse to rewrite or delete the
// role and to change its grants. SeedGrants still adds grants and
// ReconcileGrants converges them. An already-builtin role is no work and no
// error; an absent one is ErrUnknownRole.
func (p *product) MarkRoleBuiltin(ctx context.Context, tenant string, role authz.Role) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	changed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		builtin, found, err := roleState(ctx, tx, tenant, role)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, role, tenant)
		}
		if builtin {
			return nil
		}
		if err := tx.Model(&roleRow{}).
			Where("tenant_id = ? AND role = ?", tenant, string(role)).
			Update("builtin", true).Error; err != nil {
			return fmt.Errorf("authz/policy: marking the role %q of tenant %q builtin: %w", role, tenant, err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return p.afterWrite(ctx)
}

// DeleteRole removes a role and everything that names it: its grant and binding
// rows go with its role row, in one transaction. The engine reads grants and
// bindings and never the role table, so a leftover grant or binding would keep
// deciding for a role that no longer exists. A builtin role is refused; a role
// that is not there is no work and no error.
func (p *product) DeleteRole(ctx context.Context, tenant string, role authz.Role) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	removed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		var err error
		removed, err = deleteRoleIn(ctx, tx, tenant, role)
		return err
	})
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return p.afterWrite(ctx)
}

// deleteRoleIn removes a role and everything that names it inside the
// transaction it is handed. It reports whether it removed anything.
func deleteRoleIn(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (bool, error) {
	builtin, found, err := roleState(ctx, tx, tenant, role)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if builtin {
		return false, fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, role, tenant)
	}
	if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Delete(&grantRow{}).Error; err != nil {
		return false, fmt.Errorf("authz/policy: deleting the grants of the role %q of tenant %q: %w", role, tenant, err)
	}
	if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Delete(&bindingRow{}).Error; err != nil {
		return false, fmt.Errorf("authz/policy: deleting the bindings of the role %q of tenant %q: %w", role, tenant, err)
	}
	if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Delete(&roleRow{}).Error; err != nil {
		return false, fmt.Errorf("authz/policy: deleting the role %q of tenant %q: %w", role, tenant, err)
	}
	return true, nil
}

// PutGrant creates a grant on a role that exists in the tenant and is not
// builtin.
func (p *product) PutGrant(ctx context.Context, g authz.GrantRecord) error {
	if err := requireTenant(g.Tenant); err != nil {
		return err
	}
	rule := []string{string(g.Role), g.Tenant, g.Resource, g.Action}
	err := p.write(ctx, func(tx *gorm.DB) error {
		if err := requireEditableRole(ctx, tx, g.Tenant, g.Role); err != nil {
			return err
		}
		if err := persist.New(ctx, tx, tenancy.ID(g.Tenant)).AddPolicy("p", "p", rule); err != nil {
			return fmt.Errorf("authz/policy: writing the grant of tenant %q: %w", g.Tenant, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.afterWrite(ctx)
}

// DeleteGrant removes a grant. A builtin role's grants are refused.
func (p *product) DeleteGrant(ctx context.Context, g authz.GrantRecord) error {
	if err := requireTenant(g.Tenant); err != nil {
		return err
	}
	rule := []string{string(g.Role), g.Tenant, g.Resource, g.Action}
	err := p.write(ctx, func(tx *gorm.DB) error {
		if err := refuseBuiltin(ctx, tx, g.Tenant, g.Role); err != nil {
			return err
		}
		if err := persist.New(ctx, tx, tenancy.ID(g.Tenant)).RemovePolicy("p", "p", rule); err != nil {
			return fmt.Errorf("authz/policy: removing the grant of tenant %q: %w", g.Tenant, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.afterWrite(ctx)
}

// SeedGrants adds grants to a role, whether or not it is builtin, and is
// idempotent: a grant already held is not counted. The write is one transaction
// and the role check takes the role's row lock, so a concurrent delete of the
// role serialises against it. A grant is never removed, rewritten or
// re-timestamped here; the set only grows. An absent role is ErrUnknownRole, an
// empty tenant ErrTenantMismatch, and a seed that adds nothing writes and
// publishes nothing.
func (p *product) SeedGrants(ctx context.Context, tenant string, role authz.Role, grants []authz.Grant) (int, error) {
	if err := requireTenant(tenant); err != nil {
		return 0, err
	}
	added := 0
	err := p.write(ctx, func(tx *gorm.DB) error {
		var err error
		added, err = seedGrantsIn(ctx, tx, tenant, role, grants)
		return err
	})
	if err != nil {
		return 0, err
	}
	if added == 0 {
		return 0, nil
	}
	return added, p.afterWrite(ctx)
}

// seedGrantsIn adds the grants a role lacks inside the transaction it is
// handed, and reports how many it added.
func seedGrantsIn(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role, grants []authz.Grant) (int, error) {
	if _, found, err := roleState(ctx, tx, tenant, role); err != nil {
		return 0, err
	} else if !found {
		return 0, fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, role, tenant)
	}
	held, err := grantsHeld(ctx, tx, tenant, role)
	if err != nil {
		return 0, err
	}
	adapter := persist.New(ctx, tx, tenancy.ID(tenant))
	added := 0
	for _, g := range grants {
		if _, ok := held[g]; ok {
			continue
		}
		rule := []string{string(role), tenant, g.Resource, g.Action}
		if err := adapter.AddPolicy("p", "p", rule); err != nil {
			return 0, fmt.Errorf("authz/policy: seeding the grants of tenant %q: %w", tenant, err)
		}
		held[g] = struct{}{}
		added++
	}
	return added, nil
}

// grantsHeld reports the (resource, action) pairs a role holds in a tenant,
// read through the write's own transaction.
func grantsHeld(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (map[authz.Grant]struct{}, error) {
	var rows []grantRow
	err := tx.WithContext(tenancy.CrossTenant(ctx)).
		Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("authz/policy: reading the grants of the role %q of tenant %q: %w", role, tenant, err)
	}
	held := make(map[authz.Grant]struct{}, len(rows))
	for _, r := range rows {
		held[authz.Grant{Resource: r.Resource, Action: r.Action}] = struct{}{}
	}
	return held, nil
}

// PutBinding binds a role that exists in the tenant to a subject. A builtin
// role may be bound. An empty account is refused: a g line naming the empty
// account would match the decider's empty subject.
func (p *product) PutBinding(ctx context.Context, b authz.Binding) error {
	if err := p.checkBinding(ctx, b); err != nil {
		return err
	}
	changed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		var writeErr error
		changed, writeErr = putBindingIn(ctx, tx, b)
		return writeErr
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return p.afterWrite(ctx)
}

// checkBinding judges a binding write before its transaction opens: its shape,
// and the platform domain's membership.
func (p *product) checkBinding(ctx context.Context, b authz.Binding) error {
	if err := requireTenant(b.Tenant); err != nil {
		return err
	}
	if b.Account == "" {
		return fmt.Errorf("authz/policy: the binding of tenant %q names no account, and a policy row "+
			"belongs to one subject", b.Tenant)
	}
	if err := refuseReservedRole(b.Tenant, b.Role); err != nil {
		return err
	}
	return p.requirePlatformMember(ctx, b)
}

// putBindingIn writes a binding inside the transaction it is handed. It reports
// whether the call changed anything: binding a subject to a role it already
// holds is no work, and a caller has nothing to reload or announce for it.
func putBindingIn(ctx context.Context, tx *gorm.DB, b authz.Binding) (bool, error) {
	if _, found, err := roleState(ctx, tx, b.Tenant, b.Role); err != nil {
		return false, err
	} else if !found {
		return false, fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, b.Role, b.Tenant)
	}
	var held []bindingRow
	err := tx.WithContext(tenancy.CrossTenant(ctx)).
		Where("tenant_id = ? AND account_id = ? AND role = ?", b.Tenant, b.Account, string(b.Role)).
		Limit(1).Find(&held).Error
	if err != nil {
		return false, fmt.Errorf("authz/policy: reading the binding of tenant %q: %w", b.Tenant, err)
	}
	if len(held) > 0 {
		return false, nil
	}
	rule := []string{b.Account, string(b.Role), b.Tenant}
	if err := persist.New(ctx, tx, tenancy.ID(b.Tenant)).AddPolicy("g", "g", rule); err != nil {
		return false, fmt.Errorf("authz/policy: writing the binding of tenant %q: %w", b.Tenant, err)
	}
	return true, nil
}

// DeleteBinding removes a subject's binding to a role within a tenant. The last
// tenant administrator of a domain is not removable. The check and the delete are
// one transaction, and the whole removal runs under the tenant lock, taken before
// the count and released after the commit, so concurrent removals cannot each
// read enough bindings and together leave the domain with none. A removal naming
// no such binding changes nothing and is not refused.
//
// The account-deletion cascade must not come through here: it clears bindings
// with ClearAccountBindings, which takes no lock, because this call would
// re-enter the lock its removal already holds and deadlock.
func (p *product) DeleteBinding(ctx context.Context, tenant, account string, role authz.Role) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	release, err := p.tenantLock.Lock(ctx, tenant)
	if err != nil {
		return err
	}
	// The release is idempotent, so the deferred call covers the error paths
	// while the explicit one gives the lock back as soon as the write commits.
	defer release()
	rule := []string{account, string(role), tenant}
	if err := p.write(ctx, func(tx *gorm.DB) error {
		if role == authz.TenantAdminRole {
			if err := refuseLastTenantAdmin(ctx, tx, tenant, account); err != nil {
				return err
			}
		}
		if err := persist.New(ctx, tx, tenancy.ID(tenant)).RemovePolicy("g", "g", rule); err != nil {
			return fmt.Errorf("authz/policy: removing the binding of tenant %q: %w", tenant, err)
		}
		return nil
	}); err != nil {
		return err
	}
	release()
	return p.afterWrite(ctx)
}

// ClearAccountBindings removes every binding the account holds within the tenant
// in one statement, as the account-deletion path and the membership-removal
// cascade. The last-tenant-administrator rule does not apply. The tenant's other
// bindings and the account's bindings in other tenants stay, and so do the roles
// and grants. The delete is one transaction and idempotent.
//
// It takes no tenant lock: both callers can run while a removal holds the tenant
// lock, so a lock here would re-enter it and deadlock.
func (p *product) ClearAccountBindings(ctx context.Context, tenant, account string) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	removed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		var err error
		removed, err = clearAccountBindingsIn(ctx, tx, tenant, account)
		return err
	})
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return p.afterWrite(ctx)
}

// clearAccountBindingsIn removes a subject's bindings in one tenant inside the
// transaction it is handed, and reports whether it removed any.
func clearAccountBindingsIn(_ context.Context, tx *gorm.DB, tenant, account string) (bool, error) {
	result := tx.Where("tenant_id = ? AND account_id = ?", tenant, account).Delete(&bindingRow{})
	if result.Error != nil {
		return false, fmt.Errorf("authz/policy: clearing the bindings of account %q in tenant %q: %w",
			account, tenant, result.Error)
	}
	return result.RowsAffected > 0, nil
}

// refuseReservedRole refuses PlatformAdminRole in any domain other than the
// platform domain.
func refuseReservedRole(tenant string, role authz.Role) error {
	if role != authz.PlatformAdminRole || tenant == string(authz.PlatformDomain) {
		return nil
	}
	return fmt.Errorf("%w: the role %q exists only in the platform domain %q, not in tenant %q",
		authz.ErrReservedRole, role, authz.PlatformDomain, tenant)
}

// requirePlatformMember checks that the account a platform administrator binding
// names is an active member of the platform tenant. It fails closed: no checker
// refuses the write, and a checker error is the check being unavailable, not a
// verdict.
//
// It runs before the write's transaction: a call made while this module held a
// transaction could wait for a connection the pool already handed to it.
func (p *product) requirePlatformMember(ctx context.Context, b authz.Binding) error {
	if b.Role != authz.PlatformAdminRole || b.Tenant != string(authz.PlatformDomain) {
		return nil
	}
	if p.membership == nil {
		return fmt.Errorf("%w: the binding of tenant %q names the role %q",
			authz.ErrMissingMembershipChecker, b.Tenant, b.Role)
	}
	member, err := p.membership.IsMember(ctx, b.Tenant, b.Account)
	if err != nil {
		return fmt.Errorf("authz/policy: checking whether the account is a member of tenant %q: %w", b.Tenant, err)
	}
	if !member {
		return fmt.Errorf("%w: the account is not an active member of tenant %q, so the role %q "+
			"cannot be bound there", authz.ErrPlatformMembershipRequired, b.Tenant, b.Role)
	}
	return nil
}

// refuseLastTenantAdmin refuses removing the last tenant administrator binding
// of a domain. The read takes the rows under an exclusive lock, so another
// removal cannot read the same count before this transaction commits. A target
// row that is not there is no removal at all.
func refuseLastTenantAdmin(ctx context.Context, tx *gorm.DB, tenant, account string) error {
	rows, err := tenantAdminRows(ctx, tx, tenant, true)
	if err != nil {
		return err
	}
	if lastTenantAdminAfterRemoval(rows, account) {
		return fmt.Errorf("%w: account %q is the last %q of tenant %q", authz.ErrLastTenantAdmin,
			account, authz.TenantAdminRole, tenant)
	}
	return nil
}

// tenantAdminRows reads the tenant-administrator bindings of a domain, under an
// exclusive lock when lock is set.
func tenantAdminRows(ctx context.Context, db *gorm.DB, tenant string, lock bool) ([]bindingRow, error) {
	query := db.WithContext(tenancy.CrossTenant(ctx)).
		Where("tenant_id = ? AND role = ?", tenant, string(authz.TenantAdminRole))
	if lock {
		query = query.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	var rows []bindingRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("authz/policy: reading the tenant administrators of tenant %q: %w", tenant, err)
	}
	return rows, nil
}

// lastTenantAdminAfterRemoval reports whether removing account's binding would
// leave the domain with no tenant-administrator binding. A target row that is
// not among the rows is no removal, whatever the count.
func lastTenantAdminAfterRemoval(rows []bindingRow, account string) bool {
	target := false
	for _, row := range rows {
		if row.AccountID == account {
			target = true
			break
		}
	}
	return target && len(rows) == 1
}

// write runs fn inside one transaction on the cross-tenant-marked session. A
// check and the row it guards must share the transaction and the check must take
// the role row's lock (see roleState), otherwise a concurrent write can commit
// between them under READ COMMITTED.
func (p *product) write(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return p.crossing(ctx).Transaction(fn)
}

// roleState reports whether a role exists in a tenant and whether it is builtin.
// It reads with Find over a slice, so an absent role is not gorm.ErrRecordNotFound.
//
// It takes the role row's exclusive lock, held until the write's transaction
// ends, so a concurrent delete and this check serialise. Without the lock a
// delete can commit between the check and the row it guards under READ COMMITTED,
// leaving a grant or binding on a role that no longer exists. The clause is inert
// where the dialect has no row locks.
func roleState(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (builtin, found bool, err error) {
	row, found, err := roleRowState(ctx, tx, tenant, role)
	if err != nil {
		return false, false, err
	}
	return row.Builtin, found, nil
}

// roleRowState reads the role's row, locking it for the rest of the
// transaction. found is false when the tenant has no such role.
func roleRowState(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (roleRow, bool, error) {
	var rows []roleRow
	err := tx.WithContext(tenancy.CrossTenant(ctx)).
		Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Limit(1).Find(&rows).Error
	if err != nil {
		return roleRow{}, false, fmt.Errorf("authz/policy: reading the role %q of tenant %q: %w", role, tenant, err)
	}
	if len(rows) == 0 {
		return roleRow{}, false, nil
	}
	return rows[0], true, nil
}

// requireEditableRole refuses a write on a role that is absent or builtin.
func requireEditableRole(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) error {
	builtin, found, err := roleState(ctx, tx, tenant, role)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, role, tenant)
	}
	if builtin {
		return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, role, tenant)
	}
	return nil
}

// refuseBuiltin refuses a write on a builtin role and lets an absent one
// through, for the removal paths whose target need not exist.
func refuseBuiltin(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) error {
	builtin, found, err := roleState(ctx, tx, tenant, role)
	if err != nil {
		return err
	}
	if found && builtin {
		return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, role, tenant)
	}
	return nil
}

// afterWrite reloads this replica's snapshot and publishes the change, in that
// order, once the row is committed. They run on a context that is never
// cancelled, since the row is already in the table.
func (p *product) afterWrite(ctx context.Context) error {
	after := context.WithoutCancel(ctx)
	p.reload(after)
	p.publish(after)
	return nil
}
