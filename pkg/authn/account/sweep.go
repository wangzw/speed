package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/authn/provisioning"
)

const (
	throttleSweepEvery = 10 * time.Minute
	secretSweepEvery   = time.Hour
	stopWait           = 5 * time.Second

	sweepBatch   = 1000
	sweepBatches = 100
)

const diagnosticPrefix = "authn: "

type sweepers struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (p *product) start(ctx context.Context) error {
	if err := p.probe(); err != nil {
		return err
	}
	p.diagnose()

	sweepCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.sweepers.cancel = cancel
	p.sweepers.wg.Add(3)
	go p.sweepLoop(sweepCtx, cancel, func() time.Duration { return p.sweepThrottlesEvery },
		"throttles", p.sweepThrottles)
	go p.sweepLoop(sweepCtx, cancel, func() time.Duration { return p.sweepSecretsEvery },
		"service secrets", p.sweepSecrets)
	go p.sweepLoop(sweepCtx, cancel, p.unverifiedSweepInterval,
		"unverified accounts", p.sweepUnverified)
	return nil
}

func (p *product) stop(_ context.Context) {
	if p.sweepers.cancel == nil {
		return
	}
	p.sweepers.cancel()
	done := make(chan struct{})
	go func() {
		p.sweepers.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(p.stopWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		p.logger.Warn(ModuleName + ": a sweep was still running when the wait for it ended, and is left to finish")
	}
}

func (p *product) probe() error {
	var missing []string
	for _, name := range tables {
		if !p.handle.Migrator().HasTable(name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s did not find %v. Take the module in statically in the command that applies the "+
		"migrations, which is IncludeAllDeclarations", authn.ErrNotMigrated, ModuleName, missing)
}

func (p *product) diagnose() {
	algorithm, err := p.passwordAlgorithm()
	if err != nil {
		algorithm = password.Algorithm(defaults.PasswordAlgorithm)
	}
	p.logger.Info(diagnosticPrefix+"the account store is running",
		slog.String("algorithm", string(algorithm)))
	if p.signup() == signupOpen {
		p.warnRelaxation(signupPath, moveRelaxed)
	}
	if p.passwordSignup() == signupOpen {
		p.warnRelaxation(passwordSignupPath, moveRelaxed)
	}
	switch {
	case p.outbox == nil && p.bus == nil:
		p.logger.Warn(diagnosticPrefix + "this assembly has no outbox and no event bus: no account event is published, " +
			"and erasing an account's data in the other modules is not guaranteed to finish once the call that started it " +
			"has returned an error")
	case p.outbox == nil:
		p.logger.Warn(diagnosticPrefix + "this assembly has no outbox: the events of a status change, a deletion and a " +
			"membership are published after the commit and at most once, and erasing an account's data in the other " +
			"modules is not guaranteed to finish when the process stops between the commit and the cleanup")
	}
	_, _ = p.hashParams()
}

func (p *product) sweepLoop(ctx context.Context, cancel context.CancelFunc, every func() time.Duration, what string,
	sweep func(context.Context) (int64, error),
) {
	defer p.sweepers.wg.Done()
	defer cancel()
	run := func() {
		if _, err := sweep(ctx); err != nil && ctx.Err() == nil {
			p.report("sweep "+what, "a sweep of the "+what+" failed and is tried again at the next pass: "+describe(err), describe(err))
			return
		}
		p.report("sweep "+what, "", "")
	}
	run()
	timer := time.NewTimer(every())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			run()
			timer.Reset(every())
		}
	}
}

func (p *product) sweepThrottles(ctx context.Context) (int64, error) {
	now := p.now.Now().Truncate(time.Microsecond)
	horizon := now.Add(-p.throttleParams().decay)
	return p.sweep(ctx, "DELETE FROM "+tableThrottles+" WHERE (scope, key_hash) IN (SELECT scope, key_hash FROM "+
		tableThrottles+" WHERE last_failure_at < ? AND (locked_until IS NULL OR locked_until <= ?) LIMIT ?)",
		horizon, now, sweepBatch)
}

