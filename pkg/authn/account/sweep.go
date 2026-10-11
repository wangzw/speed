package account

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// The sweeps of the two tables this module owns that grow without bound, and how
// long Stop waits for them. Every replica runs both and the deletions are
// conditional, so two sweeps never conflict.
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
	p.sweepers.wg.Add(2)
	go p.sweepLoop(sweepCtx, cancel, p.sweepThrottlesEvery, "throttles", p.sweepThrottles)
	go p.sweepLoop(sweepCtx, cancel, p.sweepSecretsEvery, "service secrets", p.sweepSecrets)
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
	p.logger.Info(diagnosticPrefix+"the account store is running",
		slog.String("algorithm", string(p.cfg.algorithm)))
	if p.cfg.signupOpen {
		p.logger.Warn(diagnosticPrefix + ConfigNamespace + "." + signupItem + " is open: a first login through a " +
			"social provider or an SMS code makes an account")
	}
	if p.cfg.autoLink {
		p.logger.Warn(diagnosticPrefix + ConfigNamespace + "." + autoLinkItem + " is verified-email: a first social " +
			"login whose provider proved an email address is bound to the account that holds it")
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
	// what OWASP recommends at Error, so the startup says so as well.
	p.hashParams()
}

// sweepLoop runs one sweep at the start and again every interval until the
// context ends. It cancels the shared context as it returns, so the resources the
// sweeps were made with are released whether Stop cancelled them or the caller's
// own context ended first; the second goroutine's cancel is a no-op.
func (p *product) sweepLoop(ctx context.Context, cancel context.CancelFunc, every time.Duration, what string,
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
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
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
