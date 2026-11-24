package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

func checkTenant(tenant string) error {
	if tenant == "" || len(tenant) > tenantMax || strings.IndexFunc(tenant, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return invalid("the tenant is not an identifier of at most 255 bytes without blanks or control characters")
	}
	return nil
}

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

func (p *product) IsMember(ctx context.Context, id authn.AccountID, tenant string) (bool, error) {
	m, found, err := p.Membership(ctx, tenant, id)
	if err != nil || !found {
		return false, err
	}
	return m.Active, nil
}

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

func (p *product) MembershipTx(ctx context.Context, tx *gorm.DB, tenant string, id authn.AccountID) (authn.Membership, bool, error) {
	if id.IsStatic() || tenant == "" {
		return authn.Membership{}, false, nil
	}
	if _, err := p.lock(tx, id); err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return authn.Membership{}, false, nil
		}
		return authn.Membership{}, false, failed("reading a membership", err)
	}
	var rows []membershipRow
	err := tx.Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Limit(1).Find(&rows).Error
	if err != nil {
		return authn.Membership{}, false, failed("reading a membership", err)
	}
	if len(rows) == 0 {
		return authn.Membership{}, false, nil
	}
	return toMembership(rows[0]), true, nil
}

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

func (p *product) Members(ctx context.Context, tenant string, query authn.MemberQuery) ([]authn.Membership, error) {
	if tenant == "" {
		return nil, nil
	}
	tx := p.conn(ctx).Where("tenant_id = ?", tenant)
	if query.Query != "" {
		tx = p.filterMembersByQuery(tx, query.Query)
	}
	if query.After != "" {
		tx = tx.Where("account_id > ?", query.After)
	}
	var rows []membershipRow
	if err := tx.Order("account_id").Limit(query.EffectiveLimit()).Find(&rows).Error; err != nil {
		return nil, failed("listing the members of a tenant", err)
	}
	return toMemberships(rows), nil
}

// filterMembersByQuery narrows to accounts whose display name begins with text by
// its bytes, or whose stored identifier begins with the lowered text. A text that
// is not printable ASCII adds no identifier half.
func (p *product) filterMembersByQuery(tx *gorm.DB, text string) *gorm.DB {
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if bound, bounded := displayNameBound(text); bounded {
		conditions = append(conditions, "account_id IN (SELECT id FROM "+tableAccounts+" WHERE "+p.displayNameRange()+")")
		args = append(args, text, bound)
	} else {
		conditions = append(conditions, "account_id IN (SELECT id FROM "+tableAccounts+" WHERE "+p.displayNameLowerRange()+")")
		args = append(args, text)
	}
	lowered := strings.ToLower(text)
	if printableASCII(lowered) {
		conditions = append(conditions, "account_id IN (SELECT account_id FROM "+tableIdentifiers+" WHERE value >= ? AND value < ?)")
		args = append(args, lowered, prefixBound(lowered))
	}
	return tx.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func toMemberships(rows []membershipRow) []authn.Membership {
	out := make([]authn.Membership, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMembership(r))
	}
	return out
}

func (p *product) Put(ctx context.Context, m authn.Membership) error {
	status, err := checkMembership(m)
	if err != nil {
		return err
	}
	b := &batch{}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		_, inErr := p.putMembershipIn(ctx, tx, m, status, b)
		return inErr
	})
	if err != nil {
		return failed("writing a membership", err)
	}
	p.publish(ctx, b)
	return nil
}

func (p *product) PutMembershipTx(ctx context.Context, tx *gorm.DB, m authn.Membership) (func(context.Context) error, error) {
	status, err := checkMembership(m)
	if err != nil {
		return nil, err
	}
	b := &batch{}
	changed, err := p.putMembershipIn(ctx, tx, m, status, b)
	if err != nil {
		return nil, failed("writing a membership", err)
	}
	if !changed {
		return nil, nil
	}
	return func(afterCtx context.Context) error {
		p.publish(afterCtx, b)
		return nil
	}, nil
}

func (p *product) PutJoining(ctx context.Context, m authn.Membership, wasActive bool) (bool, error) {
	status, err := checkMembership(m)
	if err != nil {
		return false, err
	}
	if status != membershipActive {
		return false, invalid("a joining membership must be active")
	}
	b := &batch{}
	joined := false
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var inErr error
		joined, inErr = p.putJoiningIn(ctx, tx, m, status, wasActive, b)
		return inErr
	})
	if err != nil {
		return false, failed("writing a joining membership", err)
	}
	p.publish(ctx, b)
	return joined, nil
}

