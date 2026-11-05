package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
)

// hookTimeout bounds the cleanup hooks of one call, which run after the state
// they clean up after is already in force.
const hookTimeout = 30 * time.Second

// lock takes the row of an account for the rest of the transaction by writing it,
// and reads it. Two transactions that start with it on one account run one after
// the other on both engines, which is what makes a read of the account's other
// rows in the transaction a read nobody is changing under it. An account that does
// not exist, and a tombstone, are authn.ErrAccountNotFound.
func (p *product) lock(tx *gorm.DB, id authn.AccountID) (accountRow, error) {
	if err := checkID(id); err != nil {
		return accountRow{}, err
	}
	res := tx.Exec("UPDATE "+tableAccounts+" SET updated_at = ? WHERE id = ?", p.at(), id.String())
	if res.Error != nil {
		return accountRow{}, res.Error
	}
	if res.RowsAffected == 0 {
		return accountRow{}, fmt.Errorf("%w", authn.ErrAccountNotFound)
	}
	return loadAccount(tx, id)
}

// ceilSecond is the instant a validity is moved to: the next whole second after
// the time. A session made in the rest of the second the change happened in is
// void as well.
func ceilSecond(t time.Time) time.Time { return t.Truncate(time.Second).Add(time.Second) }

// reasonOK reports whether the reason is one of the classes a payload may carry.
func reasonOK(reason string) error {
	if !authn.ValidPayloadReasonClass(reason) {
		return fmt.Errorf("%w: the reason is not one of the reason classes", authn.ErrInvalidArgument)
	}
	return nil
}

// SetStatus moves an account between disabled and enabled.
//
// The account's own tables change in one transaction: the status, the instant
// before which no session is valid, and the event. Moving to disabled moves that
// instant to the next second after the change and never back, and moving to
// enabled leaves it, so a session of before is not revived. Once the transaction
// has committed the hook of the target status is called on every lifecycle
// implementation, each of them idempotent, and a hook that fails makes the call
// authn.ErrCleanupIncomplete with the new state in force. Calling again with the
// same status finishes the cleanup: a status the account already has changes
// nothing and the hook of that status is called once more.
func (p *product) SetStatus(ctx context.Context, id authn.AccountID, s authn.AccountStatus, reason string) error {
	if s != authn.StatusDisabled && s != authn.StatusActive {
		return fmt.Errorf("%w: a status is set to disabled or enabled, and an account is ended with Delete", authn.ErrInvalidArgument)
	}
	if err := reasonOK(reason); err != nil {
		return err
	}
	if s == authn.StatusDisabled {
		if err := p.refuseSuspendingTheOwner(ctx, id); err != nil {
			return err
		}
	}
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := p.lock(tx, id)
		if err != nil {
			return err
		}
		from := statusOf(row.Status)
		if from == s {
			return nil
		}
		now := p.at()
		update := map[string]any{"status": int16(s), "updated_at": now}
		if s == authn.StatusDisabled {
			update["disabled_at"] = now
			update["valid_after"] = laterOf(utc(row.ValidAfter), ceilSecond(now))
		} else {
			update["disabled_at"] = nil
		}
		if err = tx.Model(&accountRow{}).Where("id = ?", id.String()).Updates(update).Error; err != nil {
			return err
		}
		return p.emit(tx, b, statusChanged(id, from, s, reason), true)
	})
	if err != nil {
		return failed("changing an account's status", err)
	}
	p.publish(ctx, b)
	if s == authn.StatusDisabled {
		return p.callHooks(ctx, id, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
			return h.AccountDisabled(hookCtx, id)
		})
	}
	return p.callHooks(ctx, id, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.AccountEnabled(hookCtx, id)
	})
}

