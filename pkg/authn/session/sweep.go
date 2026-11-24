package session

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// The sweep's shape. A sweep deletes at most sweepBatch rows in a statement and
// at most sweepBatches statements in a run, so one run is bounded and a backlog
// is worked off over several.
const (
	sweepInterval = 10 * time.Minute
	sweepBatch    = 1000
	sweepBatches  = 100
	stopWait      = 5 * time.Second
)

type sweepPace struct {
	interval time.Duration
}

type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// start probes the table and starts the sweep. A table that is not there means the
// migrations were not applied, a startup failure naming the remedy; New leaves the
// table alone because on a first start Migrate has not run yet.
func (p *product) start(ctx context.Context) error {
	if err := p.probe(ctx); err != nil {
		return err
	}
	p.sweepMu.Lock()
	defer p.sweepMu.Unlock()
	if p.sweep != nil {
		return nil
	}
	// The sweep outlives the context Start was handed, which belongs to the
	// stage, and ends when Stop cancels it.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &sweeper{cancel: cancel, done: make(chan struct{})}
	p.sweep = s
	go p.sweepLoop(runCtx, cancel, s.done)
	return nil
}

func (p *product) probe(ctx context.Context) error {
	err := p.handle.WithContext(ctx).Exec("SELECT 1 FROM " + tableName + " WHERE 1 = 0").Error
	switch {
	case err == nil:
		return nil
	case isMissingTable(err):
		return fmt.Errorf("%w: the table %s does not exist. Apply this module's migrations: include it "+
			"in the command that migrates the database, with every declaration of it taken in "+
			"(IncludeAllDeclarations)", authn.ErrNotMigrated, tableName)
	}
	return fmt.Errorf("%s%s: probing the table %s: %w", diagnosticPrefix, ModuleName, tableName, err)
}

func (p *product) stop(_ context.Context) {
	p.sweepMu.Lock()
	s := p.sweep
	p.sweep = nil
	p.sweepMu.Unlock()
	if s == nil {
		return
	}
	s.cancel()
	timer := time.NewTimer(stopWait)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		p.logger.Warn(diagnosticPrefix + ModuleName + ": the cleanup of ended sessions had not returned when the " +
			"wait for it ran out")
	}
}

// sweepLoop runs the sweep every interval until its context ends. It cancels the
// context as it returns, so the goroutine releases the resources it was made with
// whether it was stopped or the caller's own context ended first.
func (p *product) sweepLoop(ctx context.Context, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	defer cancel()
	ticker := time.NewTicker(p.sweepEvery.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.sweepOnce(ctx); err != nil && ctx.Err() == nil {
				p.logger.Warn(diagnosticPrefix+ModuleName+": cleaning up ended sessions failed",
					slog.Any("error", err))
			}
		}
	}
}

// sweepOnce deletes the ended rows past retention in batches and returns how many.
// A browser or token row ends at its absolute period; a service row is deleted only
// once revoked and past retention, and one with no expiry never by the clock.
func (p *product) sweepOnce(ctx context.Context) (int, error) {
	cutoff := clock.UTC(p.now().Add(-p.settings.retention()))
	total := 0
	for range sweepBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := p.exec(ctx, "deleting ended sessions",
			"DELETE FROM "+tableName+" WHERE id IN (SELECT id FROM "+tableName+
				" WHERE (kind IN ('browser', 'token') AND expires_at < ?)"+
				" OR (kind = 'service' AND revoked_at IS NOT NULL AND revoked_at < ?) LIMIT ?)",
			cutoff, cutoff, sweepBatch)
		if err != nil {
			return total, err
		}
		if n > 0 {
			p.count(ctx, opPurged)
		}
		total += int(n)
		if n < sweepBatch {
			return total, nil
		}
	}
	return total, nil
}
