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

// The three tables' row shapes, read and written one tenant at a time.
//
// Each embeds the tenant convention, so its tenant column leads the key and a
// statement under the cross-tenant mark still names the tenant it acts on.
type roleRow struct {
	tenancy.Model
	Role           string    `gorm:"column:role;primaryKey"`
	Builtin        bool      `gorm:"column:builtin;not null"`
	DescriptionKey string    `gorm:"column:description_key;not null"`
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
//
// The policy tables are written and read one tenant at a time but the caller
// names the tenant as an argument rather than through a request context, so the
// session carries the cross-tenant mark and every statement names its tenant
// column explicitly. The mark lifts the filter's condition and its fill, not
// the column: a statement that left the tenant column unnamed would be refused
// rather than reaching every tenant.
func (p *product) crossing(ctx context.Context) *gorm.DB {
	return p.handle.WithContext(tenancy.CrossTenant(ctx))
}

// requireTenant refuses an empty tenant argument.
//
// A write acts on one tenant alone, and an empty identifier names none: the row
// it would write reaches no tenant's read while the cross-tenant mark lets it
// through, so it is refused at the caller's own mistake rather than written.
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
// carries the mark is refused: the mark is what freezes a role, and a role
// created with it would be frozen before the grants that give it meaning are in
// place, so MarkRoleBuiltin is the one path that sets it, called once the
// grants are written. An existing builtin role is refused as well: its shape is
// fixed, and rewriting it would change what it means.
func (p *product) PutRole(ctx context.Context, r authz.RoleRecord) error {
	if err := requireTenant(r.Tenant); err != nil {
		return err
	}
	if r.Builtin {
		return fmt.Errorf("%w: the role %q of tenant %q cannot be written with the builtin mark; "+
			"MarkRoleBuiltin sets it once the grants are in place", authz.ErrBuiltinRole, r.Role, r.Tenant)
	}
	if err := refuseReservedRole(r.Tenant, r.Role); err != nil {
		return err
	}
	row := roleRow{
		Model:          tenancy.Model{TenantID: tenancy.ID(r.Tenant)},
		Role:           string(r.Role),
		DescriptionKey: r.DescriptionKey,
		CreatedAt:      r.CreatedAt.UTC(),
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	err := p.write(ctx, func(tx *gorm.DB) error {
		builtin, found, err := roleState(ctx, tx, r.Tenant, r.Role)
		if err != nil {
			return err
		}
		if found && builtin {
			return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, r.Role, r.Tenant)
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "role"}},
			DoUpdates: clause.AssignmentColumns([]string{"description_key"}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("authz/policy: writing the role %q of tenant %q: %w", r.Role, r.Tenant, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.afterWrite(ctx)
}

// MarkRoleBuiltin freezes an existing role against the management surface: it
// sets the builtin mark, after which this manager and the management surface
// refuse to rewrite or delete the role and to change its grants. SeedGrants is
// the one write that still adds grants to it. It is the only write that sets
// the mark; PutRole refuses a record carrying it, because a builtin role
// created that way would be frozen before the grants that give it meaning.
//
// The caller is the host writing the policy data it owns: the demo seed marks
// the administrator role once its grants are in place, and the mark keeps that
// role — which holds every declared permission — off the management surface's
// delete and rewrite paths. An already-builtin role is no work and no error;
// an absent one is ErrUnknownRole.
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

// DeleteRole removes a role and everything that names it: the role's rows in
// authz_grants and authz_bindings go with its row in authz_roles, in one
// transaction.
//
// Deleting the role row alone would not revoke anything: the engine loads its
// policy from authz_grants and authz_bindings and never reads authz_roles, so a
// leftover grant or binding would keep deciding for a role that no longer
// exists. The three rows are one unit — the deletion is the revocation. A
// builtin role is refused: its shape and its permissions are fixed. A role that
// is not there is no work and no error.
func (p *product) DeleteRole(ctx context.Context, tenant string, role authz.Role) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	removed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		builtin, found, err := roleState(ctx, tx, tenant, role)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if builtin {
			return fmt.Errorf("%w: the role %q of tenant %q is builtin", authz.ErrBuiltinRole, role, tenant)
		}
		if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
			Delete(&grantRow{}).Error; err != nil {
			return fmt.Errorf("authz/policy: deleting the grants of the role %q of tenant %q: %w", role, tenant, err)
		}
		if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
			Delete(&bindingRow{}).Error; err != nil {
			return fmt.Errorf("authz/policy: deleting the bindings of the role %q of tenant %q: %w", role, tenant, err)
		}
		if err := tx.Where("tenant_id = ? AND role = ?", tenant, string(role)).
			Delete(&roleRow{}).Error; err != nil {
			return fmt.Errorf("authz/policy: deleting the role %q of tenant %q: %w", role, tenant, err)
		}
		removed = true
		return nil
	})
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return p.afterWrite(ctx)
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
// idempotent: a grant already held is left as it stands and not counted. The
// caller is the host's seeding step; the management surface holds no Seeder and
// changes a role's grants through Manager only, which refuses a builtin role.
//
// The write is one transaction, and the role check takes the role's row lock, so
// a concurrent delete of the role and this seed serialise: the delete cannot
// remove the role between the check and the rows written against it. A grant is
// never removed, rewritten or re-timestamped here; the set only grows, which is
// what leaves a builtin role's existing grants fixed. An absent role is
// ErrUnknownRole, an empty tenant ErrTenantMismatch, and a seed that adds
// nothing writes nothing and publishes nothing.
func (p *product) SeedGrants(ctx context.Context, tenant string, role authz.Role, grants []authz.Grant) (int, error) {
	if err := requireTenant(tenant); err != nil {
		return 0, err
	}
	added := 0
	err := p.write(ctx, func(tx *gorm.DB) error {
		if _, found, err := roleState(ctx, tx, tenant, role); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, role, tenant)
		}
		held, err := grantsHeld(ctx, tx, tenant, role)
		if err != nil {
			return err
		}
		adapter := persist.New(ctx, tx, tenancy.ID(tenant))
		for _, g := range grants {
			if _, ok := held[g]; ok {
				continue
			}
			rule := []string{string(role), tenant, g.Resource, g.Action}
			if err := adapter.AddPolicy("p", "p", rule); err != nil {
				return fmt.Errorf("authz/policy: seeding the grants of tenant %q: %w", tenant, err)
			}
			held[g] = struct{}{}
			added++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if added == 0 {
		return 0, nil
	}
	return added, p.afterWrite(ctx)
}

// grantsHeld reports the (resource, action) pairs a role holds in a tenant,
// read through the write's own transaction. It reads the whole set rather than
// asking per grant so that a seed of a grown declared set issues one read and
// only the inserts the set lacks.
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
// role may be bound: that is how the platform operations' roles are handed to
// an account.
//
// An empty account is refused. A binding whose account is the empty string
// would decide for the zero subject: the decider puts the actor's empty key as
// the engine's subject, and a g line naming the empty account would match it.
func (p *product) PutBinding(ctx context.Context, b authz.Binding) error {
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
	if err := p.requirePlatformMember(ctx, b); err != nil {
		return err
	}
	rule := []string{b.Account, string(b.Role), b.Tenant}
	err := p.write(ctx, func(tx *gorm.DB) error {
		if _, found, err := roleState(ctx, tx, b.Tenant, b.Role); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("%w: the role %q of tenant %q was not created", authz.ErrUnknownRole, b.Role, b.Tenant)
		}
		if err := persist.New(ctx, tx, tenancy.ID(b.Tenant)).AddPolicy("g", "g", rule); err != nil {
			return fmt.Errorf("authz/policy: writing the binding of tenant %q: %w", b.Tenant, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return p.afterWrite(ctx)
}

// DeleteBinding removes a subject's binding to a role within a tenant.
//
// The last tenant administrator of a domain is not removable: the domain would
// be left with no one who may govern it. The check and the delete are one
// transaction, and the whole removal runs under the tenant lock
// authz.TenantLock names, which an explicit membership removal's guard takes as
// well. The lock is taken before the count and released after the commit, so two
// concurrent removals — of bindings, of memberships, or one of each — cannot each
// read enough bindings and then together leave the domain with none. A removal
// naming no such binding changes nothing and is not refused: it does not take the
// last one away.
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

// ClearAccountBindings removes every binding the account holds within the
// tenant in one statement. It is the host's account-deletion path, called while
// an account is being deleted, so the last-tenant-administrator rule that
// DeleteBinding enforces does not apply: the rule keeps a domain still in
// service governable, and the deleted account's own winding-up is not a removal
// from a domain it must leave governed. The write acts on one tenant and one
// account; the tenant's other bindings and the account's bindings in other
// tenants stay, and so do the roles and grants.
//
// The delete is one transaction and idempotent: an account with no binding in
// the tenant writes nothing and publishes nothing.
//
// The call takes no tenant lock. It is the account-deletion cleanup, and the
// membership-removal cascade uses the same statement; both can run while the
// removal holds the tenant lock, so a lock here would re-enter it and deadlock.
func (p *product) ClearAccountBindings(ctx context.Context, tenant, account string) error {
	if err := requireTenant(tenant); err != nil {
		return err
	}
	removed := false
	err := p.write(ctx, func(tx *gorm.DB) error {
		result := tx.Where("tenant_id = ? AND account_id = ?", tenant, account).Delete(&bindingRow{})
		if result.Error != nil {
			return fmt.Errorf("authz/policy: clearing the bindings of account %q in tenant %q: %w",
				account, tenant, result.Error)
		}
		removed = result.RowsAffected > 0
		return nil
	})
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}
	return p.afterWrite(ctx)
}

// refuseReservedRole refuses a role reserved to the platform domain in any other
// domain. PlatformAdminRole is the one such role; TenantAdminRole is every
// domain's own highest role and is not refused.
//
// The check runs before the role is looked up, so the refusal names the reserved
// role and not an unknown one: the role was never meant to exist in this domain.
func refuseReservedRole(tenant string, role authz.Role) error {
	if role != authz.PlatformAdminRole || tenant == string(authz.PlatformDomain) {
		return nil
	}
	return fmt.Errorf("%w: the role %q exists only in the platform domain %q, not in tenant %q",
		authz.ErrReservedRole, role, authz.PlatformDomain, tenant)
}

// requirePlatformMember checks that the account a platform administrator binding
// names is an active member of the platform tenant. It applies to that one
// binding and leaves every other binding alone.
//
// The check fails closed: an assembly with no MembershipChecker refuses the
// write rather than binding an account nothing can vouch for. A checker that
// answers "not a member" refuses the write too, and an error carrying out of the
// checker is the check being unavailable, not a verdict.
//
// It runs before the write's transaction. The checker is a host seam that reads
// the host's own tables, and a call made while this module held a transaction
// could wait for a connection the pool has already handed to that transaction.
// The membership it answers is checked a moment before the row is written; a
// membership revoked in between is the removal path's to revoke, not this
// write's to prevent.
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
// of a domain.
//
// The read takes the domain's tenant-administrator binding rows under an
// exclusive lock, so the count a removal acts on cannot be read by another
// removal before this transaction commits. The clause is inert where the dialect
// has no row locks (SQLite ignores it and its single writer already serialises
// the writes). A target row that is not there is no removal at all, whatever the
// count.
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

// tenantAdminRows reads the tenant-administrator bindings of a domain. With
// lock the rows are taken under an exclusive lock, for a caller that is inside
// the write transaction whose count must hold until it commits. Without it the
// read is a plain one, for a check that runs ahead of a write in another module.
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

// write runs fn inside one transaction on the cross-tenant-marked session.
//
// A write that checks a role and then writes a row guarded by that check does
// the two inside one transaction: as two independent statements a concurrent
// writer could commit between them — a grant landing on a role a concurrent
// delete removed, or on a role a concurrent write turned builtin — and leave a
// row the manager's own rules say must not exist. The transaction alone is not
// enough under READ COMMITTED, where each statement sees the last committed row:
// the check takes the role row's lock through roleState, so the delete and the
// guarded write serialise on that one row and the later of the two reads what the
// earlier one committed. The transaction is this module's own; a write shares no
// transaction with another module.
func (p *product) write(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return p.crossing(ctx).Transaction(fn)
}

// roleState reports whether a role exists in a tenant and whether it is
// builtin, read through the given statement session.
//
// The read is a Find over a slice, not a Take: a row that is not there is the
// ordinary answer to "does this role exist yet" and not a failed statement, and
// a Take would report it as gorm.ErrRecordNotFound, which the statement log
// writes at Error.
//
// The read takes the role row's exclusive lock (SELECT ... FOR UPDATE). It is
// run inside the write's own transaction, and the lock is held until that
// transaction ends, so it serialises the check against a concurrent write that
// touches the same role: a delete waits for the check to commit before it
// removes the role, and the check waits for a delete already in flight and then
// reads the role as gone. Without the lock the check is a plain read, and under
// READ COMMITTED a delete can commit in the window between the check and the row
// it guards — leaving a grant or a binding on a role that no longer exists, a
// row the engine decides with because it reads authz_grants and authz_bindings
// and never authz_roles. The clause is inert where the dialect has no row locks
// (SQLite ignores it and its single writer already serialises the writes).
func roleState(ctx context.Context, tx *gorm.DB, tenant string, role authz.Role) (builtin, found bool, err error) {
	var rows []roleRow
	err = tx.WithContext(tenancy.CrossTenant(ctx)).
		Where("tenant_id = ? AND role = ?", tenant, string(role)).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Limit(1).Find(&rows).Error
	if err != nil {
		return false, false, fmt.Errorf("authz/policy: reading the role %q of tenant %q: %w", role, tenant, err)
	}
	if len(rows) == 0 {
		return false, false, nil
	}
	return rows[0].Builtin, true, nil
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
// order, once the row is committed.
//
// The reload and the publish run on a context that is never cancelled: they are
// this module's bookkeeping for a row already in the table, and a caller that
// gave up at that point would otherwise leave a write this replica goes on not
// serving and no other replica hears about.
func (p *product) afterWrite(ctx context.Context) error {
	after := context.WithoutCancel(ctx)
	p.reload(after)
	p.publish(after)
	return nil
}