// laterOf is the later of two times.
func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Delete ends an account for good.
//
// The row stays as a tombstone that holds the identifier, the kind, the status,
// the time of creation and the time of deletion, and every identifier, pending
// contact, external identity, credential, secret and membership of the account
// is deleted in the same transaction, together with the personal data of the row
// itself. After the erase and before the transaction returns, the required
// provisioning seam is called with the facts the erase has just taken away, so
// what the account owned is ended in the same transaction. The
// instant before which no session is valid moves on, as a disabling moves it.
// Calling again on a tombstone finishes the cleanup the hooks did not.
func (p *product) Delete(ctx context.Context, id authn.AccountID, reason string) error {
	if err := reasonOK(reason); err != nil {
		return err
	}
	if err := checkID(id); err != nil {
		return err
	}
	// The locks are taken before the check and held through the cleanup, so
	// this deletion and any other removal that judges one of the same tenants'
	// floors run one after the other (see refuseDeletingTheLastTenantAdmin).
	release, err := p.lockAccountAdministration(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	// The owner check precedes the last-administrator floor: an owner is
	// usually the organization's only administrator too, and the answer that
	// asks for the ownership to be handed on is the one that names the actual
	// obstacle. A non-owner sole administrator still meets the floor below.
	if err = p.refuseDeletingTheOwner(ctx, id); err != nil {
		return err
	}
	if err = p.refuseDeletingTheLastTenantAdmin(ctx, id); err != nil {
		return err
	}
	b := &batch{}
	ctx, after := provisioning.WithAfterCommit(ctx)
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE "+tableAccounts+" SET updated_at = ? WHERE id = ?", p.at(), id.String())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("%w", authn.ErrAccountNotFound)
		}
		var row accountRow
		if res = tx.Where("id = ?", id.String()).Take(&row); res.Error != nil {
			return res.Error
		}
		if row.Status == statusDeleted {
			return nil
		}
		// The facts are read before the erase: the username identifier and the
		// display name are what the deletion seam still needs, and eraseAccount
		// takes both away in this transaction.
		facts, factsErr := deletionFacts(tx, row)
		if factsErr != nil {
			return factsErr
		}
		if err = p.eraseAccount(tx, row, b); err != nil {
			return err
		}
		// What the account owns is ended in the same transaction. A failure
		// here rolls the tombstone and the erase back with it.
		return p.provisioning.ProvisionAccountDeletion(ctx, tx, facts)
	})
	if err != nil {
		return failed("deleting an account", err)
	}
	p.publish(ctx, b)
	p.runAfterCommit(ctx, after)
	return p.callHooks(ctx, id, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.AccountDeleted(hookCtx, id)
	})
}

// lockAccountAdministration takes the counter's tenant locks for every tenant
// in which the account administers, and answers the release. An assembly with
// no counter locks nothing: its deletion path has no floor to serialise. A lock
// that cannot be taken is ErrUnavailable and the deletion does not run, because
// an unknown floor must not be read as permission.
func (p *product) lockAccountAdministration(ctx context.Context, id authn.AccountID) (func(), error) {
	counter := p.tenantAdminCounter
	if counter == nil {
		return func() {}, nil
	}
	release, err := counter.LockAccountAdministration(ctx, id.String())
	if err != nil {
		p.logger.Error(ModuleName+": the last-administrator locks could not be taken, so the account was not deleted",
			slog.String("account", id.String()), slog.String("cause", err.Error()))
		return nil, fmt.Errorf("%w: taking the last tenant-administrator locks: %w", authn.ErrUnavailable, err)
	}
	return release, nil
}

// refuseDeletingTheLastTenantAdmin asks the optional counter before the
// tombstone is written. An account that is the only tenant administrator of an
// active business tenant is refused with authn.ErrAccountLastTenantAdmin and
// nothing is erased; the role has to be handed to another administrator first.
// The refusal carries the tenant's identifier, so a surface can name the
// organization the operator has to hand over.
//
// The check runs after lockAccountAdministration has taken the tenant locks the
// deletion shares with every other path that judges those tenants' floors, and
// the deletion's transaction and binding cleanup run before the locks are
// released. Two deletions in one tenant therefore cannot each read the other
// administrator as present and then together leave the tenant with none: the
// second either sees the first's committed cleanup (and is refused) or runs
// first. The same lock serialises a deletion against a binding or membership
// removal of one of those tenants.
//
// A tombstone is not checked: the call is finishing a cleanup whose hooks did
// not complete, and the live account the floor protects is already gone. An
// account that is not there is left to the transaction's own not-found
// refusal, so an assembly with the seam keeps the answer it gave before.
//
// A counter that cannot answer is ErrUnavailable and the deletion does not
// run: an unknown floor must not be read as permission. An assembly that
// supplies no counter skips the whole check, which is the deletion behaviour a
// host without a policy module has.
func (p *product) refuseDeletingTheLastTenantAdmin(ctx context.Context, id authn.AccountID) error {
	counter := p.tenantAdminCounter
	if counter == nil {
		return nil
	}
	var row accountRow
	switch err := p.conn(ctx).Where("id = ?", id.String()).Take(&row).Error; {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return failed("reading an account before deleting it", err)
	}
	if row.Status == statusDeleted {
		return nil
	}
	tenantID, sole, err := counter.SoleTenantAdminTenant(ctx, id.String())
	if err != nil {
		p.logger.Error(ModuleName+": the last-administrator check could not be read, so the account was not deleted",
			slog.String("account", id.String()), slog.String("cause", err.Error()))
		return fmt.Errorf("%w: reading the last tenant administrator: %w", authn.ErrUnavailable, err)
	}
	if sole {
		return &authn.AccountLastTenantAdminError{Tenant: tenantID}
	}
	return nil
}

