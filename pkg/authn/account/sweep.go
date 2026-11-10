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

// The sweeps this module runs and how long Stop waits for them. Every replica
// runs all of them and the deletions are conditional, so two sweeps never
// conflict. The throttles and service secrets sweep on a fixed interval; the
// unverified accounts sweep reads its interval from the settings before every
// pass.
const (
	throttleSweepEvery = 10 * time.Minute
	secretSweepEvery   = time.Hour
	stopWait           = 5 * time.Second

	// sweepBatch is how many rows one statement deletes, and sweepBatches how many
	// statements one pass runs at most: a pass is bounded, and what it leaves the
	// next pass takes.
	sweepBatch   = 1000
	sweepBatches = 100
)

// diagnosticPrefix starts the lines this module writes to the startup
// diagnostics, which name the capability directory and not the module.
const diagnosticPrefix = "authn: "

// sweepers is the goroutines the module starts and the means to stop them.
type sweepers struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// start probes the tables, writes the startup diagnostics and starts the sweeps.
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

// stop ends the sweeps and waits for them, at most the bound this module gives
// itself: a context given to Stop has its cancellation removed, so the wait is
// this module's to limit.
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

// probe looks for every table the module owns. A database in which they are
// missing was not migrated by a command that took this module in statically.
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

// diagnose writes the startup diagnostics: what the module runs on, each setting
// that relaxes a check, and what an assembly without an outbox or a bus does not
// have. A relaxation is a line of its own, so that it can be found in the log.
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
	// Judging the combination of the cost settings reports a combination below
	// what OWASP recommends at Error, so the startup says so as well. A read
	// fault is reported by that same call and needs no separate handling here.
	_, _ = p.hashParams()
}

// sweepLoop runs one sweep at the start and again every interval until the
// context ends. The interval is read before every pass, so a setting that
// states it takes effect without a restart. It cancels the shared context as it
// returns, so the resources the sweeps were made with are released whether Stop
// cancelled them or the caller's own context ended first; the other goroutines'
// cancel is a no-op.
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

// sweepThrottles deletes the rows of keys whose window has gone by without a failure
// and whose wait is over, a batch at a time. It reports how many it deleted.
func (p *product) sweepThrottles(ctx context.Context) (int64, error) {
	now := p.now.Now().Truncate(time.Microsecond)
	horizon := now.Add(-p.throttleParams().decay)
	return p.sweep(ctx, "DELETE FROM "+tableThrottles+" WHERE (scope, key_hash) IN (SELECT scope, key_hash FROM "+
		tableThrottles+" WHERE last_failure_at < ? AND (locked_until IS NULL OR locked_until <= ?) LIMIT ?)",
		horizon, now, sweepBatch)
}

// sweepSecrets deletes the rows of service secrets that expired or were revoked
// before the retention began, a batch at a time.
func (p *product) sweepSecrets(ctx context.Context) (int64, error) {
	cutoff := p.now.Now().Add(-p.retention()).Truncate(time.Microsecond)
	return p.sweep(ctx, "DELETE FROM "+tableServiceKeys+" WHERE id IN (SELECT id FROM "+tableServiceKeys+
		" WHERE (expires_at IS NOT NULL AND expires_at < ?) OR (revoked_at IS NOT NULL AND revoked_at < ?) LIMIT ?)",
		cutoff, cutoff, sweepBatch)
}

// sweepUnverified deletes self-registered accounts that never proved an email
// address, a batch of accounts at a time. A candidate is an account whose
// signup_state is pending, whose signup_verified_at is empty, that holds no email
// identifier written through a proving channel (invite is not one), and that has neither logged in
// nor been created within unverified-ttl; the conditions are read once to pick
// the batch and again inside each deletion, so an account that proved an address
// or logged in in between is skipped rather than deleted. An account that is not
// a self-registration and one whose registration is verified are never selected.
// A ttl of zero turns the sweep off.
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
				// The account is tombstoned even when a lifecycle hook failed;
				// the next sweep does not select it again. The failure is
				// reported and the rest of the batch goes on.
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

// deleteUnverified deletes one account the sweep selected, after re-judging in
// its own transaction that it is still an unverified registration that has been
// idle past the horizon. The conditions were judged when the batch was read, and
// the account may have proved an address or logged in since: the row is locked
// first, so the re-judgement sees whatever committed before the deletion and a
// write that comes after it waits for the lock, and an account that changed state
// is skipped. It reports whether an account was deleted.
func (p *product) deleteUnverified(ctx context.Context, id authn.AccountID, horizon time.Time) (bool, error) {
	b := &batch{}
	deleted := false
	ctx, after := provisioning.WithAfterCommit(ctx)
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := p.lock(tx, id)
		if errors.Is(err, authn.ErrAccountNotFound) {
			// The account is gone, or another pass tombstoned it: nothing is
			// left to skip or to delete.
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

// unverified reports whether a locked account is still a registration the sweep
// may delete: pending, never proved an address, and idle since before the
// horizon. The proof is judged on every email identifier the account holds, with
// the same rule the identifier write uses, so a channel this module does not
// count as a proof does not keep the account alive either.
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

// sweep runs a bounded delete until a statement deletes less than a batch, or the
// pass has run its statements.
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
