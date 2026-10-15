package db

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/tenant"
)

// jobType is the type the cases register a handler under.
const jobType = "demo.work"

// quickHost is a host running one handler of jobType that returns at once.
func quickHost(e engine, t *testing.T, rec *recorder) host {
	return e.on(t, host{jobs: quickJobs(), extra: []core.Module{consumer(rec)}})
}

// Enqueue writes one job, and a second submission carrying its idempotency key
// converges on the row already there: the same job comes back, and no second
// row is written. A key held by one tenant does not block another tenant's
// submission under it.
func TestEnqueueIsIdempotentOnTheKey(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		first, err := q.Enqueue(acme, jobs.Task{Type: jobType, IdempotencyKey: "k1"})
		if err != nil {
			t.Errorf("enqueuing the first job: %v", err)
			return errCaseFailed
		}
		again, err := q.Enqueue(acme, jobs.Task{Type: jobType, IdempotencyKey: "k1"})
		if err != nil {
			t.Errorf("re-enqueuing with the same key: %v", err)
			return errCaseFailed
		}
		if again != first {
			t.Errorf("the second submission returned %s, want the job already there, %s", again, first)
		}
		if n := rowCount(t, reg); n != 1 {
			t.Errorf("the table holds %d rows, want the one job", n)
		}
		globex := tenantCtx(t, reg, "globex")
		other, err := q.Enqueue(globex, jobs.Task{Type: jobType, IdempotencyKey: "k1"})
		if err != nil {
			t.Errorf("enqueuing under the same key in another tenant: %v", err)
			return errCaseFailed
		}
		if other == first {
			t.Errorf("another tenant's submission under the key returned the first tenant's job, %s", first)
		}
		if n := rowCount(t, reg); n != 2 {
			t.Errorf("the table holds %d rows, want one job per tenant", n)
		}
		return nil
	})
}

// Enqueue refuses an invalid task, a type with no handler and a context with no
// tenant, writing nothing in each case.
func TestEnqueueRefusalsWriteNothing(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		if _, err := q.Enqueue(acme, jobs.Task{Type: "   "}); !errors.Is(err, jobs.ErrInvalidTask) {
			t.Errorf("enqueuing a task with a blank type gave %v, want ErrInvalidTask", err)
		}
		if _, err := q.Enqueue(acme, jobs.Task{Type: "no.such.type"}); !errors.Is(err, jobs.ErrNoHandler) {
			t.Errorf("enqueuing a type with no handler gave %v, want ErrNoHandler", err)
		}
		if _, err := q.Enqueue(t.Context(), jobs.Task{Type: jobType}); !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("enqueuing with no tenant in the context gave %v, want ErrNoTenantInContext", err)
		}
		if n := rowCount(t, reg); n != 0 {
			t.Errorf("the table holds %d rows, want none written", n)
		}
		return nil
	})
}

// Get is scoped to the tenant of its context: another tenant's job, and an
// identifier no row carries, are both answered as ErrJobNotFound.
func TestGetIsScopedToTheTenant(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType, Payload: []byte("payload")})
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		got, err := q.Get(acme, id)
		if err != nil {
			t.Errorf("reading back the job: %v", err)
			return errCaseFailed
		}
		if got.ID != id || got.Type != jobType || string(got.Payload) != "payload" {
			t.Errorf("the job read back is %+v, want the one enqueued", got)
		}
		globex := tenantCtx(t, reg, "globex")
		if _, err := q.Get(globex, id); !errors.Is(err, jobs.ErrJobNotFound) {
			t.Errorf("another tenant reading the job gave %v, want ErrJobNotFound", err)
		}
		if _, err := q.Get(acme, "unknown"); !errors.Is(err, jobs.ErrJobNotFound) {
			t.Errorf("reading an unknown identifier gave %v, want ErrJobNotFound", err)
		}
		return nil
	})
}

// Cancel moves an unclaimed job straight to cancelled: the job never runs and
// its handler is never called.
func TestCancelOfAnUnclaimedJobSettlesIt(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		// Scheduled far out, so the executor never claims it and the
		// cancellation meets it unclaimed.
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithScheduledAt(time.Now().Add(time.Hour)))
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		if err := q.Cancel(acme, id); err != nil {
			t.Errorf("cancelling the job: %v", err)
			return errCaseFailed
		}
		got := stateOf(t, reg, id)
		if got.status != string(jobs.StatusCancelled) {
			t.Errorf("the job is %q, want cancelled", got.status)
		}
		time.Sleep(100 * time.Millisecond)
		if rec.called() != 0 {
			t.Errorf("the handler ran %d times, want none for a cancelled job", rec.called())
		}
		return nil
	})
}

