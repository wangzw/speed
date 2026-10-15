package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

// A job that is enqueued runs: the executor claims it, enters its tenant, hands
// it to the handler registered for its type and settles it as succeeded, and a
// succeeded terminal event carries the job's identifier.
func TestAJobRunsAndSettles(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			rec := &recorder{typ: jobType}
			events := &sink{}
			e.on(t, host{
				bus:   true,
				jobs:  quickJobs(),
				extra: []core.Module{consumer(rec), events.module()},
			}).drive(t, func(_ context.Context, reg *core.Registry) error {
				q := queueOf(t, reg)
				acme := tenantCtx(t, reg, "acme")
				id, err := q.Enqueue(acme, jobs.Task{Type: jobType, Payload: []byte("hello")})
				if err != nil {
					t.Errorf("enqueuing the job: %v", err)
					return errCaseFailed
				}
				if !eventually(func() bool {
					return stateOf(t, reg, id).status == string(jobs.StatusSucceeded)
				}) {
					t.Errorf("the job is %q, want succeeded", stateOf(t, reg, id).status)
					return errCaseFailed
				}
				got := stateOf(t, reg, id)
				if got.attempts != 1 || got.result != "done" || got.claimedBy != "" {
					t.Errorf("the settled row is %+v, want one attempt, the handler's result and no claim", got)
				}
				// The handler ran under the job's own tenant, which the
				// executor entered from the row.
				if order := rec.order(); len(order) != 1 || order[0] != "acme" {
					t.Errorf("the handler ran under the tenants %v, want the job's own", order)
				}
				if !eventually(func() bool { return len(events.ofType(SucceededEventType)) == 1 }) {
					t.Errorf("the succeeded event never reached its subscriber")
					return errCaseFailed
				}
				for _, evt := range events.events() {
					if evt.Type == SucceededEventType && (evt.ID != string(id) || evt.TenantID != "acme") {
						t.Errorf("the succeeded event is %+v, want the job %s under its tenant", evt, id)
					}
				}
				return nil
			})
		})
	}
}

// A job left running by a replica that stopped answering is taken over once its
// claim runs out: the row is running with an expiry that has passed, and an
// executor claims it through the same due-ness predicate and runs it.
func TestALeftoverRunningJobIsTakenOverAfterItsClaimExpires(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			rec := &recorder{typ: jobType}
			e.on(t, host{
				jobs: withJobs(map[string]any{
					workersItem:      1,
					pollIntervalItem: "20ms",
				}),
				extra: []core.Module{consumer(rec)},
			}).drive(t, func(_ context.Context, reg *core.Registry) error {
				q := queueOf(t, reg)
				acme := tenantCtx(t, reg, "acme")
				// Due far out, so the executor does not claim it on its own:
				// this row is the leftover, not a fresh job.
				id, err := q.Enqueue(acme, jobs.Task{Type: jobType},
					jobs.WithScheduledAt(time.Now().Add(time.Hour)))
				if err != nil {
					t.Errorf("enqueuing the job: %v", err)
					return errCaseFailed
				}
				// What a replica that claimed the job and stopped answering
				// leaves behind: running, held by a token nobody carries, and
				// due again because its claim ran out.
				past := time.Now().Add(-time.Minute).UnixMilli()
				update(t, reg, "UPDATE jobs SET status = 'running', claimed_by = ?, claim_expires_at = ?, "+
					"available_at = ? WHERE id = ?", "dead-replica", past, past, string(id))
				if !eventually(func() bool {
					return stateOf(t, reg, id).status == string(jobs.StatusSucceeded)
				}) {
					t.Errorf("the leftover job is %q after %d runs, want it taken over and succeeded",
						stateOf(t, reg, id).status, rec.called())
					return errCaseFailed
				}
				if rec.called() != 1 {
					t.Errorf("the handler ran %d times, want the leftover job taken over once", rec.called())
				}
				if order := rec.order(); len(order) != 1 || order[0] != "acme" {
					t.Errorf("the takeover ran under the tenants %v, want the row's own", order)
				}
				return nil
			})
		})
	}
}

