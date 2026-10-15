package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

// insertJob writes one row directly, for a case that steers a row the executors
// are not running against. The run reaches the init stage, so the table exists
// and no executor is claiming.
func insertJob(t *testing.T, reg *core.Registry, id, status, claimedBy string, attempts int) {
	t.Helper()
	now := time.Now().UnixMilli()
	var claimed any
	if claimedBy != "" {
		claimed = claimedBy
	}
	update(t, reg, "INSERT INTO jobs (id, tenant_id, type, status, priority, attempts, max_retries, "+
		"timeout_ms, progress_pct, progress_msg, scheduled_at, available_at, claimed_by, claim_expires_at, "+
		"cancel_requested, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, "acme", jobType, status, 5, attempts, 3, 300000, 0, "", now, now, claimed, now, false, now, now)
}

// A settling statement is conditional on the executor still holding the row: one
// that no longer holds it writes nothing, and the row is left as the executor
// holding it finds it.
func TestASettleByAnExecutorThatLostTheClaimWritesNothing(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: quickJobs()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			p := product(t, reg)
			insertJob(t, reg, "j1", string(jobs.StatusRunning), "other-executor", 0)
			err := p.settle(p.sql.succeed, []byte("late"), 1, "j1", "this-executor")
			if !errors.Is(err, errClaimLost) {
				t.Errorf("settling a row held by another executor gave %v, want the claim reported lost", err)
			}
			got := stateOf(t, reg, jobs.JobID("j1"))
			if got.status != string(jobs.StatusRunning) || got.claimedBy != "other-executor" {
				t.Errorf("the row is %+v, want it left as the executor holding it found it", got)
			}
			return nil
		})
}

// Giving rows back returns each to the state it is claimed from again: a job
// that never ran goes back to pending, and one that has already failed goes
// back to retrying, both due at once and held by nobody.
func TestReleaseReturnsEachRowToTheStateItIsClaimedFromAgain(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: quickJobs()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			p := product(t, reg)
			insertJob(t, reg, "fresh", string(jobs.StatusRunning), "this-executor", 0)
			insertJob(t, reg, "failed", string(jobs.StatusRunning), "this-executor", 2)

			p.release("this-executor")

			for id, want := range map[string]jobs.Status{"fresh": jobs.StatusPending, "failed": jobs.StatusRetrying} {
				got := stateOf(t, reg, jobs.JobID(id))
				if got.status != string(want) {
					t.Errorf("the given-back job %s is %q, want %q", id, got.status, want)
				}
				if got.claimedBy != "" {
					t.Errorf("the given-back job %s is still held by %q", id, got.claimedBy)
				}
				if got.available > time.Now().UnixMilli() {
					t.Errorf("the given-back job %s is due at %d, want it due at once", id, got.available)
				}
			}
			return nil
		})
}
