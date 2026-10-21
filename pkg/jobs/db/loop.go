package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/tenant"
)

// The bounds the loop runs by.
const (
	// statementTimeout bounds one statement of the loop's own, so a database
	// that stops answering stalls one executor rather than the queue.
	statementTimeout = 30 * time.Second
	// publishTimeout bounds one publish, so a bus that stops answering does
	// not hold an executor's round.
	publishTimeout = 30 * time.Second
	// cutoff is how long the stop stage waits for the executors once the
	// executions under way have been cancelled.
	cutoff = 5 * time.Second
	// errorLimit bounds the failure text a row keeps, in bytes.
	errorLimit = 2000
)

// worker is one execution coroutine. It carries the token it claims under,
// which names it in the rows it holds so that a release gives back its own
// rows and no other executor's, and the tenant the rotation of its next claim
// starts after.
type worker struct {
	token    string
	pivot    string
	failures int
}

// claimed is one row a claim took: what execution and settlement need of it.
type claimed struct {
	ID          string
	Tenant      string
	Type        string
	Priority    int
	Payload     []byte
	Attempts    int
	MaxRetries  int
	TimeoutMS   int64
	ScheduledAt int64
	StartedAt   *int64
	CreatedAt   int64
	UpdatedAt   int64
	ProgressPct int
	ProgressMsg string
}

// published is one terminal row whose event still owes publishing.
type published struct {
	ID     string `gorm:"column:id"`
	Tenant string `gorm:"column:tenant_id"`
	Type   string `gorm:"column:type"`
	Status string `gorm:"column:status"`
}

// view builds the job a handler sees out of a claimed row.
func (c claimed) view() *jobs.Job {
	return &jobs.Job{
		ID:          jobs.JobID(c.ID),
		Type:        c.Type,
		Status:      jobs.StatusRunning,
		Priority:    jobs.Priority(c.Priority),
		Payload:     c.Payload,
		Attempts:    c.Attempts,
		MaxRetries:  c.MaxRetries,
		Timeout:     time.Duration(c.TimeoutMS) * time.Millisecond,
		ProgressPct: c.ProgressPct,
		ProgressMsg: c.ProgressMsg,
		ScheduledAt: msTime(c.ScheduledAt),
		StartedAt:   msTimePtr(c.StartedAt),
		CreatedAt:   msTime(c.CreatedAt),
		UpdatedAt:   msTime(c.UpdatedAt),
	}
}

// runWorker is one execution coroutine: it reads the table, claims the due
// jobs, executes them and settles them, sleeping between rounds. On its way out
// it gives back every row it still holds.
func (q *queue) runWorker(w *worker) {
	defer q.release(w.token)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-q.stopping:
			return
		case <-timer.C:
		}
		delay, failed := q.pass(w)
		if failed {
			w.failures++
			// A table that cannot be read is read less often, so a database
			// that is down is not asked every poll and the record of it is not
			// written every poll either.
			delay = min(q.tuned.poll(q.logger)<<min(w.failures-1, 6), q.tuned.retryMax(q.logger))
		} else {
			w.failures = 0
		}
		timer.Reset(delay)
	}
}

// pass runs one round: it republishes what still owes an event, then claims and
// settles the due jobs until none is left. It reports how long until the next
// read, and whether a read failed.
//
// The poll interval is read here, at each round, so a value written while the
// process runs is what the next round waits.
func (q *queue) pass(w *worker) (time.Duration, bool) {
	poll := q.tuned.poll(q.logger)
	q.republish(w)
	for {
		if q.isStopping() {
			return 0, false
		}
		wait, staged, err := q.dueNext()
		if err != nil {
			q.logger.Error("jobs.db: reading when the next job falls due failed; the rows are left where they are",
				"error", err)
			return poll, true
		}
		switch {
		case !staged:
			return poll, false
		case wait > 0:
			// The earliest job is not due yet; read again then, and never
			// sleep longer than the poll interval, so a job enqueued with an
			// earlier time is noticed.
			return min(wait, poll), false
		}
		rows, err := q.claim(w)
		if err != nil {
			q.logger.Error("jobs.db: claiming the due jobs failed; the rows are left where they are", "error", err)
			return poll, true
		}
		if len(rows) == 0 {
			// Every due row is claimed by another executor at this moment, or
			// was claimed between the two statements.
			return poll, false
		}
		q.runBatch(w, rows)
	}
}