// Executors claiming at the same instant take each due job once: a job that is
// handed to a handler twice while its first claim is still good would be run
// twice by one process, which no replica failure accounts for. The row's
// available_at moves to the claim's expiry, so a row another executor has just
// taken is not due to this one.
func TestConcurrentExecutorsClaimAJobOnce(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			rec := &recorder{typ: jobType}
			e.on(t, host{
				jobs: withJobs(map[string]any{
					workersItem:      4,
					claimBatchItem:   4,
					pollIntervalItem: "20ms",
				}),
				extra: []core.Module{consumer(rec)},
			}).drive(t, func(_ context.Context, reg *core.Registry) error {
				q := queueOf(t, reg)
				acme := tenantCtx(t, reg, "acme")
				// All come due at one instant, so every executor's first claim
				// sees the same set of due jobs.
				at := time.Now().Add(300 * time.Millisecond)
				ids := make([]jobs.JobID, 0, 12)
				for i := 0; i < 12; i++ {
					id, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithScheduledAt(at))
					if err != nil {
						t.Errorf("enqueuing job %d: %v", i, err)
						return errCaseFailed
					}
					ids = append(ids, id)
				}
				if !eventually(func() bool { return rec.called() == len(ids) }) {
					t.Errorf("only %d of the %d jobs ran", rec.called(), len(ids))
					return errCaseFailed
				}
				time.Sleep(200 * time.Millisecond)
				for _, id := range ids {
					if n := rec.callsOf(id); n != 1 {
						t.Errorf("the job %s ran %d times, want once", id, n)
					}
					if got := stateOf(t, reg, id); got.status != string(jobs.StatusSucceeded) || got.attempts != 1 {
						t.Errorf("the job %s settled as %+v, want one attempt and succeeded", id, got)
					}
				}
				return nil
			})
		})
	}
}

// A failed job backs off and runs again; one that fails past its retry limit
// goes to dead_letter and publishes a dead_letter event.
func TestFailureBacksOffAndThenGoesToDeadLetter(t *testing.T) {
	rec := &recorder{
		typ:  jobType,
		fail: func(int, *jobs.Job) error { return errors.New("the work failed") },
	}
	events := &sink{}
	e := engines[0]
	e.on(t, host{
		bus: true,
		// A base far in the future keeps the retried job where it landed, so
		// the backoff it recorded is observable.
		jobs:  withJobs(map[string]any{retryBaseItem: "1h", retryMaxItem: "1h"}),
		extra: []core.Module{consumer(rec), events.module()},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		retried, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithMaxRetries(3))
		if err != nil {
			t.Errorf("enqueuing the retried job: %v", err)
			return errCaseFailed
		}
		dead, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithMaxRetries(0))
		if err != nil {
			t.Errorf("enqueuing the job with no retries: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool {
			return stateOf(t, reg, retried).status == string(jobs.StatusRetrying) &&
				stateOf(t, reg, dead).status == string(jobs.StatusDeadLetter)
		}) {
			t.Errorf("the retried job is %q and the other is %q, want retrying and dead_letter",
				stateOf(t, reg, retried).status, stateOf(t, reg, dead).status)
			return errCaseFailed
		}
		backing := stateOf(t, reg, retried)
		if backing.attempts != 1 || backing.error == "" {
			t.Errorf("the retried row is %+v, want one attempt with the failure recorded", backing)
		}
		if backing.available < time.Now().Add(30*time.Minute).UnixMilli() {
			t.Errorf("the retried job is due at %d, want it held back by the backoff", backing.available)
		}
		gone := stateOf(t, reg, dead)
		if gone.attempts != 1 || gone.error == "" {
			t.Errorf("the dead-lettered row is %+v, want one attempt with the failure recorded", gone)
		}
		if !eventually(func() bool { return len(events.ofType(DeadLetterEventType)) == 1 }) {
			t.Errorf("the dead_letter event never reached its subscriber")
		}
		return nil
	})
}

// An execution that runs past the job's own timeout is a failed execution even
// when its handler reports nothing: the queue settles it as a failure, and the
// job is retried.
func TestAnExecutionPastItsTimeoutFails(t *testing.T) {
	rec := &recorder{typ: jobType, sleep: 300 * time.Millisecond}
	e := engines[0]
	e.on(t, host{
		// A base far in the future keeps the retried job where it landed, so
		// the outcome of the timed-out execution is observable.
		jobs:  withJobs(map[string]any{retryBaseItem: "1h", retryMaxItem: "1h"}),
		extra: []core.Module{consumer(rec)},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType}, jobs.WithTimeout(50*time.Millisecond))
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool {
			return stateOf(t, reg, id).status == string(jobs.StatusRetrying)
		}) {
			t.Errorf("the job is %q after %d runs, want a time-out taken as a failure",
				stateOf(t, reg, id).status, rec.called())
			return errCaseFailed
		}
		got := stateOf(t, reg, id)
		if got.attempts != 1 || got.error == "" {
			t.Errorf("the timed-out row is %+v, want one failed attempt with its cause recorded", got)
		}
		return nil
	})
}