// Cancel of a job that is claimed but not yet executed only has a cancellation
// requested: the claimant observes it before it executes and settles the job as
// cancelled without calling the handler.
func TestCancelOfAClaimedJobIsObservedBeforeExecution(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	// The high-priority job is claimed first and blocks; the second job of the
	// same batch is claimed but not yet executed when it is cancelled.
	rec := &recorder{
		typ:  jobType,
		hold: func(call int) <-chan struct{} { return block },
	}
	e := engines[0]
	// One worker and a batch of two: both jobs are claimed together, and the
	// second is claimed but not yet executed while the first blocks.
	e.on(t, host{
		jobs:  withJobs(map[string]any{workersItem: 1, claimBatchItem: 2}),
		extra: []core.Module{consumer(rec)},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		at := time.Now().Add(200 * time.Millisecond)
		blocking, err := q.Enqueue(acme, jobs.Task{Type: jobType, Payload: []byte("first")},
			jobs.WithScheduledAt(at), jobs.WithPriority(jobs.PriorityHigh))
		if err != nil {
			t.Errorf("enqueuing the blocking job: %v", err)
			return errCaseFailed
		}
		second, err := q.Enqueue(acme, jobs.Task{Type: jobType, Payload: []byte("second")},
			jobs.WithScheduledAt(at))
		if err != nil {
			t.Errorf("enqueuing the second job: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool { return stateOf(t, reg, blocking).status == string(jobs.StatusRunning) }) {
			t.Errorf("the blocking job was never claimed")
			return errCaseFailed
		}
		if err := q.Cancel(acme, second); err != nil {
			t.Errorf("cancelling the claimed job: %v", err)
			return errCaseFailed
		}
		held := stateOf(t, reg, second)
		if held.status != string(jobs.StatusRunning) || !held.cancel {
			t.Errorf("the claimed job is %q with cancel_requested %v, want it running with the request "+
				"recorded, not settled here", held.status, held.cancel)
		}
		block <- struct{}{}
		if !eventually(func() bool { return stateOf(t, reg, second).status == string(jobs.StatusCancelled) }) {
			t.Errorf("the claimed job is %q, want the claimant to settle it cancelled",
				stateOf(t, reg, second).status)
		}
		if rec.called() != 1 {
			t.Errorf("the handler ran %d times, want the blocking job alone", rec.called())
		}
		if got := rec.payloads(); len(got) != 1 || got[0] != "first" {
			t.Errorf("the handler ran the payloads %v, want the blocking job alone", got)
		}
		return nil
	})
}

// Cancel of a job already in a terminal status is an idempotent no-op.
func TestCancelOfATerminalJobChangesNothing(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType})
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool { return stateOf(t, reg, id).status == string(jobs.StatusSucceeded) }) {
			t.Errorf("the job never succeeded")
			return errCaseFailed
		}
		before := stateOf(t, reg, id)
		if err := q.Cancel(acme, id); err != nil {
			t.Errorf("cancelling a terminal job: %v", err)
		}
		after := stateOf(t, reg, id)
		if after.status != before.status || after.cancel {
			t.Errorf("the terminal job changed from %+v to %+v", before, after)
		}
		return nil
	})
}

// Cancel names a job the tenant cannot see as ErrJobNotFound.
func TestCancelOfAnInvisibleJobIsNotFound(t *testing.T) {
	rec := &recorder{typ: jobType}
	quickHost(engines[0], t, rec).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithScheduledAt(time.Now().Add(time.Hour)))
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		globex := tenantCtx(t, reg, "globex")
		if err := q.Cancel(globex, id); !errors.Is(err, jobs.ErrJobNotFound) {
			t.Errorf("another tenant cancelling the job gave %v, want ErrJobNotFound", err)
		}
		if err := q.Cancel(acme, "unknown"); !errors.Is(err, jobs.ErrJobNotFound) {
			t.Errorf("cancelling an unknown identifier gave %v, want ErrJobNotFound", err)
		}
		return nil
	})
}

// The queue refuses every call before it has started, and registering a handler
// once it has started is refused too.
func TestTheQueueServesOnlyBetweenStartAndStop(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageNew, jobs: quickJobs()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			q := queueOf(t, reg)
			if _, err := q.Enqueue(t.Context(), jobs.Task{Type: jobType}); !errors.Is(err, jobs.ErrNotRunning) {
				t.Errorf("Enqueue before the start stage gave %v, want ErrNotRunning", err)
			}
			if _, err := q.Get(t.Context(), "any"); !errors.Is(err, jobs.ErrNotRunning) {
				t.Errorf("Get before the start stage gave %v, want ErrNotRunning", err)
			}
			if err := q.Cancel(t.Context(), "any"); !errors.Is(err, jobs.ErrNotRunning) {
				t.Errorf("Cancel before the start stage gave %v, want ErrNotRunning", err)
			}
			return nil
		})

	rec := &recorder{typ: jobType}
	e.on(t, host{jobs: quickJobs(), extra: []core.Module{consumer(rec)}}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			handlers, err := core.Resolve[jobs.Handlers](reg)
			if err != nil {
				t.Errorf("resolving the handlers face: %v", err)
				return errCaseFailed
			}
			if err := handlers.Register(&recorder{typ: "late"}); !errors.Is(err, ErrHandlerSetFrozen) {
				t.Errorf("registering after the start stage gave %v, want ErrHandlerSetFrozen", err)
			}
			return nil
		})
}

// The handlers face refuses a duplicate type, an empty type and a nil handler,
// leaving the handler set as it was.
func TestRegisterRefusalsLeaveTheSetUnchanged(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageNew, jobs: quickJobs()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			handlers, err := core.Resolve[jobs.Handlers](reg)
			if err != nil {
				t.Errorf("resolving the handlers face: %v", err)
				return errCaseFailed
			}
			if err := handlers.Register(nil); !errors.Is(err, ErrInvalidHandler) {
				t.Errorf("registering nil gave %v, want ErrInvalidHandler", err)
			}
			if err := handlers.Register(&recorder{}); !errors.Is(err, ErrInvalidHandler) {
				t.Errorf("registering a handler with an empty type gave %v, want ErrInvalidHandler", err)
			}
			if err := handlers.Register(&recorder{typ: "a"}); err != nil {
				t.Errorf("registering a handler: %v", err)
			}
			if err := handlers.Register(&recorder{typ: "a"}); !errors.Is(err, ErrDuplicateHandler) {
				t.Errorf("registering a type twice gave %v, want ErrDuplicateHandler", err)
			}
			if got := handlers.Registered(); !slices.Equal(got, []string{"a"}) {
				t.Errorf("the registered types are %v, want the one accepted registration", got)
			}
			return nil
		})
}