func (p *product) sweepSecrets(ctx context.Context) (int64, error) {
	cutoff := p.now.Now().Add(-p.retention()).Truncate(time.Microsecond)
	return p.sweep(ctx, "DELETE FROM "+tableServiceKeys+" WHERE id IN (SELECT id FROM "+tableServiceKeys+
		" WHERE (expires_at IS NOT NULL AND expires_at < ?) OR (revoked_at IS NOT NULL AND revoked_at < ?) LIMIT ?)",
		cutoff, cutoff, sweepBatch)
}

// sweepUnverified deletes self-registered accounts that never proved an email
// address. Each selected account is re-judged under its row lock, so one that
// proved an address or logged in since is skipped.
func (p *product) sweepUnverified(ctx context.Context) (int64, error) {
	ttl := p.unverifiedTTL()
	if ttl <= 0 {
		return 0, nil
	}
	horizon := p.now.Now().Add(-ttl).Truncate(time.Microsecond)
	var total int64
	var firstErr error
	for range sweepBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var ids []string
		err := p.conn(ctx).Raw("SELECT a.id FROM "+tableAccounts+" a WHERE a.signup_state = ?"+
			" AND a.signup_verified_at IS NULL AND a.status <> ? AND COALESCE(a.last_login_at, a.created_at) < ?"+
			" AND NOT EXISTS (SELECT 1 FROM "+tableIdentifiers+" i WHERE i.account_id = a.id AND i.kind = ?"+
			" AND (i.verified_via IN (?, ?) OR i.verified_via LIKE ?)) LIMIT ?",
			signupPending, statusDeleted, horizon, string(authn.IdentifierEmail),
			authn.VerifiedViaSMSCode, authn.VerifiedViaEmailCode,
			authn.VerifiedViaIDPPrefix+"%", sweepBatch).Scan(&ids).Error
		if err != nil {
			return total, err
		}
		for _, id := range ids {
			deleted, err := p.deleteUnverified(ctx, authn.AccountID(id), horizon)
			if err != nil {
				// The account is tombstoned even when a lifecycle hook failed.
				if firstErr == nil {
					firstErr = err
				}
				p.report("sweep unverified accounts", "an unverified account could not be cleaned up fully: "+
					describe(err), describe(err))
				continue
			}
			if deleted {
				total++
			}
		}
		if len(ids) < sweepBatch {
			break
		}
	}
	return total, firstErr
}

func (p *product) deleteUnverified(ctx context.Context, id authn.AccountID, horizon time.Time) (bool, error) {
	b := &batch{}
	deleted := false
	ctx, after := provisioning.WithAfterCommit(ctx)
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := p.lock(tx, id)
		if errors.Is(err, authn.ErrAccountNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		still, err := p.unverified(tx, row, horizon)
		if err != nil {
			return err
		}
		if !still {
			return nil
		}
		facts, factsErr := deletionFacts(tx, row)
		if factsErr != nil {
			return factsErr
		}
		if err = p.eraseAccount(tx, row, b); err != nil {
			return err
		}
		if err = p.provisioning.ProvisionAccountDeletion(ctx, tx, facts); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return false, failed("deleting an unverified account", err)
	}
	p.publish(ctx, b)
	p.runAfterCommit(ctx, after)
	if !deleted {
		return false, nil
	}
	return true, p.callHooks(ctx, id, nil, func(hookCtx context.Context, h authn.AccountLifecycle) error {
		return h.AccountDeleted(hookCtx, id)
	})
}

func (p *product) unverified(tx *gorm.DB, row accountRow, horizon time.Time) (bool, error) {
	if signupStateOf(row.SignupState) != authn.SignupPending || row.SignupVerifiedAt != nil {
		return false, nil
	}
	idle := utc(row.CreatedAt)
	if row.LastLoginAt != nil {
		if at := utc(*row.LastLoginAt); at.After(idle) {
			idle = at
		}
	}
	if !idle.Before(horizon) {
		return false, nil
	}
	var identifiers []identifierRow
	if err := tx.Where("account_id = ? AND kind = ?", row.ID, string(authn.IdentifierEmail)).Find(&identifiers).Error; err != nil {
		return false, err
	}
	for _, i := range identifiers {
		if (authn.Verification{Via: i.VerifiedVia}).Proves() {
			return false, nil
		}
	}
	return true, nil
}

func (p *product) sweep(ctx context.Context, statement string, args ...any) (int64, error) {
	var total int64
	for range sweepBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res := p.conn(ctx).Exec(statement, args...)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < sweepBatch {
			break
		}
	}
	return total, nil
}
