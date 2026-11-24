package db

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

// TestAnEnqueuedJobIsDueAgainstTheClockTheClaimJudgesBy checks that an immediate
// enqueue writes available_at on the clock a claim judges due-ness by.
func TestAnEnqueuedJobIsDueAgainstTheClockTheClaimJudgesBy(t *testing.T) {
	const rounds = 50
	e := engines[0]
	e.on(t, host{
		depth: core.StageInit,
		jobs:  withJobs(map[string]any{claimBatchItem: 1}),
		extra: []core.Module{consumer(&recorder{typ: jobType})},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		p := product(t, reg)
		p.running.Store(true)
		defer p.running.Store(false)
		acme := tenantCtx(t, reg, "acme")

		for i := 0; i < rounds; i++ {
			id, err := p.Enqueue(acme, jobs.Task{Type: jobType})
			if err != nil {
				t.Fatalf("enqueuing the job for round %d: %v", i, err)
			}
			rows, err := p.claim(&worker{token: "clock-executor"})
			if err != nil {
				t.Fatalf("claiming after the round-%d enqueue: %v", i, err)
			}
			update(t, reg, "DELETE FROM jobs")
			if len(rows) != 1 || rows[0].ID != string(id) {
				t.Fatalf("the job enqueued in round %d was not due to the immediate claim, which took %d rows; "+
					"an immediate enqueue has to write available_at on the clock the claim judges by",
					i, len(rows))
			}
		}
		return nil
	})
}
