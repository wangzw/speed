package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// checkTenant refuses a tenant that cannot name one: empty, too long, or holding
// a blank or a control character, which is the domain of a tenant identifier.
func checkTenant(tenant string) error {
	if tenant == "" || len(tenant) > tenantMax || strings.IndexFunc(tenant, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return invalid("the tenant is not an identifier of at most 255 bytes without blanks or control characters")
	}
	return nil
}

// toMembership is the membership a row stands for.
func toMembership(r membershipRow) authn.Membership {
	return authn.Membership{
		Account:   authn.AccountID(r.AccountID),
		Tenant:    r.TenantID,
		Active:    r.Status == membershipActive,
		Source:    r.Source,
		SSOExempt: r.SSOExempt,
		CreatedAt: utc(r.CreatedAt),
	}
}

// IsMember reports whether the account is an active member of the tenant. One
// that is suspended, one that is not a member and a subject of the static
// credential table are all not.
func (p *product) IsMember(ctx context.Context, id authn.AccountID, tenant string) (bool, error) {
	m, found, err := p.Membership(ctx, tenant, id)
	if err != nil || !found {
		return false, err
	}
	return m.Active, nil
}

// Membership reads one membership. The second result is false when there is none,
// and a suspended membership is found with Active false.
func (p *product) Membership(ctx context.Context, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	if id.IsStatic() || tenant == "" {
		return authn.Membership{}, false, nil
	}
	var rows []membershipRow
	err := p.conn(ctx).Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Limit(1).Find(&rows).Error
	if err != nil {
		return authn.Membership{}, false, failed("reading a membership", err)
	}
	if len(rows) == 0 {
		return authn.Membership{}, false, nil
	}
	return toMembership(rows[0]), true, nil
}

// Tenants lists every membership of an account, across tenants, by tenant.
func (p *product) Tenants(ctx context.Context, id authn.AccountID) ([]authn.Membership, error) {
	if id.IsStatic() {
		return nil, nil
	}
	var rows []membershipRow
	if err := p.conn(ctx).Where("account_id = ?", id.String()).Order("tenant_id").Find(&rows).Error; err != nil {
		return nil, failed("listing the memberships of an account", err)
	}
	return toMemberships(rows), nil
}

// Members lists the memberships of a tenant, one page at a time and in the order
// of the account identifiers. The page's After is the account identifier of the
// last membership of the page before.
func (p *product) Members(ctx context.Context, tenant string, page authn.Page) ([]authn.Membership, error) {
	if tenant == "" {
		return nil, nil
	}
	tx := p.conn(ctx).Where("tenant_id = ?", tenant)
	if page.After != "" {
		tx = tx.Where("account_id > ?", page.After)
	}
	var rows []membershipRow
	if err := tx.Order("account_id").Limit(page.EffectiveLimit()).Find(&rows).Error; err != nil {
		return nil, failed("listing the members of a tenant", err)
	}
	return toMemberships(rows), nil
}

func toMemberships(rows []membershipRow) []authn.Membership {
	out := make([]authn.Membership, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMembership(r))
	}
	return out
}

// Put writes a membership, and writing the same one again is no change.
//
// Adding, suspending and resuming are one call with the Membership's values, and
// every change it makes to a membership that was there is an event of its own.
// The account has to exist and not be a tombstone, since no foreign key holds a
// membership to one; the account's row is taken first, which is what keeps a
// membership from being written after the deletion that erased them.
func (p *product) Put(ctx context.Context, m authn.Membership) error {
	if err := checkTenant(m.Tenant); err != nil {
		return err
	}
	if !plain(m.Source, sourceMax) {
		return invalid("the source of a membership is text of at most 128 bytes")
	}
	if m.Account.IsStatic() {
		return fmt.Errorf("%w: a subject of the static credential table has no membership", authn.ErrAccountNotFound)
	}
	status := membershipSuspended
	if m.Active {
		status = membershipActive
	}
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := p.lock(tx, m.Account); err != nil {
			return err
		}
		now := p.at()
		var held []membershipRow
		err := tx.Where("tenant_id = ? AND account_id = ?", m.Tenant, m.Account.String()).Find(&held).Error
		if err != nil {
			return err
		}
		if len(held) == 0 {
			row := membershipRow{
				TenantID: m.Tenant, AccountID: m.Account.String(), Status: status, Source: m.Source,
				SSOExempt: m.SSOExempt, CreatedAt: now, UpdatedAt: now,
			}
			if err = tx.Create(&row).Error; err != nil {
				return err
			}
			if err = p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeAdded, m.Source), true); err != nil {
				return err
			}
			if m.SSOExempt {
				return p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeSSOExemptSet, m.Source), true)
			}
			return nil
		}
		old := held[0]
		if old.Status == status && old.Source == m.Source && old.SSOExempt == m.SSOExempt {
			return nil
		}
		err = tx.Model(&membershipRow{}).Where("tenant_id = ? AND account_id = ?", m.Tenant, m.Account.String()).
			Updates(map[string]any{"status": status, "source": m.Source, "sso_exempt": m.SSOExempt, "updated_at": now}).Error
		if err != nil {
			return err
		}
		var changes []string
		switch {
		case old.Status != status && status == membershipActive:
			changes = append(changes, changeResumed)
		case old.Status != status:
			changes = append(changes, changeSuspended)
		}
		switch {
		case !old.SSOExempt && m.SSOExempt:
			changes = append(changes, changeSSOExemptSet)
		case old.SSOExempt && !m.SSOExempt:
			changes = append(changes, changeSSOExemptCleared)
		}
		for _, change := range changes {
			if err = p.emit(tx, b, membershipChanged(m.Account, m.Tenant, change, m.Source), true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return failed("writing a membership", err)
	}
	p.publish(ctx, b)
	return nil
}