// refuseDeletingTheOwner refuses an account deletion when the account owns an
// active business organization. The organization has to be handed on or deleted
// first; a personal organization does not count, because it is ended with the
// account.
func (p *product) refuseDeletingTheOwner(ctx context.Context, id authn.AccountID) error {
	return p.refuseAccountOwningAnOrganization(ctx, id, "deleting")
}

// refuseSuspendingTheOwner refuses a suspension of an account that owns an
// active business organization. A suspended owner would leave the organization
// with nobody who can hand it on, exactly as a deletion would.
func (p *product) refuseSuspendingTheOwner(ctx context.Context, id authn.AccountID) error {
	return p.refuseAccountOwningAnOrganization(ctx, id, "suspending")
}

// refuseAccountOwningAnOrganization asks the optional owner counter. The
// refusal carries the tenant's identifier so a surface can name the
// organization the operator has to hand on or delete. A tombstone is not
// checked: the live account the floor protects is already gone. A counter that
// cannot answer is ErrUnavailable and the write does not run: an unknown owner
// must not be read as permission. An assembly that supplies no counter skips
// the check.
func (p *product) refuseAccountOwningAnOrganization(ctx context.Context, id authn.AccountID, op string) error {
	counter := p.tenantOwnerCounter
	if counter == nil {
		return nil
	}
	var row accountRow
	switch err := p.conn(ctx).Where("id = ?", id.String()).Take(&row).Error; {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return failed("reading an account before "+op+" it", err)
	}
	if row.Status == statusDeleted {
		return nil
	}
	tenantID, owns, err := counter.OwnsActiveBusinessTenant(ctx, id.String())
	if err != nil {
		p.logger.Error(ModuleName+": the organization-owner check could not be read, so the account was not changed",
			slog.String("account", id.String()), slog.String("cause", err.Error()))
		return fmt.Errorf("%w: reading the organization owner: %w", authn.ErrUnavailable, err)
	}
	if owns {
		return &authn.AccountOwnsOrganizationError{Tenant: tenantID}
	}
	return nil
}

// eraseAccount writes the tombstone of a live account in tx and deletes what
// this module holds of it: every identifier, pending contact, external identity,
// credential, secret and membership, and the personal columns of the row. The
// events are kept in b, and the caller publishes them once the transaction has
// committed. The caller has locked the row and hands in the account it read, so
// the memberships read here cannot change under the erase.
func (p *product) eraseAccount(tx *gorm.DB, row accountRow, b *batch) error {
	id := authn.AccountID(row.ID)
	now := p.at()
	var memberships []membershipRow
	if err := tx.Where("account_id = ?", id.String()).Order("tenant_id").Find(&memberships).Error; err != nil {
		return err
	}
	erase := map[string]any{
		"status":             statusDeleted,
		"signup_state":       signupNone,
		"signup_verified_at": nil,
		"display_name":       "",
		"attributes":         "{}",
		"mfa_required":       false,
		"protected":          false,
		"valid_after":        laterOf(utc(row.ValidAfter), ceilSecond(now)),
		"updated_at":         now,
		"last_login_at":      nil,
		"disabled_at":        nil,
		"deleted_at":         now,
	}
	if err := tx.Model(&accountRow{}).Where("id = ?", id.String()).Updates(erase).Error; err != nil {
		return err
	}
	for _, table := range []any{&identifierRow{}, &pendingContactRow{}, &externalRow{}, &secretRow{}, &membershipRow{}} {
		if err := tx.Where("account_id = ?", id.String()).Delete(table).Error; err != nil {
			return err
		}
	}
	if err := tx.Exec("DELETE FROM "+tableCredentials+" WHERE account_id = ?", id.String()).Error; err != nil {
		return err
	}
	if err := p.emit(tx, b, accountDeleted(id), true); err != nil {
		return err
	}
	for _, m := range memberships {
		if err := p.emit(tx, b, membershipChanged(id, m.TenantID, changeRemoved, m.Source), true); err != nil {
			return err
		}
	}
	return nil
}