// dueNext reads how long until the earliest non-terminal job may be claimed,
// and whether any such job is in the table.
func (q *queue) dueNext() (time.Duration, bool, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var wait sql.NullInt64
	if err := q.handle.WithContext(ctx).Raw(q.sql.dueNext).Row().Scan(&wait); err != nil {
		return 0, false, fmt.Errorf("reading when the next job falls due: %w", err)
	}
	if !wait.Valid {
		return 0, false, nil
	}
	return time.Duration(wait.Int64) * time.Millisecond, true, nil
}

// claim takes up to one batch of due jobs for one executor, in the order the
// sort keys give, and advances the executor's rotation past the last tenant it
// took.
//
// The batch size and the lease are read here, at each claim, so a value written
// while the process runs is what the next claim takes and holds.
func (q *queue) claim(w *worker) ([]claimed, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	lease := q.tuned.lease(q.logger).Milliseconds()
	rows, err := q.handle.WithContext(ctx).
		Raw(q.sql.claim, w.token, lease, lease, w.pivot, q.tuned.batch(q.logger)).Rows()
	if err != nil {
		return nil, fmt.Errorf("claiming the due jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var taken []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.ID, &c.Tenant, &c.Type, &c.Priority, &c.Payload, &c.Attempts,
			&c.MaxRetries, &c.TimeoutMS, &c.ScheduledAt, &c.StartedAt, &c.CreatedAt, &c.UpdatedAt,
			&c.ProgressPct, &c.ProgressMsg); err != nil {
			return nil, fmt.Errorf("reading a claimed job: %w", err)
		}
		taken = append(taken, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the claimed jobs: %w", err)
	}
	if n := len(taken); n > 0 {
		// The next claim starts after the tenant the last one served, so a
		// tenant whose due jobs all sit at one sort position does not lead
		// every batch.
		w.pivot = taken[n-1].Tenant
	}
	return taken, nil
}

// runBatch executes the jobs one claim took, one at a time. A stop stage begun
// part way through leaves the rest held: they are given back on the executor's
// way out.
func (q *queue) runBatch(w *worker, rows []claimed) {
	for _, row := range rows {
		if q.isStopping() {
			return
		}
		q.execute(w, row)
	}
}

// execute runs one claimed job and settles it.
//
// It first reads whether a cancellation has been requested and whether it still
// holds the row: a claim that ran out and was taken over hands the row to
// another executor, and this one leaves it alone. Then it enters the row's
// tenant and hands the job to its handler, within the job's own timeout.
func (q *queue) execute(w *worker, row claimed) {
	cancelRequested, held, err := q.cancelCheck(w, row.ID)
	if err != nil {
		q.logger.Error("jobs.db: reading whether a claimed job was cancelled failed; the row stays claimed "+
			"until its claim runs out", "job_id", row.ID, "error", err)
		return
	}
	if !held {
		// The claim ran out and another executor took the row over.
		return
	}
	if cancelRequested {
		q.settleCancelled(w, row)
		return
	}
	handler, ok := q.handlerOf(row.Type)
	if !ok {
		q.settleFailure(w, row, fmt.Errorf("%w: no handler is registered for %q", jobs.ErrNoHandler, row.Type))
		return
	}
	// The tenant comes from the job row, not from the submitting caller: the
	// row carries the tenant it was written under, and the handler's scoped
	// statements have to reach that tenant.
	entered, err := q.catalog.Enter(q.work, tenant.ID(row.Tenant))
	if err != nil {
		// The roster does not carry the row's tenant, or no longer serves it.
		// That is this execution's failure, and the job is retried or given up
		// on like any other.
		q.settleFailure(w, row, err)
		return
	}
	job := row.view()
	ctx, cancel := context.WithTimeout(entered, job.Timeout)
	defer cancel()
	result, err := handler.Handle(ctx, job, func(pct int, msg string) {
		job.ProgressPct, job.ProgressMsg = pct, msg
		q.recordProgress(w, row.ID, pct, msg)
	})
	if expired := ctx.Err(); expired != nil {
		// The execution's context ended before the handler returned, which is a
		// failed execution whether or not the handler reported one: a handler
		// that does not heed its context still ran too long. The context ends
		// at the job's own timeout, or, at the stop stage, when the shutdown
		// grace is spent and the execution under way is cancelled.
		cause := fmt.Errorf("the execution ran past the job's timeout of %s and did not return: %w",
			job.Timeout, expired)
		if !errors.Is(expired, context.DeadlineExceeded) {
			cause = fmt.Errorf("the execution was cancelled before the handler returned: %w", expired)
		}
		q.settleFailure(w, row, cause)
		return
	}
	if err != nil {
		q.settleFailure(w, row, err)
		return
	}
	q.settleSucceeded(w, row, result)
}

// cancelCheck reports whether a row this executor still holds has had a
// cancellation requested. The second result is false when the executor no
// longer holds the row, which is what a claim taken over leaves behind.
func (q *queue) cancelCheck(w *worker, id string) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var requested bool
	err := q.handle.WithContext(ctx).Raw(q.sql.cancelCheck, id, w.token).Row().Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading the cancellation request: %w", err)
	}
	return requested, true, nil
}

