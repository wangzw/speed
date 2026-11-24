package metadata

import (
	"context"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

const sweepBatch = 200

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

func (p *product) stopSweep() {
	if p.cancelSweep == nil {
		return
	}
	p.cancelSweep()
	<-p.sweepDone
	p.cancelSweep, p.sweepDone = nil, nil
}

type expiredRow struct {
	TenantID string `gorm:"column:tenant_id"`
	ID       string `gorm:"column:id"`
	Key      string `gorm:"column:key"`
}

func (p *product) sweepOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	sweepCtx := tenant.CrossTenant(ctx)
	p.sweepObjects(sweepCtx, ctx)
	if err := p.handle.WithContext(sweepCtx).Exec(p.stmts.purgeGrants).Error; err != nil {
		p.logger.Warn("objectstore.metadata: the sweep could not remove the expired grants; "+
			"it will try again next pass", "error", err)
	}
}

func (p *product) sweepObjects(sweepCtx, ctx context.Context) {
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
