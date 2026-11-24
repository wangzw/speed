package token

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// The shape of the sweep: every replica runs it once per interval, deleting at most
// sweepBatch rows per statement and sweepBatches statements per run, so a run is
// bounded and two replicas need no coordination. stopWait bounds Stop's wait.
const (
	sweepInterval = time.Hour
	sweepBatch    = 1000
	sweepBatches  = 100
	stopWait      = 5 * time.Second
)

// sessionTable is the session module's table, read only by the sweep's delete
// condition, which treats a refresh row as stale when its session is gone.
const sessionTable = "authn_sessions"

// deleteStale removes the stale rows of one batch, bounded at sweepBatch; the
// affected-row count is how many went.
const deleteStale = "DELETE FROM " + tableName + " WHERE id IN (" +
	"SELECT r.id FROM " + tableName + " r LEFT JOIN " + sessionTable + " s ON s.id = r.session_id " +
	"WHERE s.id IS NULL OR s.revoked_at IS NOT NULL " +
	"OR (s.expires_at IS NOT NULL AND s.expires_at < ?) OR r.idle_expires_at < ? " +
	"LIMIT ?)"

type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// The running sweep of each product, keyed by the product: one process may hold
// several products at once.
var (
	sweepMu      sync.Mutex
	sweepRunning = map[*product]*sweeper{}
)

// startSweep launches the sweep from Start, after the table has been probed. A second
// start of the same product is a no-op, so the goroutine is one per product.
func (p *product) startSweep(ctx context.Context) {
	sweepMu.Lock()
	if _, running := sweepRunning[p]; running {
		sweepMu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &sweeper{cancel: cancel, done: make(chan struct{})}
	sweepRunning[p] = s
	sweepMu.Unlock()
	go p.sweepLoop(runCtx, cancel, s.done)
}

func (p *product) stopSweep() {
	sweepMu.Lock()
	s := sweepRunning[p]
	delete(sweepRunning, p)
	sweepMu.Unlock()
	if s == nil {
		return
	}
	s.cancel()
	timer := time.NewTimer(stopWait)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		p.logger.Warn(diagnosticPrefix + ModuleName + ": the cleanup of refresh tokens had not returned when the " +
			"wait for it ran out")
	}
}

// sweepLoop runs the sweep every interval until its context ends, and cancels the
// context as it returns so the goroutine releases what it was made with.
func (p *product) sweepLoop(ctx context.Context, cancel context.CancelFunc, done chan<- struct{}) {
	p.sweepRuns.Add(1)
	defer p.sweepRuns.Add(-1)
	defer close(done)
	defer cancel()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.sweepOnce(ctx); err != nil && ctx.Err() == nil {
				p.logger.Warn(diagnosticPrefix+ModuleName+": cleaning up refresh tokens failed",
					slog.String("cause", err.Error()))
			}
		}
	}
}

// sweepOnce deletes the stale refresh rows in batches and returns how many it
// deleted; every condition is confined to rows that are already unusable.
func (p *product) sweepOnce(ctx context.Context) (int, error) {
	now := clock.UTC(p.clock.Now())
	total := 0
	for range sweepBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res := p.refresh.handle.WithContext(ctx).Exec(deleteStale, now, now, sweepBatch)
		if res.Error != nil {
			return total, refreshUnavailable("cleaning up refresh tokens", res.Error)
		}
		n := int(res.RowsAffected)
		total += n
		if n < sweepBatch {
			return total, nil
		}
	}
	return total, nil
}