// recordProgress writes the last progress a handler reported onto its row.
func (q *queue) recordProgress(w *worker, id string, pct int, msg string) {
	if _, err := q.exec(q.sql.progress, pct, msg, id, w.token); err != nil {
		q.logger.Warn("jobs.db: recording a job's progress failed; the job runs on", "job_id", id, "error", err)
	}
}

// settleSucceeded settles a job into succeeded and, with it, tries to publish
// its terminal event.
func (q *queue) settleSucceeded(w *worker, row claimed, result jobs.Result) {
	attempts := row.Attempts + 1
	if err := q.settle(q.sql.succeed, result.Data, attempts, row.ID, w.token); err != nil {
		q.settleMissed("succeeded", row.ID, err)
		return
	}
	q.publishTerminal(row, jobs.StatusSucceeded)
}

// settleCancelled settles a job the executor observed cancelled.
func (q *queue) settleCancelled(w *worker, row claimed) {
	if err := q.settle(q.sql.cancelled, row.ID, w.token); err != nil {
		q.settleMissed("cancelled", row.ID, err)
		return
	}
	q.publishTerminal(row, jobs.StatusCancelled)
}

// settleFailure settles a job whose execution failed: it is put back to run
// again after a backoff, or, once its attempts pass its retry limit, sent to
// dead_letter.
//
// The two retry bounds are read here, at the settlement, so a value written
// while the process runs is what the next backoff is computed from. A cap below
// the base delays the retry by the cap.
func (q *queue) settleFailure(w *worker, row claimed, cause error) {
	attempts := row.Attempts + 1
	text := truncate(cause.Error(), errorLimit)
	if attempts > row.MaxRetries {
		if err := q.settle(q.sql.deadLetter, text, attempts, row.ID, w.token); err != nil {
			q.settleMissed("dead_letter", row.ID, err)
			return
		}
		q.logger.Warn("jobs.db: a job failed past its retry limit and went to dead_letter",
			"job_id", row.ID, "type", row.Type, "attempts", attempts, "error", text)
		q.publishTerminal(row, jobs.StatusDeadLetter)
		return
	}
	backoff := retryAfter(q.tuned.retryBase(q.logger), q.tuned.retryMax(q.logger), attempts)
	if err := q.settle(q.sql.fail, text, attempts, backoff.Milliseconds(), row.ID, w.token); err != nil {
		q.settleMissed("retrying", row.ID, err)
		return
	}
	q.logger.Warn("jobs.db: a job failed and is retried after its backoff", "job_id", row.ID, "type", row.Type,
		"attempts", attempts, "max_retries", row.MaxRetries, "retry_in", backoff, "error", text)
}

// settleMissed records a settling statement that matched no row or failed. A
// miss is an ordinary outcome rather than a fault: another executor took the
// row over, and that one writes it, so the record is a warning naming the job
// and nothing else follows. A statement that failed for another reason is the
// executor's own to know about, and is recorded as an error.
func (q *queue) settleMissed(what, id string, err error) {
	if errors.Is(err, errClaimLost) {
		q.logger.Warn("jobs.db: the row was taken over before this executor settled it, so this execution's "+
			"outcome is dropped and the executor holding it writes the row", "job_id", id, "settle", what)
		return
	}
	q.logger.Error("jobs.db: settling a job failed; it is taken over once its claim runs out",
		"job_id", id, "settle", what, "error", err)
}

