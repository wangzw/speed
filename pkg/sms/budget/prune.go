package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/log"
)

// pruneEvery is how often the retention sweep runs. The delete is one statement
// over attempted_at, and the window it keeps is a day, so an hourly run keeps
// the table bounded without a second job competing with the ledger's write path.
const pruneEvery = time.Hour

// pruneRetention is how long an attempt stays in the ledger. The longest budget
// window either consumer counts over is one day — the authentication daily
// windows and the notification per-recipient daily window — and the extra hour
// is the margin, so a row is never removed while a count can still read it. The
// cutoff compares the attempted time every row was charged at, which is the same
// instant the counting windows use, so the margin is the only slack the window
// needs.
const pruneRetention = 25 * time.Hour

// pruneStopWait is how long Stop waits for the sweep goroutine to end.
const pruneStopWait = 5 * time.Second

// errPruneWait is what Stop returns when the sweep did not end within
// pruneStopWait.
var errPruneWait = errors.New("the ledger prune did not end in time")

// ledger is the module's product: the store the consumers take up, together
// with the retention sweep over the table this module owns. It delivers Store by
// holding one, so a consumer sees the same calling surface whether the sweep is
// running or not.
type ledger struct {
	Store
	logger    *slog.Logger
	now       func() time.Time
	every     time.Duration
	stopBound time.Duration
	sweep     *sweeper
}

var _ Store = (*ledger)(nil)

// newLedger wraps store with the module's background work. The interval and the
// clock are fields so a case can drive the sweep without waiting an hour.
func newLedger(store Store) *ledger {
	return &ledger{
		Store:     store,
		logger:    log.Default(),
		now:       time.Now,
		every:     pruneEvery,
		stopBound: pruneStopWait,
	}
}

// sweeper is the bounded goroutine that removes the attempts past the retention
// window.
type sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// start begins the sweep. It sweeps once as it starts, so a process that
// restarts more often than the interval still prunes, and then every interval
// until stopped.
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

// stop ends the sweep and waits for it, at most stopBound.
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

// prune removes every row attempted before the retention window and answers how
// many it removed. It is the one place the cutoff is computed, so the store's
// PruneBefore stays a plain delete with no policy of its own.
func (l *ledger) prune(ctx context.Context) (int64, error) {
	return l.PruneBefore(ctx, l.now().Add(-pruneRetention))
}
