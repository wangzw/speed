package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
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
// has committed every lifecycle hook is called, each of them idempotent, and a
// hook that fails makes the call authn.ErrCleanupIncomplete with the new state in
// force. Calling again with the same status finishes the cleanup: a status the
// account already has changes nothing and calls the hooks once more.
func (p *product) SetStatus(ctx context.Context, id authn.AccountID, s authn.AccountStatus, reason string) error {
	if s != authn.StatusDisabled && s != authn.StatusActive {
		return fmt.Errorf("%w: a status is set to disabled or enabled, and an account is ended with Delete", authn.ErrInvalidArgument)
	}
	if err := reasonOK(reason); err != nil {
		return err
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
	return nil
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
// the time of creation and the time of deletion, and every identifier, external
// identity, credential, secret and membership of the account is deleted in the
// same transaction, together with the personal data of the row itself. The
// instant before which no session is valid moves on, as a disabling moves it.
// Calling again on a tombstone finishes the cleanup the hooks did not.
func (p *product) Delete(ctx context.Context, id authn.AccountID, reason string) error {
	if err := reasonOK(reason); err != nil {
		return err
	}
	if err := checkID(id); err != nil {
		return err
	}
	b := &batch{}
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE "+tableAccounts+" SET updated_at = ? WHERE id = ?", p.at(), id.String())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("%w", authn.ErrAccountNotFound)
		}
		var row accountRow
		if err := tx.Where("id = ?", id.String()).Take(&row).Error; err != nil {
			return err
		}
		if row.Status == statusDeleted {
			return nil
		}
		now := p.at()
		var memberships []membershipRow
		if err := tx.Where("account_id = ?", id.String()).Order("tenant_id").Find(&memberships).Error; err != nil {
			return err
		}
		erase := map[string]any{
			"status":        statusDeleted,
			"display_name":  "",
			"attributes":    "{}",
			"mfa_required":  false,
			"protected":     false,
			"valid_after":   laterOf(utc(row.ValidAfter), ceilSecond(now)),
			"updated_at":    now,
			"last_login_at": nil,
			"disabled_at":   nil,
			"deleted_at":    now,
		}
		if err := tx.Model(&accountRow{}).Where("id = ?", id.String()).Updates(erase).Error; err != nil {
			return err
		}
		for _, table := range []any{&identifierRow{}, &externalRow{}, &secretRow{}, &membershipRow{}} {
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
	})
	if err != nil {
		return failed("deleting an account", err)
	}
	p.publish(ctx, b)
	return p.callHooks(ctx, id, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.AccountDeleted(hookCtx, id)
	})
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
