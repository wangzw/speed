package db

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

func insertJob(t *testing.T, reg *core.Registry, id, status, claimedBy string, attempts int) {
	t.Helper()
	now := dbNowMilli(t, reg)
	var claimed any
	if claimedBy != "" {
		claimed = claimedBy
	}
	update(t, reg, "INSERT INTO jobs (id, tenant_id, type, status, priority, attempts, max_retries, "+
		"timeout_ms, progress_pct, progress_msg, scheduled_at, available_at, claimed_by, claim_expires_at, "+
		"cancel_requested, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, "acme", jobType, status, 5, attempts, 3, 300000, 0, "", now, now, claimed, now, false, now, now)
}

// TestASettleByAnExecutorThatLostTheClaimWritesNothing checks that a settling
// statement by an executor that no longer holds the row writes nothing.
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

// TestReleaseReturnsEachRowToTheStateItIsClaimedFromAgain checks that releasing
// puts a never-run job back to pending and a failed one to retrying, both due.
func TestReleaseReturnsEachRowToTheStateItIsClaimedFromAgain(t *testing.T) {
	e := engines[0]
	e.on(t, host{depth: core.StageInit, jobs: quickJobs()}).
		drive(t, func(_ context.Context, reg *core.Registry) error {
			p := product(t, reg)
			insertJob(t, reg, "fresh", string(jobs.StatusRunning), "this-executor", 0)
			insertJob(t, reg, "failed", string(jobs.StatusRunning), "this-executor", 2)

			p.release("this-executor")
			now := dbNowMilli(t, reg)

			for id, want := range map[string]jobs.Status{"fresh": jobs.StatusPending, "failed": jobs.StatusRetrying} {
				got := stateOf(t, reg, jobs.JobID(id))
				if got.status != string(want) {
					t.Errorf("the given-back job %s is %q, want %q", id, got.status, want)
				}
				if got.claimedBy != "" {
					t.Errorf("the given-back job %s is still held by %q", id, got.claimedBy)
				}
				if got.available > now {
					t.Errorf("the given-back job %s is due at %d, want it due at once", id, got.available)
				}
			}
			return nil
		})
}