// Remove deletes one membership. One that is not there is already removed, and the
// call is nil.
//
// An explicit removal is asked of every MembershipGuard first: one that would
// leave the tenant with no tenant-administrator binding is refused with
// authn.ErrLastTenantAdmin rather than carried out, and a host guard may add
// refusals of its own. The question is put only when there is a row to remove,
// because a removal that takes nothing away cannot leave a tenant less governed
// and the retry of a removal whose cleanup failed must stay a success. Account
// deletion does not come through here and does not consult the guards.
//
// After the transaction commits the membership lifecycle hooks are called, for
// the row just removed and for a retry whose row is already gone. A failing hook
// makes the call authn.ErrCleanupIncomplete with the removal in force; calling
// again finishes the cleanup.
func (p *product) Remove(ctx context.Context, id authn.AccountID, tenant string) error {
	if id.IsStatic() || tenant == "" {
		return nil
	}
	held, readErr := p.membershipExists(ctx, id, tenant)
	if readErr != nil {
		return failed("reading a membership", readErr)
	}
	if held {
		if err := p.checkMembershipRemoval(ctx, id, tenant); err != nil {
			return err
		}
	}
	if err := p.removeMembership(ctx, id, tenant); err != nil {
		return err
	}
	return p.callMembershipHooks(ctx, id, tenant)
}

// RemoveMembershipForDeletion deletes one membership on the account-deletion
// path. It asks no guard and calls no membership lifecycle hook: the deletion's
// own cleanup is the winding-up, and a guard keeps a domain that stays in service
// governable, not one whose owner is being erased. One that is not there is
// already removed, and the call is nil.
func (p *product) RemoveMembershipForDeletion(ctx context.Context, id authn.AccountID, tenant string) error {
	if id.IsStatic() || tenant == "" {
		return nil
	}
	return p.removeMembership(ctx, id, tenant)
}

// removeMembership deletes one membership row and publishes the change. A row
// that is not there is already removed, and the call is nil. The caller decides
// whether the guards and the membership lifecycle hooks apply.
func (p *product) removeMembership(ctx context.Context, id authn.AccountID, tenant string) error {
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var held membershipRow
		err := tx.Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Take(&held).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		res := tx.Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Delete(&membershipRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return p.emit(tx, b, membershipChanged(id, tenant, changeRemoved, held.Source), true)
	})
	if err != nil {
		return failed("removing a membership", err)
	}
	p.publish(ctx, b)
	return nil
}

// membershipExists reports whether the account holds a membership row in the
// tenant, so that the guard is asked only about a removal that takes something
// away.
func (p *product) membershipExists(ctx context.Context, id authn.AccountID, tenant string) (bool, error) {
	var n int64
	err := p.conn(ctx).Model(&membershipRow{}).
		Where("tenant_id = ? AND account_id = ?", tenant, id.String()).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// checkMembershipRemoval asks every optional guard whether the explicit removal
// may proceed. The guards stand side by side and any one of them may refuse; a
// guard that cannot answer is ErrUnavailable, never a pass.
func (p *product) checkMembershipRemoval(ctx context.Context, id authn.AccountID, tenant string) error {
	for _, guard := range p.membershipGuards {
		if err := guard.CheckMembershipRemoval(ctx, id, tenant); err != nil {
			return failed("checking a membership removal", err)
		}
	}
	return nil
}