func (p *product) PutJoiningTx(ctx context.Context, tx *gorm.DB, m authn.Membership, wasActive bool) (bool, func(context.Context) error, error) {
	status, err := checkMembership(m)
	if err != nil {
		return false, nil, err
	}
	if status != membershipActive {
		return false, nil, invalid("a joining membership must be active")
	}
	b := &batch{}
	joined, err := p.putJoiningIn(ctx, tx, m, status, wasActive, b)
	if err != nil {
		return false, nil, failed("writing a joining membership", err)
	}
	if !joined {
		return false, nil, nil
	}
	return true, func(afterCtx context.Context) error {
		p.publish(afterCtx, b)
		return nil
	}, nil
}

func (p *product) putJoiningIn(ctx context.Context, tx *gorm.DB, m authn.Membership, status int16, wasActive bool, b *batch) (bool, error) {
	if _, err := p.lock(tx, m.Account); err != nil {
		return false, err
	}
	held, err := p.heldMembership(tx, m)
	if err != nil {
		return false, err
	}
	now := p.at()
	if len(held) == 0 {
		if wasActive {
			return false, nil
		}
		row := membershipRow{
			TenantID: m.Tenant, AccountID: m.Account.String(), Status: status, Source: m.Source,
			SSOExempt: m.SSOExempt, CreatedAt: now, UpdatedAt: now,
		}
		if err = tx.Create(&row).Error; err != nil {
			if duplicate(err) {
				return false, nil
			}
			return false, err
		}
		if err = p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeAdded, m.Source), true); err != nil {
			return false, err
		}
		if m.SSOExempt {
			return true, p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeSSOExemptSet, m.Source), true)
		}
		return true, nil
	}
	old := held[0]
	if old.Status != membershipActive {
		return false, nil
	}
	if !wasActive {
		res := tx.Model(&membershipRow{}).
			Where("tenant_id = ? AND account_id = ? AND status = ?", m.Tenant, m.Account.String(), membershipActive).
			Update("updated_at", now)
		if res.Error != nil {
			return false, res.Error
		}
		return res.RowsAffected == 1, nil
	}
	res := tx.Model(&membershipRow{}).
		Where("tenant_id = ? AND account_id = ? AND status = ?", m.Tenant, m.Account.String(), membershipActive).
		Updates(map[string]any{"status": status, "source": m.Source, "sso_exempt": m.SSOExempt, "updated_at": now})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected != 1 {
		return false, nil
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
			return false, err
		}
	}
	return true, nil
}

func checkMembership(m authn.Membership) (int16, error) {
	if err := checkTenant(m.Tenant); err != nil {
		return 0, err
	}
	if !plain(m.Source, sourceMax) {
		return 0, invalid("the source of a membership is text of at most 128 bytes")
	}
	if m.Account.IsStatic() {
		return 0, fmt.Errorf("%w: a subject of the static credential table has no membership", authn.ErrAccountNotFound)
	}
	if m.Active {
		return membershipActive, nil
	}
	return membershipSuspended, nil
}

func (p *product) heldMembership(tx *gorm.DB, m authn.Membership) ([]membershipRow, error) {
	var held []membershipRow
	err := tx.Where("tenant_id = ? AND account_id = ?", m.Tenant, m.Account.String()).Find(&held).Error
	return held, err
}

func (p *product) putMembershipIn(ctx context.Context, tx *gorm.DB, m authn.Membership, status int16, b *batch) (bool, error) {
	if _, err := p.lock(tx, m.Account); err != nil {
		return false, err
	}
	held, err := p.heldMembership(tx, m)
	if err != nil {
		return false, err
	}
	return p.writeMembershipIn(tx, m, status, b, held)
}

func (p *product) writeMembershipIn(tx *gorm.DB, m authn.Membership, status int16, b *batch, held []membershipRow) (bool, error) {
	now := p.at()
	if len(held) == 0 {
		row := membershipRow{
			TenantID: m.Tenant, AccountID: m.Account.String(), Status: status, Source: m.Source,
			SSOExempt: m.SSOExempt, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return false, err
		}
		if err := p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeAdded, m.Source), true); err != nil {
			return false, err
		}
		if m.SSOExempt {
			return true, p.emit(tx, b, membershipChanged(m.Account, m.Tenant, changeSSOExemptSet, m.Source), true)
		}
		return true, nil
	}
	old := held[0]
	if old.Status == status && old.Source == m.Source && old.SSOExempt == m.SSOExempt {
		return false, nil
	}
	err := tx.Model(&membershipRow{}).Where("tenant_id = ? AND account_id = ?", m.Tenant, m.Account.String()).
		Updates(map[string]any{"status": status, "source": m.Source, "sso_exempt": m.SSOExempt, "updated_at": now}).Error
	if err != nil {
		return false, err
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
			return false, err
		}
	}
	return true, nil
}

