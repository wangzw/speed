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

const hookTimeout = 30 * time.Second

// lock takes the account's row by writing it; the write serializes concurrent
// transactions on one account on both engines, so the reads that follow in tx are
// stable. A missing account and a tombstone are authn.ErrAccountNotFound.
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

func ceilSecond(t time.Time) time.Time { return t.Truncate(time.Second).Add(time.Second) }

func reasonOK(reason string) error {
	if !authn.ValidPayloadReasonClass(reason) {
		return fmt.Errorf("%w: the reason is not one of the reason classes", authn.ErrInvalidArgument)
	}
	return nil
}

func (p *product) SetStatus(ctx context.Context, id authn.AccountID, s authn.AccountStatus, reason string) error {
	if s != authn.StatusDisabled && s != authn.StatusActive {
		return fmt.Errorf("%w: a status is set to disabled or enabled, and an account is ended with Delete", authn.ErrInvalidArgument)
	}
	if err := reasonOK(reason); err != nil {
		return err
	}
	if s == authn.StatusDisabled {
		release, err := p.lockAccountAdministration(ctx, id)
		if err != nil {
			return err
		}
		defer release()
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

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (p *product) Delete(ctx context.Context, id authn.AccountID, reason string) error {
	if err := reasonOK(reason); err != nil {
		return err
	}
	if err := checkID(id); err != nil {
		return err
	}
	release, err := p.lockAccountAdministration(ctx, id)
	if err != nil {
		return err
	}
	defer release()
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
		facts, factsErr := deletionFacts(tx, row)
		if factsErr != nil {
			return factsErr
		}
		if err = p.eraseAccount(tx, row, b); err != nil {
			return err
		}
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

func (p *product) lockAccountAdministration(ctx context.Context, id authn.AccountID) (func(), error) {
	counter := p.tenantAdminCounter
	if counter == nil {
		return func() {}, nil
	}
	release, err := counter.LockAccountAdministration(ctx, id.String())
	if err != nil {
		p.logger.Error(ModuleName+": the last-administrator locks could not be taken, so the account was not changed",
			slog.String("account", id.String()), slog.String("cause", err.Error()))
		return nil, fmt.Errorf("%w: taking the last tenant-administrator locks: %w", authn.ErrUnavailable, err)
	}
	return release, nil
}

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

func (p *product) refuseDeletingTheOwner(ctx context.Context, id authn.AccountID) error {
	return p.refuseAccountOwningAnOrganization(ctx, id, "deleting")
}

func (p *product) refuseSuspendingTheOwner(ctx context.Context, id authn.AccountID) error {
	return p.refuseAccountOwningAnOrganization(ctx, id, "suspending")
}

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
