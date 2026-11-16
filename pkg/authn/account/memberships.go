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

// MembershipTx reads one membership in the transaction it is handed, taking the
// account's row first. That row is what Put also takes before it writes a
// membership, so a read under it and a concurrent suspension run one after the
// other: the caller either sees the suspension's committed result or has its own
// commit land before the suspension writes. A missing account reads as no
// membership, as one the account does not hold does.
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
// last membership of the page before. A query narrows the page to the accounts
// whose display name or login identifiers begin with it.
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

// filterMembersByQuery narrows a membership statement to the accounts whose
// display name or login identifiers begin with text. The display name is
// compared as written, by its bytes, the same range the account list's
// display-name prefix reads. An identifier is compared after the text is
// lower-cased, because usernames and email addresses are stored that way; a
// text that is not printable ASCII begins no stored identifier and skips that
// half rather than closing a range on a byte no identifier holds.
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

// Put writes a membership, and writing the same one again is no change.
//
// Adding, suspending and resuming are one call with the Membership's values, and
// every change it makes to a membership that was there is an event of its own.
// The account has to exist and not be a tombstone, since no foreign key holds a
// membership to one; the account's row is taken first, which is what keeps a
// membership from being written after the deletion that erased them.
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

// PutMembershipTx writes a membership in the transaction it is handed, with
// Put's rules. The after closure publishes the events staged in that
// transaction; the caller runs it once the transaction has committed. A
// membership already carrying the same values returns a nil closure.
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

// PutJoining writes a membership for an account joining a tenant, conditional
// on the membership not having been withdrawn since the caller read it.
//
// wasActive says the caller read an active membership. When it is false the
// call writes only into a tenant the account has no membership in; when it is
// true it writes only over a membership that is still active. A suspended
// membership is never resumed, and a membership the caller read as active but
// that has been removed is not written again: both answer false and change
// nothing. The row and the account are taken in one transaction, so a
// concurrent suspension either is seen by the check or waits for this write.
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

// PutJoiningTx writes a joining membership in the transaction it is handed,
// with PutJoining's rules. The after closure publishes the events staged in that
// transaction; the caller runs it once the transaction has committed. A call
// that changed nothing returns a nil closure.
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

// putJoiningIn writes a joining membership inside the transaction it is handed
// and reports whether the account is an active member after the call.
//
// The write is conditional on the stored state, not only on the read: a
// removal takes no account row lock, so the row the read found can be gone by
// the time the update runs. The update names `status = active` and its affected
// row count decides; zero means the removal or suspension won the race and the
// join did not happen.
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
		// A membership the caller read as active is gone: the removal won the
		// race, and a join must not write the row again.
		if wasActive {
			return false, nil
		}
		row := membershipRow{
			TenantID: m.Tenant, AccountID: m.Account.String(), Status: status, Source: m.Source,
			SSOExempt: m.SSOExempt, CreatedAt: now, UpdatedAt: now,
		}
		if err = tx.Create(&row).Error; err != nil {
			// Another path joined the account between the read and the insert:
			// the join already holds, and this call wrote nothing.
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
		// An active membership appeared while the caller read none. Confirm it
		// still exists without rewriting the source or exemption it carries.
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
	// Zero means the removal or suspension committed after the read, so the
	// membership the caller saw is not the one this call would leave behind.
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

// checkMembership judges a membership write's arguments and answers the stored
// status the Membership's Active maps to.
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

// heldMembership reads the account's membership of the tenant inside tx.
func (p *product) heldMembership(tx *gorm.DB, m authn.Membership) ([]membershipRow, error) {
	var held []membershipRow
	err := tx.Where("tenant_id = ? AND account_id = ?", m.Tenant, m.Account.String()).Find(&held).Error
	return held, err
}

// putMembershipIn writes a membership inside the transaction it is handed. It
// reports whether the call changed anything.
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

// writeMembershipIn writes m over the membership rows held, which the caller
// read under the account's row lock. It reports whether the call changed
// anything.
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
//
// Every guard's tenant lock is taken before the row is read and the checks are
// asked, and held across the removal's commit: two removals that both passed a
// check on the same tenant cannot then both commit and leave the tenant with no
// administrator. A lock that cannot be taken is ErrUnavailable and the removal
// does not happen — it is never read as permission to proceed.
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
	// The commit is behind us; the hooks run outside the serialised section so
	// that a slow cleanup does not hold the tenant's removals up. The guard
	// locks no longer protect anything once the row is gone.
	release()
	return p.callMembershipHooks(ctx, id, tenant)
}

// lockMembershipRemoval takes every optional guard's lock for the tenant and
// answers one function that releases them all. A guard's lock joins the ones
// already taken so that a later failure releases what it did take; a lock that
// cannot be taken is the call's error, and none of the guards is asked its
// check.
//
// Guards wired to one lock object report it through authn.SharedMembershipLock,
// and the object is taken once: a non-reentrant lock taken a second time by the
// same removal would wait on itself until its context ended. Guards whose locks
// are their own are taken as they are asked.
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

// RemoveMembershipForDeletion deletes one membership on the account-deletion
// path. It asks no guard and calls no membership lifecycle hook: the deletion's
// own cleanup is the winding-up, and a guard keeps a domain that stays in service
// governable, not one whose owner is being erased. The row it removes still
// publishes authn.membership.changed, so an event subscriber cleans it like a
// row removed explicitly. One that is not there is already removed, and the call
// is nil.
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
		_, err := p.removeMembershipIn(ctx, tx, id, tenant, b)
		return err
	})
	if err != nil {
		return failed("removing a membership", err)
	}
	p.publish(ctx, b)
	return nil
}

// RemoveMembershipTx removes a membership in the transaction it is handed, with
// RemoveMembershipForDeletion's rules: it asks no guard and calls no lifecycle
// hook. The after closure publishes the removal staged in that transaction; the
// caller runs it once the transaction has committed. A row that is not there
// returns a nil closure.
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

// RemoveTenantMembershipsTx deletes every membership of one tenant in the
// transaction it is handed, with RemoveMembershipForDeletion's rules: no guard
// is asked and no lifecycle hook runs, and each removed row publishes
// authn.membership.changed so an event subscriber cleans what it holds. The
// after closure publishes the removal staged in that transaction; a tenant with
// no membership returns a nil closure.
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

// removeMembershipIn deletes one membership row inside the transaction it is
// handed and stages its event in b. It reports whether it removed anything.
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
