package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/log"
)

const pruneEvery = time.Hour

const pruneRetention = 25 * time.Hour

const pruneStopWait = 5 * time.Second

var errPruneWait = errors.New("the ledger prune did not end in time")

type ledger struct {
	Store
	logger    *slog.Logger
	now       func() time.Time
	every     time.Duration
	stopBound time.Duration
	sweep     *sweeper
}

var _ Store = (*ledger)(nil)

func newLedger(store Store) *ledger {
	return &ledger{
		Store:     store,
		logger:    log.Default(),
		now:       time.Now,
		every:     pruneEvery,
		stopBound: pruneStopWait,
	}
}

type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *ledger) start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &sweeper{cancel: cancel, done: make(chan struct{})}
	l.sweep = s
	go func() {
		defer close(s.done)
		defer cancel()
		ticker := time.NewTicker(l.every)
		defer ticker.Stop()
		for {
			if _, err := l.prune(runCtx); err != nil && runCtx.Err() == nil {
				l.logger.Warn(ModuleName+": pruning the ledger failed", slog.String("cause", err.Error()))
			}
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (l *ledger) stop() error {
	s := l.sweep
	if s == nil {
		return nil
	}
	s.cancel()
	timer := time.NewTimer(l.stopBound)
	defer timer.Stop()
	select {
	case <-s.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: %s", errPruneWait, ModuleName)
	}
}

func (l *ledger) prune(ctx context.Context) (int64, error) {
	return l.PruneBefore(ctx, l.now().Add(-pruneRetention))
}
