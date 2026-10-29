package db

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/jobs"
)

// TestAnEnqueuedJobIsDueAgainstTheClockTheClaimJudgesBy holds the contract
// between the instant an immediate enqueue writes into available_at and the
// instant a claim judges due-ness by: they are the same clock. A job submitted
// with no scheduled time is due at once, so the very next claim takes it.
//
// The case repeats the pair enough times to catch a clock skew of one
// millisecond, which a single pass would miss about half the time. The run
// reaches the init stage, where the table exists and no executor is running,
// and opens the submission face directly so nothing but the claim under test
// touches the row. The assertion distinguishes a row that was due only because
// the two clocks drifted apart from one that is due against the claim's own
// clock: a skewed write misses the immediate claim and is reported here.
func TestAnEnqueuedJobIsDueAgainstTheClockTheClaimJudgesBy(t *testing.T) {
	const rounds = 50
	e := engines[0]
	e.on(t, host{
		depth: core.StageInit,
		jobs:  withJobs(map[string]any{claimBatchItem: 1}),
		extra: []core.Module{consumer(&recorder{typ: jobType})},
	}).drive(t, func(_ context.Context, reg *core.Registry) error {
		p := product(t, reg)
		// No executor runs at the init stage; opening the submission face by
		// hand is what lets each enqueue be met by exactly one claim, with no
		// worker racing the case for the row.
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
