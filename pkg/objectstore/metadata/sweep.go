package metadata

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// sweepBatch bounds how many rows one pass tombstones. A pass that read the
// whole table at once would hold the rows of every tenant in memory at once; a
// bounded batch leaves the rest for the next pass, and the sweep runs on its own
// interval so the backlog drains at a steady rate.
const sweepBatch = 200

// startSweep begins the background sweep unless the configured interval is zero
// or negative. It runs its first pass at once rather than after one interval,
// and that first pass is the module's first read of the objects table: the table
// exists from Migrate on, and nothing before Start touched it.
func (p *product) startSweep(ctx context.Context) {
	if p.sweepInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.cancelSweep, p.sweepDone = cancel, done
	go func() {
		defer close(done)
		p.sweepOnce(ctx)
		ticker := time.NewTicker(p.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.sweepOnce(ctx)
			}
		}
	}()
}

// stopSweep cancels the sweep and waits for its goroutine to return.
//
// The wait is what keeps a pass from outliving Stop: a read or a byte deletion
// in flight at that point would otherwise meet the database handle being closed
// or the backend being released, and report a failure on an ordinary shutdown.
func (p *product) stopSweep() {
	if p.cancelSweep == nil {
		return
	}
	p.cancelSweep()
	<-p.sweepDone
	p.cancelSweep, p.sweepDone = nil, nil
}

// expiredRow is one row the sweep found past its time: the tenant it belongs to,
// its identifier, and the internal key its bytes are stored under. The key is
// read here only to hand it back to the byte seam; it is never logged.
type expiredRow struct {
	TenantID string `gorm:"column:tenant_id"`
	ID       string `gorm:"column:id"`
	Key      string `gorm:"column:key"`
}

// sweepOnce runs one pass: read the rows whose time is up, remove each one's
// bytes through the seam, and tombstone its row.
//
// The pass crosses tenants — it is the database's own housekeeping, not a
// tenant's request — so it runs under a cross-tenant context. Every instant is
// judged against the database's own clock, so several replicas' sweeps agree on
// which row is past its time.
//
// The bytes are removed before the row is tombstoned, and the byte operation is
// outside any database transaction. A byte deletion that fails is a Warn and
// leaves the row as it is, for the next pass to retry; the row is never
// tombstoned while its bytes might still be there. A row whose window closed
// while an upload left bytes behind — a Put that succeeded and a state advance
// that did not — is the case the byte deletion exists for: the pending row
// cannot be assumed to have no bytes.
func (p *product) sweepOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	sweepCtx := tenant.CrossTenant(ctx)
	var rows []expiredRow
	if err := p.handle.WithContext(sweepCtx).Raw(p.stmts.expired, sweepBatch).Scan(&rows).Error; err != nil {
		p.logger.Warn("objectstore.metadata: the object sweep could not read the expired rows; "+
			"it will try again next pass", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if err := p.bytes.Delete(sweepCtx, row.Key); err != nil {
			p.logger.Warn("objectstore.metadata: the object sweep could not remove an expired object's "+
				"bytes; the row is left as it is and the sweep will try again next pass",
				"object", row.ID, "error", err)
			continue
		}
		if err := p.handle.WithContext(sweepCtx).Exec(p.stmts.tombstone, row.TenantID, row.ID).Error; err != nil {
			p.logger.Warn("objectstore.metadata: the object sweep could not tombstone an expired object; "+
				"the sweep will try again next pass", "object", row.ID, "error", err)
		}
	}
}