func (p *product) Remove(ctx context.Context, id authn.AccountID, tenant string) error {
	if id.IsStatic() || tenant == "" {
		return nil
	}
	release, err := p.lockMembershipRemoval(ctx, tenant)
	if err != nil {
		return failed("taking the tenant's membership-removal lock", err)
	}
	defer release()
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
	release()
	return p.callMembershipHooks(ctx, id, tenant)
}

func (p *product) lockMembershipRemoval(ctx context.Context, tenant string) (func(), error) {
	releases := make([]func(), 0, len(p.membershipGuards))
	shared := make(map[any]struct{}, len(p.membershipGuards))
	var once sync.Once
	releaseAll := func() {
		once.Do(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		})
	}
	for _, guard := range p.membershipGuards {
		if holder, ok := guard.(authn.SharedMembershipLock); ok {
			if object := holder.MembershipLock(); object != nil {
				if _, taken := shared[object]; taken {
					continue
				}
				shared[object] = struct{}{}
			}
		}
		release, err := guard.Lock(ctx, tenant)
		if err != nil {
			releaseAll()
			return nil, err
		}
		if release != nil {
			releases = append(releases, release)
		}
	}
	return releaseAll, nil
}

func (p *product) RemoveMembershipForDeletion(ctx context.Context, id authn.AccountID, tenant string) error {
	if id.IsStatic() || tenant == "" {
		return nil
	}
	return p.removeMembership(ctx, id, tenant)
}

func (p *product) removeMembership(ctx context.Context, id authn.AccountID, tenant string) error {
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := p.removeMembershipIn(ctx, tx, id, tenant, b)
		return err
	})
	if err != nil {
		return failed("removing a membership", err)
	}
	p.publish(ctx, b)
	return nil
}

func (p *product) RemoveMembershipTx(ctx context.Context, tx *gorm.DB, id authn.AccountID, tenant string) (func(context.Context) error, error) {
	if id.IsStatic() || tenant == "" {
		return nil, nil
	}
	b := &batch{}
	removed, err := p.removeMembershipIn(ctx, tx, id, tenant, b)
	if err != nil {
		return nil, failed("removing a membership", err)
	}
	if !removed {
		return nil, nil
	}
	return func(afterCtx context.Context) error {
		p.publish(afterCtx, b)
		return nil
	}, nil
}

func (p *product) RemoveTenantMembershipsTx(ctx context.Context, tx *gorm.DB, tenant string) (func(context.Context) error, error) {
	if tenant == "" {
		return nil, nil
	}
	var held []membershipRow
	if err := tx.Where("tenant_id = ?", tenant).Order("account_id").Find(&held).Error; err != nil {
		return nil, failed("reading a tenant's memberships", err)
	}
	if len(held) == 0 {
		return nil, nil
	}
	if err := tx.Where("tenant_id = ?", tenant).Delete(&membershipRow{}).Error; err != nil {
		return nil, failed("removing a tenant's memberships", err)
	}
	b := &batch{}
	for _, m := range held {
		if err := p.emit(tx, b, membershipChanged(authn.AccountID(m.AccountID), tenant, changeRemoved, m.Source), true); err != nil {
			return nil, failed("staging a membership removal", err)
		}
	}
	return func(afterCtx context.Context) error {
		p.publish(afterCtx, b)
		return nil
	}, nil
}

func (p *product) removeMembershipIn(_ context.Context, tx *gorm.DB, id authn.AccountID, tenant string, b *batch) (bool, error) {
	var held membershipRow
	err := tx.Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Take(&held).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	res := tx.Where("tenant_id = ? AND account_id = ?", tenant, id.String()).Delete(&membershipRow{})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	if err := p.emit(tx, b, membershipChanged(id, tenant, changeRemoved, held.Source), true); err != nil {
		return false, err
	}
	return true, nil
}

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

func (p *product) checkMembershipRemoval(ctx context.Context, id authn.AccountID, tenant string) error {
	for _, guard := range p.membershipGuards {
		if err := guard.CheckMembershipRemoval(ctx, id, tenant); err != nil {
			return failed("checking a membership removal", err)
		}
	}
	return nil
}
