package token

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// The shape of the sweep. It runs on every replica, once per interval, and
// deletes at most sweepBatch rows per statement and at most sweepBatches
// statements per run, so a single run is bounded and a backlog is worked off
// over several. The deletes are conditional, so two replicas sweeping at once
// delete nothing twice and need no coordination. stopWait is how long Stop
// waits: the context Stop is given has its cancellation removed, so the bound is
// the module's own.
const (
	sweepInterval = time.Hour
	sweepBatch    = 1000
	sweepBatches  = 100
	stopWait      = 5 * time.Second
)

// sessionTable is the session module's table, read here only by the sweep's
// delete condition: a refresh row is stale when the session it belongs to is
// gone, has been revoked, or has passed its absolute term. The name is fixed by
// design-authn-session, and the join is the read the design's bulk conditional
// delete needs.
const sessionTable = "authn_sessions"

// deleteStale is the statement the sweep runs: it removes the stale rows of one
// batch, bounded at sweepBatch, and the affected-row count is how many went. The
// four conditions and the bound are the design's closed set.
const deleteStale = "DELETE FROM " + tableName + " WHERE id IN (" +
	"SELECT r.id FROM " + tableName + " r LEFT JOIN " + sessionTable + " s ON s.id = r.session_id " +
	"WHERE s.id IS NULL OR s.revoked_at IS NOT NULL " +
	"OR (s.expires_at IS NOT NULL AND s.expires_at < ?) OR r.idle_expires_at < ? " +
	"LIMIT ?)"

// sweeper is the bounded goroutine that deletes the stale refresh rows of one
// product.
type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// The running sweep of each product. The product is built before its sweep
// starts and the sweep must be reachable from Stop, and one process may hold
// several products at once (a test does), so the pair is kept here rather than
// as a field of the product.
var (
	sweepMu      sync.Mutex
	sweepRunning = map[*product]*sweeper{}
)

// startSweep launches the sweep. It is called from Start, after the table has
// been probed: New does not touch the table, because on a first start it is not
// there until the migration has run. A second start of the same product is a
// no-op, so the goroutine is one per product.
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

// stopSweep ends the sweep and waits for it, at most stopWait. A sweep that is
// in the middle of a statement is cancelled with the statement.
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

// sweepLoop runs the sweep every interval until its context ends, and cancels
// the context as it returns so the goroutine releases what it was made with
// whether it was stopped or its caller's context ended first.
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
// deleted. A row is stale when its session is gone, revoked or past its
// absolute term, or when the row's own idle window has passed. Every condition
// is confined to rows that are already unusable, so deleting them cannot end a
// session a client still holds.
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