// The stop stage claims nothing more, and gives back every row an executor
// still holds, so another executor or the next start may claim them at once.
func TestStopGivesBackTheRowsAnExecutorHolds(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	rec := &recorder{
		typ:  jobType,
		hold: func(int) <-chan struct{} { return block },
	}
	e := engines[0]
	e.on(t, host{
		jobs: withJobs(map[string]any{
			workersItem:       1,
			shutdownGraceItem: "200ms",
		}),
		extra: []core.Module{consumer(rec)},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType})
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool {
			return stateOf(t, reg, id).status == string(jobs.StatusRunning) && rec.called() == 1
		}) {
			t.Errorf("the job was never claimed")
			return errCaseFailed
		}
		if err := product(t, reg).stop(context.Background()); err != nil {
			t.Errorf("stopping the queue: %v", err)
		}
		if !eventually(func() bool { return stateOf(t, reg, id).claimedBy == "" }) {
			t.Errorf("the row is still held by %q after the stop stage", stateOf(t, reg, id).claimedBy)
			return errCaseFailed
		}
		given := stateOf(t, reg, id)
		if given.status != string(jobs.StatusPending) || given.available > time.Now().UnixMilli() {
			t.Errorf("the given-back row is %+v, want it pending and due at once", given)
		}
		return nil
	})
}

// Claims rotate across tenants: among the jobs that are due at one instant, one
// tenant's rows do not lead every batch.
func TestClaimsRotateAcrossTenants(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			rec := &recorder{typ: jobType}
			e.on(t, host{
				jobs: withJobs(map[string]any{
					workersItem:      1,
					claimBatchItem:   1,
					pollIntervalItem: "20ms",
				}),
				extra: []core.Module{consumer(rec)},
			}).drive(t, func(_ context.Context, reg *core.Registry) error {
				q := queueOf(t, reg)
				alpha := tenantCtx(t, reg, "alpha")
				beta := tenantCtx(t, reg, "beta")
				// All four come due at one instant, so the tenant key is what
				// decides the order among them.
				at := time.Now().Add(300 * time.Millisecond)
				for i := 0; i < 3; i++ {
					if _, err := q.Enqueue(alpha, jobs.Task{Type: jobType, Payload: []byte("alpha")},
						jobs.WithScheduledAt(at)); err != nil {
						t.Errorf("enqueuing an alpha job: %v", err)
						return errCaseFailed
					}
				}
				if _, err := q.Enqueue(beta, jobs.Task{Type: jobType, Payload: []byte("beta")},
					jobs.WithScheduledAt(at)); err != nil {
					t.Errorf("enqueuing the beta job: %v", err)
					return errCaseFailed
				}
				if !eventually(func() bool { return rec.called() == 4 }) {
					t.Errorf("only %d of the four jobs ran", rec.called())
					return errCaseFailed
				}
				order := rec.order()
				if len(order) < 2 || order[0] == order[1] {
					t.Errorf("the executions began %v, want the claims to move to another tenant rather than "+
						"lead with the same one", order)
				}
				return nil
			})
		})
	}
}

// With no event bus the terminal events are not published, and the jobs run and
// settle exactly as they otherwise would.
func TestJobsRunWithNoEventBus(t *testing.T) {
	rec := &recorder{typ: jobType}
	e := engines[0]
	e.on(t, host{jobs: quickJobs(), extra: []core.Module{consumer(rec)}}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			q := queueOf(t, reg)
			acme := tenantCtx(t, reg, "acme")
			id, err := q.Enqueue(acme, jobs.Task{Type: jobType})
			if err != nil {
				t.Errorf("enqueuing the job: %v", err)
				return errCaseFailed
			}
			if !eventually(func() bool {
				return stateOf(t, reg, id).status == string(jobs.StatusSucceeded)
			}) {
				t.Errorf("the job is %q, want succeeded with no bus in the assembly",
					stateOf(t, reg, id).status)
				return errCaseFailed
			}
			if stateOf(t, reg, id).published != nil {
				t.Errorf("the row records a publish with no bus in the assembly")
			}
			return nil
		})
}

// A terminal event whose publish obligation is still open is published again by
// a later round, and the row records the publish once the bus accepted it.
func TestAnUnpublishedTerminalEventIsPublishedAgain(t *testing.T) {
	rec := &recorder{typ: jobType}
	events := &sink{}
	e := engines[0]
	e.on(t, host{
		bus:   true,
		jobs:  quickJobs(),
		extra: []core.Module{consumer(rec), events.module()},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		q := queueOf(t, reg)
		acme := tenantCtx(t, reg, "acme")
		id, err := q.Enqueue(acme, jobs.Task{Type: jobType})
		if err != nil {
			t.Errorf("enqueuing the job: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool { return stateOf(t, reg, id).published != nil }) {
			t.Errorf("the job's terminal event was never published")
			return errCaseFailed
		}
		// Reopen the publish obligation, as a replica that published and then
		// died before recording it leaves behind.
		update(t, reg, "UPDATE jobs SET terminal_published_at = NULL WHERE id = ?", string(id))
		if !eventually(func() bool {
			return len(events.ofType(SucceededEventType)) >= 2 && stateOf(t, reg, id).published != nil
		}) {
			t.Errorf("the reopened event was published %d times and the row records %v, want it taken up "+
				"again", len(events.ofType(SucceededEventType)), stateOf(t, reg, id).published)
			return errCaseFailed
		}
		return nil
	})
}