// settle runs a settling statement, which is conditional on this executor still
// holding the row. A statement that matched no row settles nothing: another
// executor took the row over, and that one writes it.
func (q *queue) settle(statement string, args ...any) error {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	res := q.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errClaimLost
	}
	return nil
}

// exec runs one statement of the loop's own, within the statement bound,
// reporting how many rows it touched.
func (q *queue) exec(statement string, args ...any) (int64, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	res := q.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// errClaimLost reports a settling statement that matched no row because
// another executor has taken the row over. The row is left as that executor
// finds it.
var errClaimLost = errors.New("jobs.db: the row is no longer held by this executor")

// release gives back every row one executor still holds, unexecuted and due at
// once, so another executor or the next start may claim them without waiting
// out the lease. It runs on a context of its own, since at the stop stage the
// loop's may already be cancelled.
func (q *queue) release(token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(q.work), statementTimeout)
	defer cancel()
	if err := q.handle.WithContext(ctx).Exec(q.sql.release, token).Error; err != nil {
		q.logger.Error("jobs.db: giving back the rows this executor held failed; they are taken over once "+
			"their claim runs out", "error", err)
	}
}

// republish sends the terminal events still owed: rows in a terminal status
// whose event has not been published. A bus that is absent owes nothing, and
// the rows keep their empty published time rather than being marked.
func (q *queue) republish(w *worker) {
	if q.bus == nil {
		return
	}
	rows, err := q.owing()
	if err != nil {
		q.logger.Error("jobs.db: reading the jobs whose terminal event is unpublished failed", "error", err)
		return
	}
	for _, row := range rows {
		if q.isStopping() {
			return
		}
		q.publishFrom(row.ID, row.Tenant, row.Type, jobs.Status(row.Status))
	}
}

// owing lists the terminal rows whose event has not been published. Its page is
// the claim batch, read here so it follows the same stored value a claim uses.
func (q *queue) owing() ([]published, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var rows []published
	err := q.handle.WithContext(ctx).Raw(q.sql.pendingPublish, q.tuned.batch(q.logger)).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the jobs owing a terminal event: %w", err)
	}
	return rows, nil
}

// publishTerminal tries to publish the terminal event of the row an executor
// just settled. A failure leaves the row's published time empty, so the
// republish pass takes it up on a later round; the terminal status is not
// undone.
func (q *queue) publishTerminal(row claimed, status jobs.Status) {
	if q.bus == nil {
		return
	}
	q.publishFrom(row.ID, row.Tenant, row.Type, status)
}

// publishFrom publishes one terminal event and, once the bus accepted it,
// records that the row no longer owes one.
func (q *queue) publishFrom(id, tenant, typ string, status jobs.Status) {
	evt := event.Event{
		// The job's identifier is the event's, so a repeated publish of the
		// same job's terminal event is the same event to a subscriber that
		// de-duplicates by it.
		ID:       id,
		Type:     terminalEventType(status),
		TenantID: tenant,
		Payload:  Terminal{ID: jobs.JobID(id), Type: typ, Status: status},
	}
	ctx, cancel := context.WithTimeout(q.work, publishTimeout)
	defer cancel()
	if err := q.bus.Publish(ctx, evt); err != nil {
		q.logger.Error("jobs.db: publishing a job's terminal event failed; the job keeps its terminal status "+
			"and the event is published again", "job_id", id, "event", evt.Type, "error", err)
		return
	}
	if _, err := q.exec(q.sql.markPublished, id); err != nil {
		q.logger.Error("jobs.db: recording that a terminal event was published failed; it is published "+
			"again", "job_id", id, "error", err)
	}
}

// terminalEventType names the event a job's terminal status publishes.
func terminalEventType(status jobs.Status) string {
	switch status {
	case jobs.StatusSucceeded:
		return SucceededEventType
	case jobs.StatusDeadLetter:
		return DeadLetterEventType
	case jobs.StatusCancelled:
		return CancelledEventType
	default:
		return ""
	}
}

// isStopping reports whether the stop stage has begun.
func (q *queue) isStopping() bool {
	select {
	case <-q.stopping:
		return true
	default:
		return false
	}
}

// truncate cuts s to at most limit bytes, on a character boundary.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
