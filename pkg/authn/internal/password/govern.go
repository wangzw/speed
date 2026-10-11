package password

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/vislake/speed/pkg/authn"
)

// DefaultMemoryBudgetKiB is the memory, in KiB, a Governor lets derivations
// hold at once when its owner states no other: 512 MiB.
const DefaultMemoryBudgetKiB = 512 * 1024

// DefaultWait is how long a derivation that finds the width or the memory taken
// waits for it before it is refused as busy, for a caller that waits at all: an
// interactive login waits this long, and the static table, whose every request
// carries a password, does not wait.
const DefaultWait = time.Second

// DefaultConcurrency is how many derivations run at once when nothing states
// another number: one for each processor the process may use, and no more than
// eight, because a derivation is the work of one processor for its whole
// duration and a request is not helped by more of them than there are to run.
func DefaultConcurrency() int { return min(runtime.GOMAXPROCS(0), 8) }

// Governor bounds what derivations may cost at once, in two ways: how many run
// and how much memory they hold. An Argon2id derivation takes the memory its
// entry states, and a budget on bytes is the only bound that holds when that
// number can differ from one entry to the next; PBKDF2 takes a slot and no
// memory share.
//
// Both bounds are fixed when the Governor is made. A derivation that cannot get
// what it needs is refused with authn.ErrBusy and does not queue behind the ones
// running, beyond the wait its caller allows: a backend that queued without
// bound would hand every goroutine the server has to whoever sends passwords
// fastest.
type Governor struct {
	slots       *semaphore.Weighted
	memory      *semaphore.Weighted
	memoryTotal int64
}

// NewGovernor makes a Governor that lets concurrency derivations run at once
// and hold memoryKiB between them. A value below one is raised to one.
func NewGovernor(concurrency int, memoryKiB int64) *Governor {
	concurrency = max(concurrency, 1)
	memoryKiB = max(memoryKiB, 1)
	return &Governor{
		slots:       semaphore.NewWeighted(int64(concurrency)),
		memory:      semaphore.NewWeighted(memoryKiB),
		memoryTotal: memoryKiB,
	}
}

// acquire takes a slot and, when memoryKiB is positive, a share of the memory
// budget, waiting for each at most wait, and returns what releases them. The
// error is authn.ErrBusy when the wait ran out, which is also the answer at once
// when wait is zero, and when the share asked for is more than the whole budget
// and can therefore never be had; and the context's own error when it is the
// caller's context that ended, which is not the Governor being full.
func (g *Governor) acquire(ctx context.Context, memoryKiB int64, wait time.Duration) (release func(), err error) {
	if memoryKiB > g.memoryTotal {
		return nil, fmt.Errorf("%w: a derivation needs %d KiB and the budget is %d KiB in all",
			authn.ErrBusy, memoryKiB, g.memoryTotal)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	take := func(s *semaphore.Weighted, n int64) error {
		if wait <= 0 {
			if s.TryAcquire(n) {
				return nil
			}
			return fmt.Errorf("%w: no capacity to start a derivation now", authn.ErrBusy)
		}
		if err := s.Acquire(waitCtx, n); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: no capacity to start a derivation within %s", authn.ErrBusy, wait)
		}
		return nil
	}

	if err := take(g.slots, 1); err != nil {
		return nil, err
	}
	if memoryKiB > 0 {
		if err := take(g.memory, memoryKiB); err != nil {
			g.slots.Release(1)
			return nil, err
		}
	}
	return func() {
		if memoryKiB > 0 {
			g.memory.Release(memoryKiB)
		}
		g.slots.Release(1)
	}, nil
}

// Verify checks the password against the entry, holding the capacity the
// derivation needs. It waits for that capacity at most wait; zero is not to wait.
func (g *Governor) Verify(ctx context.Context, e Entry, password string, wait time.Duration) (bool, error) {
	release, err := g.acquire(ctx, e.MemoryKiB(), wait)
	if err != nil {
		return false, err
	}
	defer release()
	return e.Verify(password)
}

// Hash writes an entry of the password with the parameters, holding the capacity
// the derivation needs.
func (g *Governor) Hash(ctx context.Context, p Params, password string, wait time.Duration) (string, error) {
	if err := p.Check(); err != nil {
		return "", err
	}
	var memory int64
	if p.Algorithm == Argon2id {
		memory = int64(p.MemoryKiB)
	}
	release, err := g.acquire(ctx, memory, wait)
	if err != nil {
		return "", err
	}
	defer release()
	return p.Hash(password)
}