// callHooks calls one method of every lifecycle hook once the state it cleans up
// after is in force. Every hook is called whatever the ones before it did, and the
// failures are counted by hook and returned together as authn.ErrCleanupIncomplete.
// The hooks run to the end whatever becomes of the caller's context, within a
// bound: the state is in force, and the cleanup is all that is left. name, when
// not nil, is what a failure is counted under in place of the hook's own name.
func (p *product) callHooks(ctx context.Context, id authn.AccountID, name *string,
	call func(context.Context, authn.AccountLifecycle) error,
) error {
	if len(p.hooks) == 0 {
		return nil
	}
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hookTimeout)
	defer cancel()
	var failures []error
	for _, h := range p.hooks {
		if err := call(hookCtx, h); err != nil {
			hook := hookName(h)
			if name != nil {
				hook = *name
			}
			p.meters.cleanupFailed(ctx, hook)
			p.logger.Warn(ModuleName+": a lifecycle hook failed after the state changed, and repeating the call "+
				"finishes the cleanup",
				slog.String("account", id.String()), slog.String("hook", hook), slog.String("cause", describe(err)))
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", authn.ErrCleanupIncomplete, errors.Join(failures...))
}

// callMembershipHooks calls one method of every membership lifecycle hook once
// the removal it cleans up after is in force. Every hook is called whatever the
// ones before it did, and the failures are returned together as
// authn.ErrCleanupIncomplete. The hooks run to the end whatever becomes of the
// caller's context, within the same bound the account hooks take.
func (p *product) callMembershipHooks(ctx context.Context, id authn.AccountID, tenant string) error {
	if len(p.membershipHooks) == 0 {
		return nil
	}
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hookTimeout)
	defer cancel()
	var failures []error
	for _, h := range p.membershipHooks {
		if err := h.MembershipRemoved(hookCtx, id, tenant); err != nil {
			p.logger.Warn(ModuleName+": a membership lifecycle hook failed after the membership was removed, "+
				"and repeating the call finishes the cleanup",
				slog.String("account", id.String()), slog.String("tenant", tenant), slog.String("cause", describe(err)))
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", authn.ErrCleanupIncomplete, errors.Join(failures...))
}

// RevokeServiceSecret revokes a secret and then calls
// AccountLifecycle.ServiceSecretRevoked on every hook. A secret that is already
// revoked is revoked as it was, and the hooks are called again: that is how a
// call that was told the cleanup was incomplete is repeated.
func (p *product) RevokeServiceSecret(ctx context.Context, id authn.AccountID, secretID string) error {
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := p.lock(tx, id); err != nil {
			return err
		}
		var row secretRow
		err := tx.Where("id = ? AND account_id = ?", secretID, id.String()).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: the account has no such service secret", authn.ErrAccountNotFound)
		}
		if err != nil {
			return err
		}
		if row.RevokedAt != nil {
			return nil
		}
		return tx.Model(&secretRow{}).Where("id = ? AND revoked_at IS NULL", secretID).
			Updates(map[string]any{"revoked_at": p.at()}).Error
	})
	if err != nil {
		return failed("revoking a service secret", err)
	}
	secret := hookSecret
	return p.callHooks(ctx, id, &secret, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.ServiceSecretRevoked(hookCtx, id, secretID)
	})
}
